//go:build integration

package tui

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	openclawBackend "github.com/lucinate-ai/lucinate/internal/backend/openclaw"
	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// dockerGatewayContainer is the compose service container name for the local
// integration gateway. The repro drops it mid-session to mimic the gateway
// becoming unreachable under an established WebSocket.
const dockerGatewayContainer = "integration-gateway-1"

// dockerCmd runs a docker subcommand and fails the test on error.
func dockerCmd(t *testing.T, args ...string) {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
	t.Logf("docker %v: %s", args, out)
}

// TestSendWhileDisconnected_Integration reproduces the crash report: with an
// established connection to the gateway, the transport drops and the user
// submits a message.
//
// LUCINATE_DROP_MODE selects how the gateway goes away:
//   - "stop" (default): `docker stop` — a clean WebSocket close. The SDK read
//     loop ends, the supervisor nils out the gateway client and re-dials (which
//     fails while the container is down), so the submission lands while
//     c.gw == nil. This is the path the original panic came from.
//   - "pause": `docker pause` — freezes the container with no FIN/RST, exactly
//     like a tailscale tunnel disappearing. The write blocks indefinitely; the
//     submission goroutine hangs rather than returning.
//
// It drives the same plumbing app.go runs: an events pump and the connection
// supervisor, both feeding tea messages into the model, with submission going
// through the full chatModel.Update path. The submission runs in a goroutine
// with NO panic recover, so any nil-deref crashes the test process with a full
// stack trace; a hang is detected by timeout instead of blocking the suite.
//
// Run with:
//
//	docker compose -f test/integration/docker-compose.yml up -d --wait
//	go test -tags integration -run TestSendWhileDisconnected ./internal/tui/ -v -count=1
func TestSendWhileDisconnected_Integration(t *testing.T) {
	dropMode := os.Getenv("LUCINATE_DROP_MODE")
	if dropMode == "" {
		dropMode = "stop"
	}

	// Always restore the gateway on exit so the next run finds it healthy.
	t.Cleanup(func() {
		_ = exec.Command("docker", "unpause", dockerGatewayContainer).Run()
		_ = exec.Command("docker", "start", dockerGatewayContainer).Run()
	})

	c := connectTestClient(t)
	defer c.Close()

	agents, err := c.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents.Agents) == 0 {
		t.Fatal("no agents available on gateway")
	}
	agentName := agents.Agents[0].Name
	if agentName == "" {
		agentName = agents.Agents[0].ID
	}

	// Build the model through the production constructor so the textarea,
	// viewport and renderer are fully initialised — chatModel.Update has a
	// value receiver returning a value, so we drive a chatModel value.
	b := openclawBackend.New(c)
	m := newChatModel(b, agents.MainKey, agents.Agents[0].ID, agentName, "", config.Preferences{}, false, "localhost", "", false)
	m.historyLoading = false
	m.connState = ConnStateMsg{Status: client.StatusConnected}

	// msgCh carries every tea.Msg the model would receive in the real app:
	// gateway events and connection-state transitions.
	msgCh := make(chan tea.Msg, 64)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)

	// Events pump — mirrors app.go's first driver goroutine.
	go func() {
		defer wg.Done()
		events := c.Events()
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					return
				}
				select {
				case msgCh <- GatewayEventMsg(ev):
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Supervisor — mirrors app.go's second driver goroutine. On the transport
	// drop this nils out the gateway client and emits Disconnected/Reconnecting.
	go func() {
		defer wg.Done()
		c.Supervise(ctx, func(s client.ConnState) {
			select {
			case msgCh <- ConnStateMsg{Status: s.Status, Attempt: s.Attempt, Err: s.Err}:
			case <-ctx.Done():
			}
		})
	}()

	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	// observed receives every non-batch tea.Msg runCmd produces, so the
	// submission can assert on the send's own result (chatSentMsg) rather
	// than on the whole transitive cmd chain — the post-send history refresh
	// is a separate, independently-bounded RPC and would otherwise stack its
	// own timeout onto the measurement.
	observed := make(chan tea.Msg, 64)

	// runCmd executes a tea.Cmd the way the program loop would, fanning out
	// batches sequentially, and routes any produced msg back into the model.
	// No recover(): a panic here is the bug we are hunting and should abort
	// the test with a stack trace.
	var runCmd func(cmd tea.Cmd)
	runCmd = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		msg := cmd()
		if msg == nil {
			return
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				runCmd(sub)
			}
			return
		}
		select {
		case observed <- msg:
		default:
		}
		m, cmd = m.Update(msg)
		runCmd(cmd)
	}

	// Drop the gateway.
	switch dropMode {
	case "pause":
		t.Log(">>> pausing gateway container (silent freeze, no clean close)")
		dockerCmd(t, "pause", dockerGatewayContainer)
	default:
		t.Log(">>> stopping gateway container (clean WebSocket close)")
		dockerCmd(t, "stop", dockerGatewayContainer)
	}

	// Wait until the supervisor reports a non-connected state, draining msgCh
	// through Update as the program loop would. For the "stop" path this is
	// deterministic (clean close → Disconnected within ~1s). For "pause" it
	// may never fire before the TCP keepalive lapses, so cap the wait.
	deadline := time.After(20 * time.Second)
	sawDrop := false
waitDrop:
	for !sawDrop {
		select {
		case msg := <-msgCh:
			if cs, ok := msg.(ConnStateMsg); ok && cs.Status != client.StatusConnected {
				sawDrop = true
			}
			var cmd tea.Cmd
			m, cmd = m.Update(msg)
			runCmd(cmd)
		case <-deadline:
			t.Logf("note: no disconnect state observed within deadline (drop mode %q)", dropMode)
			break waitDrop
		}
	}

	// Now the user types a message and hits Enter while disconnected. Run it in
	// a goroutine so a hung write (the "pause" path) does not block the suite;
	// a panic still crashes the process with a stack trace.
	t.Logf(">>> submitting message while disconnected (sending=%v connState=%v sawDrop=%v)", m.sending, m.connState.Status, sawDrop)
	m.textarea.SetValue("hello while disconnected")

	go func() {
		var cmd tea.Cmd
		m, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		runCmd(cmd)
	}()

	// The send must produce its chatSentMsg result — panic-free, and bounded.
	// On the "stop" path it returns almost immediately via ErrNotConnected; on
	// the "pause" path it blocks on the dead transport until the client's
	// defaultRPCTimeout turns it into an error. The guard sits above that one
	// timeout with margin; before the fix the send blocked forever. Follow-up
	// RPCs (the history refresh) are each independently bounded and run on in
	// the background — not part of this measurement.
	const sendGuard = 45 * time.Second
	for {
		select {
		case msg := <-observed:
			if sent, ok := msg.(chatSentMsg); ok {
				t.Logf(">>> send returned without panic or unbounded hang (err=%v)", sent.err)
				return
			}
		case <-time.After(sendGuard):
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			t.Logf("goroutine dump at hang:\n%s", buf[:n])
			t.Fatal("send hung past the RPC timeout — a gateway call blocked on a dead transport without a deadline")
		}
	}
}
