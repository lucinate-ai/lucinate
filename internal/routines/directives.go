package routines

import (
	"regexp"
	"strings"
)

// directivePattern matches a /routine: instruction occupying its own line.
// Leading whitespace is allowed; trailing whitespace is allowed. The body
// must be one of the supported keywords.
var directivePattern = regexp.MustCompile(`^\s*/routine:(stop|pause|continue|mode\s+(auto|manual))\s*$`)

// ScanDirectives returns every /routine: directive found in reply, in order
// of appearance. Each directive must occupy its own line — inline mentions
// like "send /routine:stop to halt" are ignored deliberately.
func ScanDirectives(reply string) []Directive {
	var out []Directive
	for _, raw := range strings.Split(reply, "\n") {
		line := strings.TrimRight(raw, "\r")
		m := directivePattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		switch {
		case strings.HasPrefix(strings.TrimSpace(m[1]), "stop"):
			out = append(out, Directive{Kind: DirectiveStop})
		case strings.HasPrefix(strings.TrimSpace(m[1]), "pause"):
			out = append(out, Directive{Kind: DirectivePause})
		case strings.HasPrefix(strings.TrimSpace(m[1]), "continue"):
			out = append(out, Directive{Kind: DirectiveContinue})
		case m[2] == "auto":
			out = append(out, Directive{Kind: DirectiveModeAuto})
		case m[2] == "manual":
			out = append(out, Directive{Kind: DirectiveModeManual})
		}
	}
	return out
}
