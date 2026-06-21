package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/lucinate-ai/lucinate/internal/config"
)

func TestNewAskFlagSet_AppliesDefaults(t *testing.T) {
	defaults := config.AskDefaults{Connection: "home", Agent: "bot", Session: "s1", Detach: true}
	var (
		connection, agent, session string
		detach                     bool
	)
	fs := newAskFlagSet(&connection, &agent, &session, &detach, defaults)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if connection != "home" || agent != "bot" || session != "s1" || !detach {
		t.Errorf("defaults not applied: c=%q a=%q s=%q d=%v", connection, agent, session, detach)
	}
}

func TestNewAskFlagSet_FlagsOverrideDefaults(t *testing.T) {
	defaults := config.AskDefaults{Connection: "home", Agent: "bot"}
	var (
		connection, agent, session string
		detach                     bool
	)
	fs := newAskFlagSet(&connection, &agent, &session, &detach, defaults)
	if err := fs.Parse([]string{"-c", "work", "-a", "other"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if connection != "work" || agent != "other" {
		t.Errorf("flags should override defaults: c=%q a=%q", connection, agent)
	}
}

func TestRunAsk_MissingMessage(t *testing.T) {
	t.Setenv("LUCINATE_DATA_DIR", t.TempDir())
	var buf bytes.Buffer
	err := runAsk(context.Background(), nil, &buf)
	if err == nil || !strings.Contains(err.Error(), "missing message") {
		t.Fatalf("expected missing-message error, got %v", err)
	}
}

func TestRunAsk_NoConnectionConfigured(t *testing.T) {
	// Empty data dir → default prefs with a blank Ask connection.
	t.Setenv("LUCINATE_DATA_DIR", t.TempDir())
	var buf bytes.Buffer
	err := runAsk(context.Background(), []string{"hello", "there"}, &buf)
	if err == nil || !strings.Contains(err.Error(), "no connection configured") {
		t.Fatalf("expected no-connection error, got %v", err)
	}
}

func TestRunAsk_NoAgentConfigured(t *testing.T) {
	t.Setenv("LUCINATE_DATA_DIR", t.TempDir())
	var buf bytes.Buffer
	err := runAsk(context.Background(), []string{"-c", "home", "hello"}, &buf)
	if err == nil || !strings.Contains(err.Error(), "no agent configured") {
		t.Fatalf("expected no-agent error, got %v", err)
	}
}

func TestRunHelpAsk(t *testing.T) {
	var buf bytes.Buffer
	if err := runHelp([]string{"ask"}, &buf); err != nil {
		t.Fatalf("runHelp(ask): %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Usage: lucinate ask", "-connection", "-agent", "-detach"} {
		if !strings.Contains(out, want) {
			t.Errorf("ask help missing %q\n--- output ---\n%s", want, out)
		}
	}
}
