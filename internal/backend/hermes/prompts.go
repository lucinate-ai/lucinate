package hermes

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// promptRecord pairs a stored Hermes response ID with the user prompt
// that produced it. Hermes' GET /v1/responses/{id} returns only the
// assistant output; without a client-side prompt log the history-walk
// reconstruction would be assistant-only. The log is bounded to the
// same 100-entry cap as Hermes' server-side LRU so the two stay in
// rough sync.
type promptRecord struct {
	ResponseID string `json:"id"`
	Prompt     string `json:"prompt"`
	Time       int64  `json:"time"`
}

const promptLogCap = 100

// promptLog is a per-connection append-only prompts file with a
// soft cap. Reads load the entire file into a map keyed by response
// ID; the file is small enough (≤100 short JSON lines) that this
// stays cheap.
type promptLog struct {
	mu   sync.Mutex
	path string
}

func newPromptLog(connID string) (*promptLog, error) {
	dir, err := config.DataDir()
	if err != nil {
		return nil, err
	}
	scoped := filepath.Join(dir, "hermes", connID)
	if err := os.MkdirAll(scoped, 0700); err != nil {
		return nil, err
	}
	return &promptLog{path: filepath.Join(scoped, "prompts.jsonl")}, nil
}

// Append records a (responseID → prompt) pair. If the log exceeds the
// cap after the append we trim the oldest entries by rewriting the
// file with the most recent promptLogCap lines.
func (p *promptLog) Append(responseID, prompt string, timestampMS int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	rec := promptRecord{ResponseID: responseID, Prompt: prompt, Time: timestampMS}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return p.trimLockedIfNeeded()
}

// Lookup returns the recorded prompt for a response ID, or empty if
// the entry is missing (which can happen after rotation).
func (p *promptLog) Lookup(responseID string) (promptRecord, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lookupLocked(responseID)
}

// Clear removes the log. Used by SessionDelete.
func (p *promptLog) Clear() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := os.Remove(p.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (p *promptLog) lookupLocked(responseID string) (promptRecord, bool, error) {
	f, err := os.Open(p.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return promptRecord{}, false, nil
		}
		return promptRecord{}, false, err
	}
	defer f.Close()
	var found promptRecord
	var ok bool
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var rec promptRecord
		if err := json.Unmarshal([]byte(strings.TrimSpace(scanner.Text())), &rec); err != nil {
			continue
		}
		if rec.ResponseID == responseID {
			found = rec
			ok = true
			// keep scanning; the same id can appear if a write was
			// retried, in which case we want the latest.
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return promptRecord{}, false, err
	}
	return found, ok, nil
}

func (p *promptLog) trimLockedIfNeeded() error {
	all, err := p.readAllLocked()
	if err != nil {
		return err
	}
	if len(all) <= promptLogCap {
		return nil
	}
	all = all[len(all)-promptLogCap:]
	tmp := p.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, rec := range all {
		line, err := json.Marshal(rec)
		if err != nil {
			_ = f.Close()
			return err
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

func (p *promptLog) readAllLocked() ([]promptRecord, error) {
	f, err := os.Open(p.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []promptRecord
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var rec promptRecord
		if err := json.Unmarshal([]byte(strings.TrimSpace(scanner.Text())), &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return out, nil
}
