package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunHelpTopLevel(t *testing.T) {
	var buf bytes.Buffer
	if err := runHelp(nil, &buf); err != nil {
		t.Fatalf("runHelp(nil): %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Usage: lucinate",
		"Commands:",
		"send",
		"chat",
		"help",
		"--version",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("top-level help missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRunHelpSend(t *testing.T) {
	var buf bytes.Buffer
	if err := runHelp([]string{"send"}, &buf); err != nil {
		t.Fatalf("runHelp(send): %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Usage: lucinate send",
		"-connection",
		"-agent",
		"-session",
		"-detach",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("send help missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRunHelpChat(t *testing.T) {
	var buf bytes.Buffer
	if err := runHelp([]string{"chat"}, &buf); err != nil {
		t.Fatalf("runHelp(chat): %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Usage: lucinate chat",
		"-connection",
		"-agent",
		"-session",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("chat help missing %q\n--- output ---\n%s", want, out)
		}
	}
	if strings.Contains(out, "-detach") {
		t.Errorf("chat help should not list --detach (it's send-only)\n%s", out)
	}
}

func TestRunHelpUnknownCommand(t *testing.T) {
	var buf bytes.Buffer
	err := runHelp([]string{"bogus"}, &buf)
	if err == nil {
		t.Fatal("expected error for unknown command, got nil")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should name the unknown command: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output on unknown command, got %q", buf.String())
	}
}
