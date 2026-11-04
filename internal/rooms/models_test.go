package rooms

import (
	"context"
	"strings"
	"testing"

	tg "github.com/lucinate-ai/lucinate/internal/testgateway"
)

// tgNew is the two-member room the /model tests start from.
func tgNew(t *testing.T) *tg.Server {
	t.Helper()
	return tg.New(t, tg.DefaultOptions())
}

// ── the /model command ───────────────────────────────────────────────────────

func TestParseModelCommand_FourShapes(t *testing.T) {
	roster := RosterFor([]string{"matt", "kowal"})

	t.Run("no arguments asks which member", func(t *testing.T) {
		cmd, err := ParseModelCommand(nil, roster)
		if err != nil {
			t.Fatalf("ParseModelCommand() error: %v", err)
		}
		if cmd.Member != nil || cmd.Model != "" {
			t.Fatalf("command = %+v, want an empty command meaning \"pick a member\"", cmd)
		}
	})

	t.Run("@handle picks the model for that member", func(t *testing.T) {
		cmd, err := ParseModelCommand([]string{"@matt"}, roster)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if cmd.Member == nil || cmd.Member.Profile != "matt" {
			t.Fatalf("member = %+v, want matt", cmd.Member)
		}
		if cmd.Model != "" {
			t.Errorf("model = %q, want empty so the picker opens", cmd.Model)
		}
	})

	t.Run("@handle name switches directly", func(t *testing.T) {
		cmd, err := ParseModelCommand([]string{"@kowal", "gpt-5.1"}, roster)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if cmd.Member == nil || cmd.Member.Profile != "kowal" || cmd.Model != "gpt-5.1" {
			t.Fatalf("command = %+v, want kowal on gpt-5.1", cmd)
		}
	})

	t.Run("a multi-word model name is joined", func(t *testing.T) {
		cmd, err := ParseModelCommand([]string{"@matt", "claude", "sonnet", "4"}, roster)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if cmd.Model != "claude sonnet 4" {
			t.Fatalf("model = %q, want the words joined", cmd.Model)
		}
	})

	t.Run("handle matching is case-insensitive", func(t *testing.T) {
		cmd, err := ParseModelCommand([]string{"@MATT", "x"}, roster)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if cmd.Member == nil || cmd.Member.Profile != "matt" {
			t.Fatalf("member = %+v, want matt", cmd.Member)
		}
	})
}

func TestParseModelCommand_Errors(t *testing.T) {
	roster := RosterFor([]string{"matt", "kowal"})

	t.Run("a bare model name has no member to apply it to", func(t *testing.T) {
		_, err := ParseModelCommand([]string{"gpt-5"}, roster)
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "@handle") {
			t.Errorf("error = %v, want it to ask for a handle", err)
		}
	})

	t.Run("unknown handle lists the roster", func(t *testing.T) {
		_, err := ParseModelCommand([]string{"@ghost", "gpt-5"}, roster)
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "@ghost") {
			t.Errorf("error = %v, want the unknown handle named", err)
		}
		if !strings.Contains(err.Error(), "@matt") || !strings.Contains(err.Error(), "@kowal") {
			t.Errorf("error = %v, want the roster listed so the user can retry", err)
		}
	})

	t.Run("a typo is an unknown handle, not a silent match", func(t *testing.T) {
		if _, err := ParseModelCommand([]string{"@mat", "gpt-5"}, roster); err == nil {
			t.Fatal("@mat must not resolve to matt")
		}
	})

	t.Run("an empty model name is refused", func(t *testing.T) {
		_, err := ParseModelCommand([]string{"@matt", "   "}, roster)
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("error = %v, want it to say the name is empty", err)
		}
	})
}

// ── roster model helpers ─────────────────────────────────────────────────────

func TestMemberModelAndLabel(t *testing.T) {
	plain := Member{MemberID: "m1", Profile: "matt", Handle: "matt"}
	if got := MemberModel(plain); got != "" {
		t.Errorf("MemberModel(no config) = %q, want empty", got)
	}
	if got := ModelLabel(plain); got != "profile default" {
		t.Errorf("ModelLabel(no config) = %q, want the profile default named", got)
	}

	set := WithMemberModel(plain, "gpt-5")
	if got := MemberModel(set); got != "gpt-5" {
		t.Errorf("MemberModel = %q, want gpt-5", got)
	}
	if got := ModelLabel(set); got != "gpt-5" {
		t.Errorf("ModelLabel = %q, want gpt-5", got)
	}
	// The helper copies: the row it came from must not change under the caller.
	if plain.ModelConfig != nil {
		t.Error("WithMemberModel mutated the original member")
	}
	if _, ok := set.ModelConfig["model"]; !ok {
		t.Error("the copy should carry the model key")
	}
}

func TestMemberModel_KeepsOtherConfigKeys(t *testing.T) {
	m := Member{Profile: "matt", Handle: "matt", ModelConfig: map[string]any{
		"provider": "openai", "reasoning": "high", "model": "old",
	}}
	next := WithMemberModel(m, "new")
	if next.ModelConfig["provider"] != "openai" || next.ModelConfig["reasoning"] != "high" {
		t.Fatalf("config = %+v, want the other keys preserved", next.ModelConfig)
	}
	if MemberModel(next) != "new" {
		t.Errorf("model = %q, want new", MemberModel(next))
	}
}

func TestFilterModels(t *testing.T) {
	options := []string{"gpt-5", "gpt-5-mini", "claude-sonnet-4", "llama-3.3-70b"}

	if got := FilterModels(options, ""); len(got) != len(options) {
		t.Errorf("empty filter returned %d, want all %d", len(got), len(options))
	}
	if got := FilterModels(options, "GPT-5"); len(got) != 2 {
		t.Errorf("filter GPT-5 returned %v, want the two gpt-5 models", got)
	}
	if got := FilterModels(options, "sonnet"); len(got) != 1 || got[0] != "claude-sonnet-4" {
		t.Errorf("filter sonnet = %v, want claude-sonnet-4", got)
	}
	// Subsequence, like the chat picker's fuzzy filter: letters in order.
	if got := FilterModels(options, "g5m"); len(got) != 1 || got[0] != "gpt-5-mini" {
		t.Errorf("filter g5m = %v, want gpt-5-mini", got)
	}
	if got := FilterModels(options, "zzz"); len(got) != 0 {
		t.Errorf("filter zzz = %v, want nothing", got)
	}
	if got := FilterModels(options, "  "); len(got) != len(options) {
		t.Errorf("whitespace-only filter returned %d, want all", len(got))
	}
}

// ── the client, against the fake gateway (MODEL_LIST / MODEL_REJECT) ─────────

func TestClient_ModelCatalogue(t *testing.T) {
	s := tgNew(t)
	s.SetModelCatalogue("gpt-5", "gpt-5", "gpt-5-mini", "claude-sonnet-4")
	c := dialGateway(t, s)

	got, err := c.ModelCatalogue(context.Background())
	if err != nil {
		t.Fatalf("ModelCatalogue: %v", err)
	}
	if got.Model != "gpt-5" || len(got.Options) != 3 {
		t.Fatalf("catalogue = %+v, want the fake gateway's list", got)
	}
}

func TestClient_SupportsMemberModelFollowsCapabilities(t *testing.T) {
	s := tgNew(t)
	c := dialGateway(t, s)

	supported, err := c.SupportsMemberModel(context.Background())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if supported {
		t.Error("a gateway that does not advertise groups.member_model must report false")
	}

	s.SetMemberModelSupported(true)
	if supported, err = c.SupportsMemberModel(context.Background()); err != nil || !supported {
		t.Fatalf("probe = %v, %v; want true once the method is advertised", supported, err)
	}
}

func TestClient_SetMemberModel(t *testing.T) {
	s := tgNew(t)
	s.SetMemberModelSupported(true)
	c := dialGateway(t, s)

	seat, err := c.SetMemberModel(context.Background(), "sztab", "@matt", "gpt-5-mini", "")
	if err != nil {
		t.Fatalf("SetMemberModel: %v", err)
	}
	if seat.Model != "gpt-5-mini" || seat.Handle != "matt" || seat.RoomID != "sztab" {
		t.Errorf("seat = %+v, want the model the gateway seated for that member", seat)
	}
	if seat.SessionID == "" {
		t.Error("seat carries no session id: the caller cannot tell where the pick landed")
	}
	if !seat.SessionCreated {
		t.Error("first seat for a handle reports session_created: the member had no room session yet")
	}
	if calls := s.MemberModelCalls(); len(calls) != 1 || calls[0] != "matt=gpt-5-mini" {
		t.Errorf("gateway recorded %v, want the switch to have reached the wire", calls)
	}
	// A second seat lands on the session that already exists.
	again, err := c.SetMemberModel(context.Background(), "sztab", "@matt", "gpt-5", "")
	if err != nil {
		t.Fatalf("second SetMemberModel: %v", err)
	}
	if again.SessionCreated || again.SessionID != seat.SessionID {
		t.Errorf("second seat = %+v, want the same session and session_created=false", again)
	}
}

func TestClient_SetMemberModelSendsProvider(t *testing.T) {
	s := tgNew(t)
	s.SetMemberModelSupported(true)
	c := dialGateway(t, s)

	seat, err := c.SetMemberModel(context.Background(), "sztab", "@matt", "gpt-5", "openai")
	if err != nil {
		t.Fatalf("SetMemberModel: %v", err)
	}
	if seat.Provider != "openai" {
		t.Errorf("seat.Provider = %q, want the provider echoed from the wire", seat.Provider)
	}
	if calls := s.MemberModelCalls(); len(calls) != 1 || calls[0] != "matt=gpt-5" {
		t.Errorf("gateway recorded %v, want the switch to have reached the wire", calls)
	}
}

func TestClient_SetMemberModelRejections(t *testing.T) {
	ctx := context.Background()

	t.Run("gateway without the method", func(t *testing.T) {
		s := tgNew(t)
		c := dialGateway(t, s)
		if _, err := c.SetMemberModel(ctx, "sztab", "@matt", "gpt-5", ""); err == nil {
			t.Fatal("expected the gateway to refuse an unknown method")
		}
		if calls := s.MemberModelCalls(); len(calls) != 0 {
			t.Errorf("gateway recorded %v, want nothing applied", calls)
		}
	})

	t.Run("gateway rejects the model (MODEL_REJECT)", func(t *testing.T) {
		s := tgNew(t)
		s.SetMemberModelSupported(true)
		s.SetMemberModelReject(4104, "model gpt-9 is not available for this member")
		c := dialGateway(t, s)
		_, err := c.SetMemberModel(ctx, "sztab", "@matt", "gpt-9", "")
		if err == nil {
			t.Fatal("expected the rejection to surface")
		}
		if !strings.Contains(err.Error(), "not available") {
			t.Errorf("error = %v, want the gateway's message", err)
		}
		row, _ := s.MemberRow("matt")
		if row.ModelConfig != nil {
			t.Errorf("row = %+v, want the model unchanged after a rejection", row.ModelConfig)
		}
	})

	t.Run("unknown member", func(t *testing.T) {
		s := tgNew(t)
		s.SetMemberModelSupported(true)
		c := dialGateway(t, s)
		if _, err := c.SetMemberModel(ctx, "sztab", "@ghost", "gpt-5", ""); err == nil {
			t.Fatal("expected an error for a member the room does not have")
		}
	})

	t.Run("local validation", func(t *testing.T) {
		s := tgNew(t)
		s.SetMemberModelSupported(true)
		c := dialGateway(t, s)
		for _, tc := range []struct{ room, handle, model string }{
			{room: "", handle: "@matt", model: "gpt-5"},
			{room: "sztab", handle: "", model: "gpt-5"},
			{room: "sztab", handle: "@matt", model: "   "},
		} {
			if _, err := c.SetMemberModel(ctx, tc.room, tc.handle, tc.model, ""); err == nil {
				t.Errorf("SetMemberModel(%q,%q,%q) should have failed locally", tc.room, tc.handle, tc.model)
			}
		}
		if calls := s.MemberModelCalls(); len(calls) != 0 {
			t.Errorf("invalid requests reached the wire: %v", calls)
		}
	})
}
