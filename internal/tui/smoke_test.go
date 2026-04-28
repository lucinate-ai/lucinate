package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// TestStartupSmoke_NoPanicOnInitialWindowSize is the hermetic
// equivalent of running `make run` on a developer terminal: it
// constructs AppModel in every entry-view variant the resolver can
// produce and feeds it an initial WindowSizeMsg, the message that
// bubbletea always sends before the first render. Any setSize call
// that touches an uninitialised bubbles widget will panic from
// updatePagination → divide-by-zero or nil-pointer deref, which is
// exactly the regression we want CI to catch before a release ships.
//
// CI runners don't have a TTY so the real bubbletea program can't
// drive itself end-to-end on Linux; this test exercises the same
// AppModel.update code path the program would hit on the host.
func TestStartupSmoke_NoPanicOnInitialWindowSize(t *testing.T) {
	cases := []struct {
		name  string
		setup func() AppModel
	}{
		{
			name: "managed mode, no connections — picker entry",
			setup: func() AppModel {
				return NewApp(nil, AppOptions{
					Store: &config.Connections{},
					ClientFactory: func(*config.Connection) (*client.Client, error) {
						return nil, nil
					},
				})
			},
		},
		{
			name: "managed mode, initial connection — connecting entry",
			setup: func() AppModel {
				store := &config.Connections{}
				conn, err := store.Add("home", config.ConnTypeOpenClaw, "https://home.example.com")
				if err != nil {
					t.Fatalf("seed: %v", err)
				}
				return NewApp(nil, AppOptions{
					Store:   store,
					Initial: conn,
					ClientFactory: func(*config.Connection) (*client.Client, error) {
						return nil, nil
					},
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on initial WindowSizeMsg: %v", r)
				}
			}()
			m := tc.setup()
			// Bubble Tea sends WindowSizeMsg before the first render
			// every time. 80x24 is the standard CI default.
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			if _, ok := updated.(AppModel); !ok {
				t.Fatalf("Update returned unexpected model type %T", updated)
			}
		})
	}
}
