package tui

import (
	"testing"

	"github.com/a3tai/openclaw-go/protocol"
)

// TestQualifiedModelRef covers the provider-prefix normalisation the
// model picker and /model command apply before sessions.patch. The
// gateway reports a provider-local id + separate provider from
// models.list, but sessions.patch validates against the fully-qualified
// "<provider>/<id>" reference — so the bare id must be joined with its
// provider or the switch is rejected with "model not allowed".
func TestQualifiedModelRef(t *testing.T) {
	tests := []struct {
		name string
		in   protocol.ModelChoice
		want string
	}{
		{
			name: "openrouter provider-local slug gets prefixed",
			in:   protocol.ModelChoice{ID: "deepseek/deepseek-v4-pro", Provider: "openrouter"},
			want: "openrouter/deepseek/deepseek-v4-pro",
		},
		{
			name: "simple provider and id",
			in:   protocol.ModelChoice{ID: "claude-sonnet-4", Provider: "anthropic"},
			want: "anthropic/claude-sonnet-4",
		},
		{
			name: "empty provider keeps bare id (openai/hermes)",
			in:   protocol.ModelChoice{ID: "gpt-4o", Provider: ""},
			want: "gpt-4o",
		},
		{
			name: "already-qualified id is not double-prefixed",
			in:   protocol.ModelChoice{ID: "openrouter/deepseek/deepseek-v4-pro", Provider: "openrouter"},
			want: "openrouter/deepseek/deepseek-v4-pro",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := qualifiedModelRef(tt.in); got != tt.want {
				t.Errorf("qualifiedModelRef(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
