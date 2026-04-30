package httpcommon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a3tai/openclaw-go/protocol"
)

func TestClient_NewRequest_SetsBearerAuth(t *testing.T) {
	c, err := NewClient(Options{BaseURL: "http://example.com/v1", APIKey: "secret"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	req, err := c.NewRequest(context.Background(), http.MethodGet, "/models", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer secret" {
		t.Errorf("Authorization = %q want %q", got, "Bearer secret")
	}
	if got := req.URL.String(); got != "http://example.com/v1/models" {
		t.Errorf("URL = %q want %q", got, "http://example.com/v1/models")
	}
}

func TestClient_NewRequest_NoAuthHeaderWhenKeyEmpty(t *testing.T) {
	c, _ := NewClient(Options{BaseURL: "http://x/v1"})
	req, _ := c.NewRequest(context.Background(), http.MethodGet, "/models", nil)
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q, want empty", got)
	}
}

func TestClient_SetAPIKey_TakesEffectImmediately(t *testing.T) {
	c, _ := NewClient(Options{BaseURL: "http://x/v1", APIKey: "old"})
	c.SetAPIKey("new")
	req, _ := c.NewRequest(context.Background(), http.MethodGet, "/models", nil)
	if got := req.Header.Get("Authorization"); got != "Bearer new" {
		t.Errorf("Authorization = %q want %q", got, "Bearer new")
	}
}

func TestClient_NewRequest_JSONEncodesBody(t *testing.T) {
	c, _ := NewClient(Options{BaseURL: "http://x/v1"})
	req, err := c.NewRequest(context.Background(), http.MethodPost, "/chat", map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	body, _ := io.ReadAll(req.Body)
	if !strings.Contains(string(body), `"k":"v"`) {
		t.Errorf("body = %s", body)
	}
}

func TestClient_Do_RoundTrips(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c, _ := NewClient(Options{BaseURL: srv.URL})
	req, _ := c.NewRequest(context.Background(), http.MethodGet, "/", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestScanSSE_DispatchesDataLines(t *testing.T) {
	body := strings.NewReader("data: a\n\ndata: b\nevent: foo\n: a comment\ndata: c\n\n")
	var seen []string
	err := ScanSSE(body, func(payload string) bool {
		seen = append(seen, payload)
		return false
	})
	if err != nil {
		t.Fatalf("ScanSSE: %v", err)
	}
	want := []string{"a", "b", "c"}
	if len(seen) != len(want) {
		t.Fatalf("got %d payloads, want %d (%v)", len(seen), len(want), seen)
	}
	for i, w := range want {
		if seen[i] != w {
			t.Errorf("seen[%d] = %q, want %q", i, seen[i], w)
		}
	}
}

func TestScanSSE_StopEarly(t *testing.T) {
	body := strings.NewReader("data: a\n\ndata: STOP\n\ndata: never\n\n")
	var seen []string
	_ = ScanSSE(body, func(payload string) bool {
		seen = append(seen, payload)
		return payload == "STOP"
	})
	if len(seen) != 2 || seen[1] != "STOP" {
		t.Errorf("seen = %v", seen)
	}
}

func TestEventEmitter_EmitChatDelta(t *testing.T) {
	e := NewEventEmitter(4)
	e.EmitChatDelta("run-1", "session-1", "hello")
	select {
	case ev := <-e.Channel():
		if ev.EventName != protocol.EventChat {
			t.Errorf("EventName = %s", ev.EventName)
		}
		var ce protocol.ChatEvent
		if err := json.Unmarshal(ev.Payload, &ce); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if ce.State != "delta" {
			t.Errorf("State = %q", ce.State)
		}
		var content string
		if err := json.Unmarshal(ce.Message, &content); err != nil {
			t.Fatalf("decode message: %v", err)
		}
		if content != "hello" {
			t.Errorf("content = %q", content)
		}
	default:
		t.Fatal("no event")
	}
}

func TestEventEmitter_DropsWhenFull(t *testing.T) {
	e := NewEventEmitter(1)
	e.EmitChatDelta("r", "s", "first")
	// second send should be dropped silently rather than blocking
	done := make(chan struct{})
	go func() {
		e.EmitChatDelta("r", "s", "dropped")
		close(done)
	}()
	<-done
	count := 0
	for {
		select {
		case <-e.Channel():
			count++
		default:
			if count != 1 {
				t.Errorf("count = %d, want 1", count)
			}
			return
		}
	}
}

func TestEventEmitter_CloseAndSendIsSafe(t *testing.T) {
	e := NewEventEmitter(1)
	e.Close()
	// must not panic
	e.EmitChatDelta("r", "s", "ignored")
}
