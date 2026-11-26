package rooms

import (
	"testing"
)

func TestValidHexColor(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"valid #RRGGBB upper", "#AABBCC", true},
		{"valid #RRGGBB lower", "#aabbcc", true},
		{"valid #RGB upper", "#ABC", true},
		{"valid #RGB lower", "#abc", true},
		{"valid mixed case", "#AaBbCc", true},
		{"missing hash", "AABBCC", false},
		{"too short", "#AB", false},
		{"too long", "#AABBCCDD", false},
		{"invalid hex digit", "#GGGGGG", false},
		{"empty", "", false},
		{"only hash", "#", false},
		{"trims spaces", "  #ABC  ", true},
		{"mixed valid", "#123456", true},
		{"zero colour", "#000000", true},
		{"white", "#FFFFFF", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidHexColor(tt.in); got != tt.want {
				t.Errorf("ValidHexColor(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeHex(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		err  bool
	}{
		{"expand #RGB", "#ABC", "#AABBCC", false},
		{"uppercase #RRGGBB", "#aabbcc", "#AABBCC", false},
		{"already uppercase", "#AABBCC", "#AABBCC", false},
		{"mixed case", "#AaBbCc", "#AABBCC", false},
		{"expand lower #rgb", "#abc", "#AABBCC", false},
		{"invalid too short", "#AB", "", true},
		{"invalid too long", "#AABBCCDD", "", true},
		{"invalid chars", "#GGGGGG", "", true},
		{"missing hash", "AABBCC", "", true},
		{"empty", "", "", true},
		{"only hash", "#", "", true},
		{"spaces trimmed", "  #ABC  ", "#AABBCC", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeHex(tt.in)
			if tt.err {
				if err == nil {
					t.Errorf("NormalizeHex(%q) expected error, got %q", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Errorf("NormalizeHex(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeHex(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestColorFor(t *testing.T) {
	t.Run("empty handle returns first palette entry", func(t *testing.T) {
		got := ColorFor("", nil)
		if got != MemberPalette[0] {
			t.Errorf("ColorFor(empty, nil) = %q, want %q", got, MemberPalette[0])
		}
	})

	t.Run("override wins", func(t *testing.T) {
		overrides := map[string]string{"mary": "#FF0000"}
		got := ColorFor("mary", overrides)
		if got != "#FF0000" {
			t.Errorf("ColorFor with valid override = %q, want %q", got, "#FF0000")
		}
	})

	t.Run("override is case-insensitive", func(t *testing.T) {
		for _, key := range []string{"MARY", "Mary", "mArY", "mary"} {
			overrides := map[string]string{key: "#00FF00"}
			got := ColorFor("mary", overrides)
			if got != "#00FF00" {
				t.Errorf("ColorFor with key %q (case-insensitive) = %q, want %q", key, got, "#00FF00")
			}
		}
	})

	t.Run("invalid override value is ignored", func(t *testing.T) {
		overrides := map[string]string{"mary": "not-a-color"}
		got := ColorFor("mary", overrides)
		if !validPaletteEntry(got) {
			t.Errorf("ColorFor with invalid override returned %q, not a palette colour", got)
		}
	})

	t.Run("unknown handle falls to palette via deterministic hash", func(t *testing.T) {
		got := ColorFor("alice", nil)
		if !validPaletteEntry(got) {
			t.Errorf("ColorFor unknown handle returned %q, not a palette colour", got)
		}
	})

	t.Run("same handle always returns same colour", func(t *testing.T) {
		handle := "deterministic-test"
		c1 := ColorFor(handle, nil)
		c2 := ColorFor(handle, nil)
		if c1 != c2 {
			t.Errorf("ColorFor not deterministic: %q vs %q", c1, c2)
		}
		if !validPaletteEntry(c1) {
			t.Errorf("ColorFor returned non-palette colour %q", c1)
		}
	})

	t.Run("six typical profiles yield at least four distinct colours", func(t *testing.T) {
		profiles := []string{"matt", "mary", "kowal", "ala", "john", "bot"}
		seen := map[string]struct{}{}
		for _, p := range profiles {
			c := ColorFor(p, nil)
			if !validPaletteEntry(c) {
				t.Errorf("ColorFor(%q) = %q not a palette colour", p, c)
			}
			seen[c] = struct{}{}
		}
		if l := len(seen); l < 4 {
			t.Errorf("expected at least 4 distinct palette colours across 6 profiles, got %d", l)
		}
	})
}

// validPaletteEntry reports whether c is one of the closed palette entries,
// used by tests to detect accidental regressions outside MemberPalette.
func validPaletteEntry(c string) bool {
	for _, p := range MemberPalette {
		if c == p {
			return true
		}
	}
	return false
}
