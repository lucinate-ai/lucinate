package cli

import (
	"flag"
	"strings"
	"testing"
	"time"
)

// Go's flag package stops parsing at the first non-flag argument, so
// `rooms log <room> --follow` would silently drop --follow and read as a
// broken flag. Every rooms subcommand takes its positional first and its
// flags after, so the args are reordered before parsing.
func TestReorderFlagsFirst(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantFollow bool
		wantConn   string
		wantTime   time.Duration
		wantPos    []string
	}{
		{
			name:       "trailing bool and value flags",
			args:       []string{"sztab-orca", "--follow", "--timeout", "45s"},
			wantFollow: true,
			wantTime:   45 * time.Second,
			wantPos:    []string{"sztab-orca"},
		},
		{
			name:     "trailing flags with = form",
			args:     []string{"sztab-orca", "--connection=prod", "--timeout=2m"},
			wantConn: "prod",
			wantTime: 2 * time.Minute,
			wantPos:  []string{"sztab-orca"},
		},
		{
			name:       "leading flags still work",
			args:       []string{"--follow", "sztab-orca"},
			wantFollow: true,
			wantPos:    []string{"sztab-orca"},
		},
		{
			name:       "value that looks positional stays with its flag",
			args:       []string{"sztab-orca", "-c", "prod", "--follow"},
			wantFollow: true,
			wantConn:   "prod",
			wantPos:    []string{"sztab-orca"},
		},
		{
			name:    "double dash protects flag-like positionals",
			args:    []string{"sztab-orca", "--", "--not-a-flag", "and more"},
			wantPos: []string{"sztab-orca", "--not-a-flag", "and more"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var follow bool
			var connection string
			var timeout time.Duration
			fs := flag.NewFlagSet("rooms log", flag.ContinueOnError)
			fs.BoolVar(&follow, "follow", false, "")
			fs.StringVar(&connection, "connection", "", "")
			fs.StringVar(&connection, "c", "", "")
			fs.DurationVar(&timeout, "timeout", 0, "")

			if err := fs.Parse(reorderFlagsFirst(fs, tc.args)); err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if follow != tc.wantFollow {
				t.Errorf("follow = %t, want %t", follow, tc.wantFollow)
			}
			if connection != tc.wantConn {
				t.Errorf("connection = %q, want %q", connection, tc.wantConn)
			}
			if timeout != tc.wantTime {
				t.Errorf("timeout = %v, want %v", timeout, tc.wantTime)
			}
			if got := fs.Args(); strings.Join(got, "|") != strings.Join(tc.wantPos, "|") {
				t.Errorf("positionals = %v, want %v", got, tc.wantPos)
			}
		})
	}
}
