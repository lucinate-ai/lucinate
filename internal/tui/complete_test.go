package tui

import "testing"

func TestCompleteSlashCommand(t *testing.T) {
	tests := []struct {
		prefix string
		want   string
	}{
		{"/h", "/help"},
		{"/he", "/help"},
		{"/help", "/help"},
		{"/q", "/quit"},
		{"/qu", "/quit"},
		{"/b", "/back"},
		{"/c", "/clear"},
		{"/e", "/exit"},
		{"/z", ""},
		{"/", "/back"}, // first alphabetically
		{"/H", "/help"}, // case-insensitive
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			got := completeSlashCommand(tt.prefix)
			if got != tt.want {
				t.Errorf("completeSlashCommand(%q) = %q, want %q", tt.prefix, got, tt.want)
			}
		})
	}
}

func TestSlashCommandHint(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/h", "elp"},
		{"/he", "lp"},
		{"/help", ""},    // exact match, no hint
		{"/q", "uit"},
		{"/z", ""},       // no match
		{"hello", ""},    // not a slash command
		{"/help foo", ""}, // has space, not completing
		{"", ""},          // empty
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := slashCommandHint(tt.input)
			if got != tt.want {
				t.Errorf("slashCommandHint(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
