// Package openai implements the backend.Backend interface for
// OpenAI-compatible HTTP servers (Ollama, vLLM, LM Studio, llamafile,
// any /v1/chat/completions endpoint). Agents are stored locally — one
// directory per agent under ~/.lucinate/agents/<conn-id>/<agent-id>/
// — because OpenAI-compatible servers have no concept of agents
// themselves. /v1/models is exposed as the model picker; the agent's
// configured model is the default for new conversations.
//
// Each agent directory holds:
//
//	agent.json   — id, name, model, timestamps
//	IDENTITY.md  — who the agent is (name, role, persona)
//	SOUL.md      — how the agent behaves (tone, values, working style)
//	history.jsonl — append-only conversation transcript (one
//	                JSON-encoded message per line)
//
// IDENTITY.md and SOUL.md are concatenated at runtime to form the
// system prompt so users can edit them on disk between sessions
// without needing the TUI's editor.
package openai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// archiveDir is the subdirectory under each connection's agent root
// where archived agent dirs live. It's intentionally a hidden name so
// AgentStore.List skips it (List filters on parsable agent.json at
// the top level of each direct child; .archive itself has none).
const archiveDir = ".archive"

// AgentMeta is the shape persisted to agent.json. Identity / soul /
// history are stored alongside in their own files because they're
// either user-edited (markdown) or grow-only (transcript).
type AgentMeta struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Message is one transcript entry, persisted line-delimited in
// history.jsonl.
type Message struct {
	Role    string    `json:"role"`              // "user" | "assistant" | "system"
	Content string    `json:"content"`
	Time    time.Time `json:"time"`
	Model   string    `json:"model,omitempty"`   // model used to generate (assistant only)
}

// AgentStore manages a per-connection agents directory. The path is
// ~/.lucinate/agents/<connID>/. Construct one Store per active
// connection; the OpenAI backend keeps a Store reference for the
// duration of the session.
type AgentStore struct {
	root string
}

// NewAgentStore returns a Store rooted under
// <data-dir>/agents/<connID>/. The directory is created lazily on
// first write.
func NewAgentStore(connID string) (*AgentStore, error) {
	if connID == "" {
		return nil, fmt.Errorf("connection id is required")
	}
	dataDir, err := config.DataDir()
	if err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	root := filepath.Join(dataDir, "agents", connID)
	return &AgentStore{root: root}, nil
}

// Root returns the on-disk path so callers can show it to the user
// (e.g. "your IDENTITY.md lives at ...").
func (s *AgentStore) Root() string { return s.root }

// AgentDir returns the directory for a single agent.
func (s *AgentStore) AgentDir(agentID string) string {
	return filepath.Join(s.root, agentID)
}

// List returns all agents under root, sorted by UpdatedAt descending
// (most recently used first). Errors reading individual agents are
// skipped so a single corrupted directory doesn't hide the rest.
func (s *AgentStore) List() ([]AgentMeta, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []AgentMeta
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := s.LoadMeta(entry.Name())
		if err != nil {
			continue
		}
		out = append(out, meta)
	}
	// Sort by UpdatedAt desc.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].UpdatedAt.After(out[i].UpdatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

// LoadMeta reads agent.json for the given agent ID.
func (s *AgentStore) LoadMeta(agentID string) (AgentMeta, error) {
	data, err := os.ReadFile(filepath.Join(s.AgentDir(agentID), "agent.json"))
	if err != nil {
		return AgentMeta{}, err
	}
	var m AgentMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return AgentMeta{}, fmt.Errorf("parse agent.json: %w", err)
	}
	return m, nil
}

// Create provisions a new agent on disk. identity and soul are
// markdown bodies the user authored on the create form (or accepted
// the defaults for); model is the model ID the agent will use by
// default. Returns the persisted meta with timestamps populated.
func (s *AgentStore) Create(name, identity, soul, model string) (AgentMeta, error) {
	if name == "" {
		return AgentMeta{}, fmt.Errorf("agent name is required")
	}
	id := slugify(name)
	if id == "" {
		return AgentMeta{}, fmt.Errorf("agent name produces empty id: %q", name)
	}
	dir := s.AgentDir(id)
	if _, err := os.Stat(dir); err == nil {
		return AgentMeta{}, fmt.Errorf("agent %q already exists", id)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return AgentMeta{}, err
	}
	now := time.Now().UTC()
	meta := AgentMeta{
		ID:        id,
		Name:      name,
		Model:     model,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.saveMeta(meta); err != nil {
		return AgentMeta{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "IDENTITY.md"), []byte(identity), 0600); err != nil {
		return AgentMeta{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte(soul), 0600); err != nil {
		return AgentMeta{}, err
	}
	return meta, nil
}

// Delete removes an agent and its transcript. Used by /reset to
// clear an agent's history (the TUI re-creates it immediately so the
// user perceives it as a clean slate) and by the picker's delete
// affordance when the user chose "delete files".
func (s *AgentStore) Delete(agentID string) error {
	return os.RemoveAll(s.AgentDir(agentID))
}

// Archive moves an agent directory to <root>/.archive/<id>-<unixts>/
// so the user can recover IDENTITY.md, SOUL.md and history.jsonl from
// disk later. The agent disappears from List because the archive
// lives outside the picker's enumeration root (List skips entries
// without a parsable agent.json at their top level, and the .archive
// directory itself is one).
func (s *AgentStore) Archive(agentID string) error {
	src := s.AgentDir(agentID)
	if _, err := os.Stat(src); err != nil {
		return err
	}
	dest := filepath.Join(s.root, archiveDir)
	if err := os.MkdirAll(dest, 0700); err != nil {
		return err
	}
	target := filepath.Join(dest, agentID+"-"+strconv.FormatInt(time.Now().Unix(), 10))
	return os.Rename(src, target)
}

// LoadIdentity returns the IDENTITY.md body. Missing file returns
// empty string so the agent can render even if a user has deleted
// the file manually.
func (s *AgentStore) LoadIdentity(agentID string) string {
	data, _ := os.ReadFile(filepath.Join(s.AgentDir(agentID), "IDENTITY.md"))
	return string(data)
}

// LoadSoul returns the SOUL.md body.
func (s *AgentStore) LoadSoul(agentID string) string {
	data, _ := os.ReadFile(filepath.Join(s.AgentDir(agentID), "SOUL.md"))
	return string(data)
}

// SystemPrompt composes the Identity and Soul markdown into the
// system prompt the OpenAI backend prepends to every chat completion.
// Both files are optional; an agent with neither yields an empty
// system prompt and the model proceeds with no preamble.
func (s *AgentStore) SystemPrompt(agentID string) string {
	identity := strings.TrimSpace(s.LoadIdentity(agentID))
	soul := strings.TrimSpace(s.LoadSoul(agentID))
	switch {
	case identity != "" && soul != "":
		return "# Identity\n\n" + identity + "\n\n# Soul\n\n" + soul
	case identity != "":
		return "# Identity\n\n" + identity
	case soul != "":
		return "# Soul\n\n" + soul
	}
	return ""
}

// AppendMessage atomically appends a single message to the agent's
// history.jsonl. Touches UpdatedAt so List() orders recent agents
// first.
func (s *AgentStore) AppendMessage(agentID string, msg Message) error {
	dir := s.AgentDir(agentID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if msg.Time.IsZero() {
		msg.Time = time.Now().UTC()
	}
	line, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	f, err := os.OpenFile(filepath.Join(dir, "history.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return err
	}
	return s.touch(agentID)
}

// LoadHistory reads the transcript and returns up to limit most
// recent messages. limit <= 0 returns the full history.
func (s *AgentStore) LoadHistory(agentID string, limit int) ([]Message, error) {
	data, err := os.ReadFile(filepath.Join(s.AgentDir(agentID), "history.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Message
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		out = append(out, msg)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// SetModel updates the agent's default model. Called when /model
// switches mid-session — the choice is persisted so the next launch
// remembers it.
func (s *AgentStore) SetModel(agentID, model string) error {
	meta, err := s.LoadMeta(agentID)
	if err != nil {
		return err
	}
	meta.Model = model
	meta.UpdatedAt = time.Now().UTC()
	return s.saveMeta(meta)
}

// touch updates UpdatedAt without rewriting the agent's content.
func (s *AgentStore) touch(agentID string) error {
	meta, err := s.LoadMeta(agentID)
	if err != nil {
		return err
	}
	meta.UpdatedAt = time.Now().UTC()
	return s.saveMeta(meta)
}

func (s *AgentStore) saveMeta(meta AgentMeta) error {
	dir := s.AgentDir(meta.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".agent-*.json")
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
	return os.Rename(tmpPath, filepath.Join(dir, "agent.json"))
}

// slugify converts a free-form agent name into a filesystem-safe ID:
// lower-case, alphanumerics and hyphens only. The ID is also the
// session key that the chat view uses for event filtering, so it has
// to round-trip through gateway-protocol fields without escaping.
func slugify(s string) string {
	var b strings.Builder
	prevHyphen := true
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		case r == '-' || r == '_' || r == ' ':
			if !prevHyphen {
				b.WriteRune('-')
				prevHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// DefaultIdentity returns the placeholder IDENTITY.md body used when
// the user accepts the suggestion on the create form. The agent's
// chosen name is interpolated as the Name: header so the model
// addresses itself by the right label from turn one. Users can edit
// the file on disk later.
func DefaultIdentity(name string) string {
	if name == "" {
		name = "New agent"
	}
	return "Name: " + name + `

Role: A helpful assistant. Replace this with a description of who this
agent is and what it's for.
`
}

// DefaultSoul is the placeholder body for SOUL.md.
const DefaultSoul = `Tone: Friendly, direct, no fluff.

Working style: Ask clarifying questions when a request is ambiguous.
Prefer concrete examples over abstract explanations. Admit uncertainty
rather than confabulating.
`
