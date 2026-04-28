package config

import (
	"os"

	"github.com/joho/godotenv"
)

// EntryConnection is the result of resolving how lucinate should
// start. Exactly one of Connection or ShowPicker is meaningful:
//
//   - Connection != nil: connect to it directly, mirroring the legacy
//     "OPENCLAW_GATEWAY_URL is set, just go" behaviour. The Store
//     reflects any auto-add the resolver performed (env var pointing
//     at a URL not in the store), which the caller is expected to
//     persist after a successful connect.
//   - ShowPicker == true: there's no obvious connection to use
//     (first-run, store cleared, ambiguous multi-entry without a
//     default). The caller should drop the user into the connections
//     picker view.
type EntryConnection struct {
	Store      Connections
	Connection *Connection
	ShowPicker bool
}

// ResolveEntryConnection runs the startup decision tree:
//
//  1. Load the connections store (creates empty on first run).
//  2. If OPENCLAW_GATEWAY_URL is set, find or auto-add a matching
//     OpenClaw connection and use it.
//  3. Else if a saved DefaultID resolves to an existing entry, use it.
//  4. Else if exactly one connection is stored, use it.
//  5. Else show the picker.
//
// Auto-add (step 2) mutates the in-memory store but does not persist
// it — the caller persists after a successful connect so failed env
// var typos don't accumulate ghost entries.
func ResolveEntryConnection() EntryConnection {
	_ = godotenv.Load()

	store := LoadConnections()

	if envURL := os.Getenv("OPENCLAW_GATEWAY_URL"); envURL != "" {
		if conn := store.FindByURL(ConnTypeOpenClaw, envURL); conn != nil {
			return EntryConnection{Store: store, Connection: conn}
		}
		conn, err := store.Add(AutoNameForURL(envURL), ConnTypeOpenClaw, envURL)
		if err == nil {
			return EntryConnection{Store: store, Connection: conn}
		}
		// Invalid env URL falls through to whatever the store offers.
	}

	if store.DefaultID != "" {
		if conn := store.Find(store.DefaultID); conn != nil {
			return EntryConnection{Store: store, Connection: conn}
		}
	}

	if len(store.Connections) == 1 {
		return EntryConnection{Store: store, Connection: &store.Connections[0]}
	}

	return EntryConnection{Store: store, ShowPicker: true}
}
