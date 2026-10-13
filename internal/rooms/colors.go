// Package rooms ... (see rooms.go)
//
// Colors gives every roster member a stable, readable on-dark accent so
// a multi-member transcript stays scannable without per-message noise.
package rooms

import (
	"fmt"
	"strings"
)

// MemberPalette is the closed set of on-dark accents every member picks
// from. Eight entries is enough for the MaxMembers roster while staying
// distinguishable for the common 2–4 case; pickers must not add to it.
var MemberPalette = []string{
	"#60A5FA", // blue
	"#34D399", // green
	"#FBBF24", // amber
	"#F472B6", // pink
	"#A78BFA", // purple
	"#22D3EE", // cyan
	"#FB923C", // orange
	"#E879F9", // fuchsia
}

// ValidHexColor reports whether s is a #RRGGBB or #RGB colour, case-
// insensitive. Anything else (missing #, wrong length, non-hex digits)
// is invalid.
func ValidHexColor(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 4 && len(s) != 7 {
		return false
	}
	if s[0] != '#' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// NormalizeHex expands a #RGB triplet to #RRGGBB and uppercases it.
// Invalid input returns a descriptive error — never a guess.
func NormalizeHex(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !ValidHexColor(s) {
		return "", fmt.Errorf("not a hex colour: %q", s)
	}
	if len(s) == 4 {
		// #RGB -> #RRGGBB
		return fmt.Sprintf("#%c%c%c%c%c%c",
			strings.ToUpper(string(s[1]))[0],
			strings.ToUpper(string(s[1]))[0],
			strings.ToUpper(string(s[2]))[0],
			strings.ToUpper(string(s[2]))[0],
			strings.ToUpper(string(s[3]))[0],
			strings.ToUpper(string(s[3]))[0],
		), nil
	}
	return strings.ToUpper(s), nil
}

// colorHash is a stable, non-cryptographic hash used to pick a palette
// index for a handle that has no override. FNV-1a keeps the same handle
// on the same palette index across runs; map iteration is never used so
// the result is deterministic regardless of Go's map shard order.
func colorHash(s string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// ColorFor returns the on-dark hex colour to render a member under.
// overrides win when the key (case-insensitive) maps to a valid hex
// colour; invalid override values are ignored (the lookup falls through
// to the palette). Members with an unknown or empty handle always land
// on MemberPalette[0].
func ColorFor(handle string, overrides map[string]string) string {
	if handle == "" {
		return MemberPalette[0]
	}
	// Case-insensitive override lookup.
	for k, v := range overrides {
		if strings.EqualFold(k, handle) && ValidHexColor(v) {
			c, err := NormalizeHex(v)
			if err == nil {
				return c
			}
		}
	}
	// Deterministic palette pick.
	idx := colorHash(handle) % uint64(len(MemberPalette))
	return MemberPalette[idx]
}
