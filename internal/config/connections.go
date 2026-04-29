package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ConnectionType identifies the protocol/backend a Connection points at.
// New backend types extend this enum and are surfaced as choices on
// the connections form.
type ConnectionType string

const (
	ConnTypeOpenClaw ConnectionType = "openclaw"
	ConnTypeOpenAI   ConnectionType = "openai"
)

// AllConnectionTypes lists every type the picker UI knows about, in
// the order it should render them.
var AllConnectionTypes = []ConnectionType{ConnTypeOpenClaw, ConnTypeOpenAI}

// Label returns the user-facing label for the connection type.
func (t ConnectionType) Label() string {
	switch t {
	case ConnTypeOpenClaw:
		return "OpenClaw"
	case ConnTypeOpenAI:
		return "OpenAI-compatible"
	}
	return string(t)
}

// Connection is a saved target the user can connect to. The triple
// (Type, URL) is treated as the natural key when matching against the
// OPENCLAW_GATEWAY_URL env var or auto-detecting an existing entry.
type Connection struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Type      ConnectionType `json:"type"`
	URL       string         `json:"url"`
	CreatedAt time.Time      `json:"createdAt"`
	LastUsed  time.Time      `json:"lastUsed,omitempty"`

	// DefaultModel is the model new agents on this connection use
	// when the create-agent form does not specify one. OpenAI-compat
	// only.
	DefaultModel string `json:"defaultModel,omitempty"`
}

// Connections is the on-disk persistence shape for the connections
// store. DefaultID points at the connection that should be auto-picked
// at startup ("last used = default" — every successful connect updates
// it).
type Connections struct {
	DefaultID   string       `json:"defaultId,omitempty"`
	Connections []Connection `json:"connections"`
}

// ConnectionsPath returns the path to the connections file, creating
// the parent directory if necessary. The file lives alongside
// preferences under the lucinate data dir (LUCINATE_DATA_DIR or
// ~/.lucinate).
func ConnectionsPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "connections.json"), nil
}

// LoadConnections reads the connections store from disk. A missing or
// unreadable file yields an empty store rather than an error so first
// runs and corrupted state both fall through to the picker.
func LoadConnections() Connections {
	path, err := ConnectionsPath()
	if err != nil {
		return Connections{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Connections{}
	}
	var c Connections
	if err := json.Unmarshal(data, &c); err != nil {
		return Connections{}
	}
	return c
}

// SaveConnections writes the connections store to disk atomically via
// a tempfile + rename so a crashed write doesn't leave a half-written
// JSON document that LoadConnections would silently throw away.
func SaveConnections(c Connections) error {
	path, err := ConnectionsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".connections-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// Find returns the connection with the given ID, or nil if none.
func (c *Connections) Find(id string) *Connection {
	for i := range c.Connections {
		if c.Connections[i].ID == id {
			return &c.Connections[i]
		}
	}
	return nil
}

// FindByURL returns the connection matching the given (type, url)
// pair, or nil if none. URL comparison is normalised so trailing
// slashes and case differences in the host don't cause spurious misses
// when matching against the env var.
func (c *Connections) FindByURL(t ConnectionType, rawURL string) *Connection {
	target := normaliseURL(rawURL)
	for i := range c.Connections {
		if c.Connections[i].Type != t {
			continue
		}
		if normaliseURL(c.Connections[i].URL) == target {
			return &c.Connections[i]
		}
	}
	return nil
}

// ConnectionFields holds the user-supplied fields of a connection at
// add or edit time. The form populates the subset relevant to the
// chosen Type.
type ConnectionFields struct {
	Name         string
	Type         ConnectionType
	URL          string
	DefaultModel string // OpenAI-compat only
}

// Add appends a new connection, generating an ID and CreatedAt
// timestamp. URL validation works for any HTTP(S)/WS(S) URL — the
// picker UI is the source of type-specific validation rules.
func (c *Connections) Add(fields ConnectionFields) (*Connection, error) {
	if fields.Name == "" {
		return nil, fmt.Errorf("connection name is required")
	}
	if fields.URL == "" {
		return nil, fmt.Errorf("connection URL is required")
	}
	if _, err := deriveWSURL(fields.URL); err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	id, err := newConnectionID()
	if err != nil {
		return nil, err
	}
	conn := Connection{
		ID:           id,
		Name:         fields.Name,
		Type:         fields.Type,
		URL:          fields.URL,
		DefaultModel: fields.DefaultModel,
		CreatedAt:    time.Now().UTC(),
	}
	c.Connections = append(c.Connections, conn)
	return &c.Connections[len(c.Connections)-1], nil
}

// Update replaces the editable fields of an existing connection.
// Type is intentionally immutable — switching backend types changes
// the auth shape and identity directory, which is closer to deleting
// and re-adding than to an edit.
func (c *Connections) Update(id string, fields ConnectionFields) error {
	conn := c.Find(id)
	if conn == nil {
		return fmt.Errorf("connection not found: %s", id)
	}
	if fields.Name == "" {
		return fmt.Errorf("connection name is required")
	}
	if fields.URL == "" {
		return fmt.Errorf("connection URL is required")
	}
	if _, err := deriveWSURL(fields.URL); err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	conn.Name = fields.Name
	conn.URL = fields.URL
	conn.DefaultModel = fields.DefaultModel
	return nil
}

// Delete removes the connection with the given ID. If it was the
// default, DefaultID is cleared so startup falls back to the picker
// (or the single-connection auto-pick if exactly one remains).
func (c *Connections) Delete(id string) error {
	idx := -1
	for i, conn := range c.Connections {
		if conn.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("connection not found: %s", id)
	}
	c.Connections = append(c.Connections[:idx], c.Connections[idx+1:]...)
	if c.DefaultID == id {
		c.DefaultID = ""
	}
	return nil
}

// MarkUsed records that a connection was successfully chosen and
// promotes it to the default ("last used = default"). Callers persist
// the store afterwards.
func (c *Connections) MarkUsed(id string) {
	conn := c.Find(id)
	if conn == nil {
		return
	}
	conn.LastUsed = time.Now().UTC()
	c.DefaultID = id
}

// AutoNameForURL produces a friendly default name from a URL — the
// host (sans port) — so users importing an env var don't have to type
// a name to get a reasonable picker label. The picker's edit form
// lets them rename later.
func AutoNameForURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	host := u.Hostname()
	if host == "" {
		return rawURL
	}
	return host
}

// normaliseURL produces a comparable form of a URL: lower-case scheme
// and host, no trailing slash on the path. URLs that fail to parse
// are returned trimmed so they still compare equal to themselves.
func normaliseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}

func newConnectionID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
