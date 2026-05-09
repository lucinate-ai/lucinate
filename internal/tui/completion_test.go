package tui

import (
	"reflect"
	"testing"
)

func TestLongestCommonPrefix(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"empty", nil, ""},
		{"single", []string{"/help"}, "/help"},
		{"all same", []string{"/help", "/help"}, "/help"},
		{"two diverge late", []string{"/example", "/exams"}, "/exam"},
		{"three converge at slash", []string{"/agents", "/agent", "/clear"}, "/"},
		{"first is prefix of second", []string{"/agent", "/agents"}, "/agent"},
		{"empty string in slice", []string{"/help", ""}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := longestCommonPrefix(tt.in)
			if got != tt.want {
				t.Errorf("longestCommonPrefix(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestMatchingSlashCommands_BuiltinsAndSkills(t *testing.T) {
	m := newSlashTestModel()
	m.skills = []agentSkill{
		{Name: "example", Description: "x"},
		{Name: "exams", Description: "y"},
	}

	// Slash-only prefix returns the full built-in list (in curated order)
	// followed by skills.
	got := m.matchingSlashCommands("/")
	if len(got) != len(slashCommands)+2 {
		t.Fatalf("expected %d candidates, got %d: %v", len(slashCommands)+2, len(got), got)
	}
	for i, cmd := range slashCommands {
		if got[i] != cmd {
			t.Errorf("position %d: got %q, want %q (curated order broken)", i, got[i], cmd)
		}
	}
	if got[len(slashCommands)] != "/example" || got[len(slashCommands)+1] != "/exams" {
		t.Errorf("expected skills appended in skill order, got tail: %v", got[len(slashCommands):])
	}

	// Prefix that matches only skills.
	if g := m.matchingSlashCommands("/exa"); !reflect.DeepEqual(g, []string{"/example", "/exams"}) {
		t.Errorf("/exa: got %v, want [/example /exams]", g)
	}

	// Prefix that matches both a built-in and ordering tiebreak.
	if g := m.matchingSlashCommands("/age"); !reflect.DeepEqual(g, []string{"/agents", "/agent"}) {
		t.Errorf("/age: got %v, want [/agents /agent]", g)
	}

	// Case-insensitive match on the typed prefix.
	if g := m.matchingSlashCommands("/Q"); !reflect.DeepEqual(g, []string{"/quit"}) {
		t.Errorf("/Q: got %v, want [/quit]", g)
	}

	// Non-slash → nil.
	if g := m.matchingSlashCommands("hello"); g != nil {
		t.Errorf("hello: got %v, want nil", g)
	}
}

func TestMatchingSlashCommands_SkillNameClashIsDeduped(t *testing.T) {
	m := newSlashTestModel()
	// A skill with the same name as a built-in must not duplicate the entry.
	m.skills = []agentSkill{{Name: "help", Description: "duplicate"}}
	got := m.matchingSlashCommands("/h")
	count := 0
	for _, c := range got {
		if c == "/help" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected /help once, got %d in %v", count, got)
	}
}
