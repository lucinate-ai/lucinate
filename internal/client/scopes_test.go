package client

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/a3tai/openclaw-go/protocol"
)

func TestOperatorScopes(t *testing.T) {
	t.Run("defaults to full set including admin when unset", func(t *testing.T) {
		t.Setenv("OPENCLAW_OPERATOR_SCOPES", "")
		got := operatorScopes()
		if !containsScope(got, protocol.ScopeOperatorAdmin) {
			t.Fatalf("default scopes %v should include operator.admin", got)
		}
	})

	t.Run("honours the env override and can omit admin", func(t *testing.T) {
		t.Setenv("OPENCLAW_OPERATOR_SCOPES", " operator.read , operator.write ,operator.approvals ")
		got := operatorScopes()
		want := []protocol.Scope{
			protocol.ScopeOperatorRead,
			protocol.ScopeOperatorWrite,
			protocol.ScopeOperatorApprovals,
		}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
		if containsScope(got, protocol.ScopeOperatorAdmin) {
			t.Fatalf("override %v should not include operator.admin", got)
		}
	})

	t.Run("falls back to the default when the override is all separators", func(t *testing.T) {
		t.Setenv("OPENCLAW_OPERATOR_SCOPES", " , , ")
		if got := operatorScopes(); !containsScope(got, protocol.ScopeOperatorAdmin) {
			t.Fatalf("blank override should fall back to the default set, got %v", got)
		}
	})
}

func TestAdminScopeHint(t *testing.T) {
	t.Run("nil passes through", func(t *testing.T) {
		if adminScopeHint(nil) != nil {
			t.Fatal("nil error should stay nil")
		}
	})

	t.Run("unrelated error is untouched", func(t *testing.T) {
		in := errors.New("agents create: boom")
		if got := adminScopeHint(in); got.Error() != in.Error() {
			t.Fatalf("unrelated error was modified: %q", got.Error())
		}
	})

	t.Run("missing-admin error is annotated and still unwraps", func(t *testing.T) {
		base := fmt.Errorf("agents create: agents.create: INVALID_REQUEST: missing scope: %s", protocol.ScopeOperatorAdmin)
		got := adminScopeHint(base)
		if !strings.Contains(got.Error(), "OPENCLAW_OPERATOR_SCOPES") {
			t.Fatalf("annotated error should mention OPENCLAW_OPERATOR_SCOPES: %q", got.Error())
		}
		if !errors.Is(got, base) {
			t.Fatal("annotated error should still unwrap to the original")
		}
	})
}

func containsScope(scopes []protocol.Scope, want protocol.Scope) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}
