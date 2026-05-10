package routines

import "strings"

// IsValidKebab reports whether name is a kebab-case routine identifier:
// non-empty, lowercase ASCII letters / digits / hyphens, no leading or
// trailing hyphen, no consecutive hyphens. Tightening the form-side
// rule keeps the on-disk routine directory readable and predictable
// for users typing /routine <name> in chat.
//
// Existing non-kebab directories still load through Load/Delete (which
// only enforce path safety via validName) so nothing on disk breaks;
// Save rejects them, so any edit will force a rename to a kebab name.
func IsValidKebab(name string) bool {
	if name == "" {
		return false
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	prevHyphen := false
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z':
			prevHyphen = false
		case c >= '0' && c <= '9':
			prevHyphen = false
		case c == '-':
			if prevHyphen {
				return false
			}
			prevHyphen = true
		default:
			return false
		}
	}
	return true
}

// ToKebab returns a best-effort kebab-case version of s: lowercased,
// non-alphanumeric runs collapsed to a single hyphen, leading/trailing
// hyphens trimmed. Useful for turning a user's typo ("My Routine!")
// into a suggested fix ("my-routine") in form error messages.
//
// Returns "" when no usable characters remain (e.g. all punctuation).
func ToKebab(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevHyphen := true // drop leading hyphens
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			prevHyphen = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			prevHyphen = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if prevHyphen {
				continue
			}
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}
