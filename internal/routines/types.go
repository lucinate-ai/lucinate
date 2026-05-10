// Package routines implements multi-step prompt routines: ordered lists of
// user-facing prompts read from disk, applied via /routine <name> in the chat
// view, and optionally auto-advanced after each assistant reply.
package routines

// Mode is the activation mode for an active routine.
type Mode string

const (
	ModeManual Mode = "manual"
	ModeAuto   Mode = "auto"
)

// Frontmatter is the optional YAML header at the top of a STEPS.md file.
// Every field is optional; absent fields fall back to documented defaults.
type Frontmatter struct {
	Name string `yaml:"name"`
	Mode string `yaml:"mode"`
	Log  string `yaml:"log"`
}

// Routine is a parsed STEPS.md file ready for activation.
type Routine struct {
	// Name is the routine's directory name (the on-disk identity).
	// Frontmatter Name is informational and does not override this.
	Name string

	// Frontmatter holds the verbatim metadata block from the file.
	Frontmatter Frontmatter

	// Steps are the ordered step bodies. Each is the user-message text
	// to send when that step fires, with surrounding whitespace trimmed.
	Steps []string

	// Path is the absolute path to the routine's STEPS.md file.
	Path string
}

// ResolvedMode returns the routine's mode with the documented default
// (manual) applied when frontmatter omits the field.
func (r Routine) ResolvedMode() Mode {
	switch Mode(r.Frontmatter.Mode) {
	case ModeAuto:
		return ModeAuto
	default:
		return ModeManual
	}
}

// DirectiveKind classifies a /routine: directive parsed from an
// assistant reply.
type DirectiveKind int

const (
	DirectiveStop DirectiveKind = iota
	DirectivePause
	DirectiveModeAuto
	DirectiveModeManual
	// DirectiveContinue is a no-op the assistant can emit to make its
	// intent explicit ("keep going") in conditional steps. The routine
	// controller treats it as a recognised pass-through so unknown
	// /routine: tokens don't silently advance under it.
	DirectiveContinue
)

// Directive is a single parsed /routine: instruction.
type Directive struct {
	Kind DirectiveKind
}
