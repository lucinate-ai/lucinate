package routines

import "testing"

func TestIsValidKebab(t *testing.T) {
	good := []string{
		"a",
		"foo",
		"foo-bar",
		"deploy-staging-2",
		"x-y-z",
		"step1",
		"123-abc",
	}
	bad := map[string]string{
		"":            "empty",
		"-foo":        "leading hyphen",
		"foo-":        "trailing hyphen",
		"foo--bar":    "consecutive hyphens",
		"Foo":         "uppercase",
		"foo_bar":     "underscore",
		"foo bar":     "space",
		"foo.bar":     "dot",
		"foo/bar":     "slash",
		"FOO-BAR":     "uppercase",
		"-":           "single hyphen",
		"--":          "double hyphen",
		"foo.bar.baz": "dots",
	}
	for _, in := range good {
		if !IsValidKebab(in) {
			t.Errorf("IsValidKebab(%q) = false, want true", in)
		}
	}
	for in, why := range bad {
		if IsValidKebab(in) {
			t.Errorf("IsValidKebab(%q) = true, want false (%s)", in, why)
		}
	}
}

func TestToKebab(t *testing.T) {
	cases := map[string]string{
		"":              "",
		"foo":           "foo",
		"Foo":           "foo",
		"FOO BAR":       "foo-bar",
		"My Routine!":   "my-routine",
		"  spaced  ":    "spaced",
		"foo_bar":       "foo-bar",
		"foo--bar":      "foo-bar",
		"--foo--":       "foo",
		"foo.bar.baz":   "foo-bar-baz",
		"!!!":           "",
		"deploy/stage1": "deploy-stage1",
		"a b c d":       "a-b-c-d",
	}
	for in, want := range cases {
		if got := ToKebab(in); got != want {
			t.Errorf("ToKebab(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToKebab_RoundTripsThroughIsValidKebab(t *testing.T) {
	inputs := []string{"My Routine", "FOO BAR", "deploy/stage1", "a b c"}
	for _, in := range inputs {
		got := ToKebab(in)
		if got == "" {
			continue
		}
		if !IsValidKebab(got) {
			t.Errorf("ToKebab(%q) = %q which fails IsValidKebab", in, got)
		}
	}
}
