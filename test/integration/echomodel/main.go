// Command echomodel is a tiny model server for integration tests. It answers
// chat requests with a canned reply so the gateway can exercise a real chat
// round-trip with no external model and no API charge.
//
// It speaks both the Ollama-native API (/api/tags, /api/show, /api/chat) — so
// it is a drop-in for an "ollama" provider baseUrl — and the OpenAI-compatible
// API (/v1/models, .../chat/completions), so it also works for any
// openai-compatible provider baseUrl.
//
// Usage:
//
//	echomodel [-addr :18080] [-model echo]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", envOr("ECHO_ADDR", ":18080"), "listen address")
	model := flag.String("model", envOr("ECHO_MODEL", "echo"), "model id to advertise")
	flag.Parse()

	srv := &server{model: *model}
	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.route)

	log.Printf("echomodel listening on %s (model=%s)", *addr, *model)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("echomodel: %v", err)
	}
}

type server struct{ model string }

func (s *server) route(w http.ResponseWriter, r *http.Request) {
	log.Printf("%s %s", r.Method, r.URL.Path)
	p := r.URL.Path
	switch {
	case strings.HasSuffix(p, "/api/chat"):
		s.ollamaChat(w, r)
	case strings.HasSuffix(p, "/api/show"):
		s.ollamaShow(w)
	case strings.HasSuffix(p, "/api/tags"):
		s.ollamaTags(w)
	case strings.HasSuffix(p, "/chat/completions"):
		s.openAIChat(w, r)
	case strings.HasSuffix(p, "/models"):
		s.openAIModels(w)
	default:
		// Be permissive: anything else gets an empty 200 so provider warm-up
		// probes don't fail.
		w.WriteHeader(http.StatusOK)
	}
}

// --- Ollama-native -------------------------------------------------------

func (s *server) ollamaTags(w http.ResponseWriter) {
	writeJSON(w, map[string]any{
		"models": []map[string]any{{
			"name":        s.model,
			"model":       s.model,
			"size":        1,
			"digest":      "echo",
			"modified_at": "2026-01-01T00:00:00Z",
			"details":     s.details(),
		}},
	})
}

func (s *server) ollamaShow(w http.ResponseWriter) {
	writeJSON(w, map[string]any{
		"license":      "",
		"modelfile":    "FROM echo",
		"parameters":   "",
		"template":     "{{ .Prompt }}",
		"details":      s.details(),
		"model_info":   map[string]any{"general.architecture": "echo"},
		"capabilities": []string{"completion", "tools"},
	})
}

func (s *server) details() map[string]any {
	return map[string]any{
		"parent_model":       "",
		"format":             "gguf",
		"family":             "echo",
		"families":           []string{"echo"},
		"parameter_size":     "1B",
		"quantization_level": "none",
	}
}

type ollamaChatRequest struct {
	Model    string `json:"model"`
	Stream   *bool  `json:"stream"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

func (s *server) ollamaChat(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req ollamaChatRequest
	_ = json.Unmarshal(body, &req)

	reply := "echo: " + ollamaLastUser(req)
	now := "2026-01-01T00:00:00Z"
	streaming := req.Stream == nil || *req.Stream // ollama defaults to stream

	if !streaming {
		writeJSON(w, map[string]any{
			"model":       s.model,
			"created_at":  now,
			"message":     map[string]any{"role": "assistant", "content": reply},
			"done":        true,
			"done_reason": "stop",
		})
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	flusher, _ := w.(http.Flusher)
	emit := func(v map[string]any) {
		payload, _ := json.Marshal(v)
		fmt.Fprintf(w, "%s\n", payload)
		if flusher != nil {
			flusher.Flush()
		}
	}
	emit(map[string]any{
		"model": s.model, "created_at": now,
		"message": map[string]any{"role": "assistant", "content": reply}, "done": false,
	})
	emit(map[string]any{
		"model": s.model, "created_at": now,
		"message":     map[string]any{"role": "assistant", "content": ""},
		"done":        true,
		"done_reason": "stop",
		"eval_count":  1, "prompt_eval_count": 1,
	})
}

func ollamaLastUser(req ollamaChatRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			return req.Messages[i].Content
		}
	}
	return "ok"
}

// --- OpenAI-compatible ---------------------------------------------------

func (s *server) openAIModels(w http.ResponseWriter) {
	writeJSON(w, map[string]any{
		"object": "list",
		"data":   []map[string]any{{"id": s.model, "object": "model", "owned_by": "echomodel"}},
	})
}

type openAIChatRequest struct {
	Stream   bool `json:"stream"`
	Messages []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"messages"`
}

// toolMarkerRe is the scripted-mode trigger: a prompt containing
// [[tool:shell CMD]] makes the stub reply with an OpenAI-format
// tool_calls invocation of Hermes' terminal tool running CMD, then a
// plain text turn once the tool result comes back. Hermes executes the
// tool for real and emits real tool.start / tool.complete frames over
// the WS — integration tests assert on that protocol structure, not on
// model behaviour.
var toolMarkerRe = regexp.MustCompile(`\[\[tool:shell ([^\]]+)\]\]`)

func (s *server) openAIChat(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req openAIChatRequest
	_ = json.Unmarshal(body, &req)

	created := time.Now().Unix()
	id := "echo-cmpl"

	if m := toolMarkerRe.FindStringSubmatch(openAILastUser(req)); m != nil && !hasToolResult(req) {
		s.openAIToolCall(w, req, strings.TrimSpace(m[1]), created, id)
		return
	}

	reply := "echo: " + openAILastUser(req)
	if lastTool := lastToolResult(req); lastTool != "" {
		// Follow-up request carrying the executed tool's output: close
		// the turn with a deterministic plain-text reply.
		reply = "tool-ok: " + lastTool
	}

	if !req.Stream {
		writeJSON(w, map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": s.model,
			"choices": []map[string]any{{
				"index": 0, "message": map[string]any{"role": "assistant", "content": reply},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	chunk := func(delta map[string]any, finish any) {
		payload, _ := json.Marshal(map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": s.model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		})
		fmt.Fprintf(w, "data: %s\n\n", payload)
		if flusher != nil {
			flusher.Flush()
		}
	}
	chunk(map[string]any{"role": "assistant", "content": reply}, nil)
	chunk(map[string]any{}, "stop")
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func openAILastUser(req openAIChatRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != "user" {
			continue
		}
		switch c := req.Messages[i].Content.(type) {
		case string:
			return c
		case []any:
			for _, part := range c {
				if m, ok := part.(map[string]any); ok {
					if t, ok := m["text"].(string); ok {
						return t
					}
				}
			}
		}
		break
	}
	return "ok"
}

// hasToolResult reports whether the conversation already carries a
// tool-role message — i.e. the scripted call has been executed and
// this request wants the closing text turn.
func hasToolResult(req openAIChatRequest) bool {
	for _, m := range req.Messages {
		if m.Role == "tool" {
			return true
		}
	}
	return false
}

// lastToolResult returns the content of the newest tool-role message,
// or "" when there is none.
func lastToolResult(req openAIChatRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != "tool" {
			continue
		}
		if c, ok := req.Messages[i].Content.(string); ok {
			return strings.TrimSpace(c)
		}
		return ""
	}
	return ""
}

// openAIToolCall answers with a tool_calls completion invoking Hermes'
// terminal tool, in both streaming and non-streaming shapes.
func (s *server) openAIToolCall(w http.ResponseWriter, req openAIChatRequest, command string, created int64, id string) {
	args, _ := json.Marshal(map[string]string{"command": command})
	toolCalls := []map[string]any{{
		"id":   "call_echo_scripted",
		"type": "function",
		"function": map[string]any{
			"name":      "terminal",
			"arguments": string(args),
		},
	}}

	if !req.Stream {
		writeJSON(w, map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": s.model,
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role": "assistant", "content": nil, "tool_calls": toolCalls,
				},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	chunk := func(delta map[string]any, finish any) {
		payload, _ := json.Marshal(map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": s.model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		})
		fmt.Fprintf(w, "data: %s\n\n", payload)
		if flusher != nil {
			flusher.Flush()
		}
	}
	streamCalls := []map[string]any{{
		"index": 0,
		"id":    "call_echo_scripted",
		"type":  "function",
		"function": map[string]any{
			"name":      "terminal",
			"arguments": string(args),
		},
	}}
	chunk(map[string]any{"role": "assistant", "tool_calls": streamCalls}, nil)
	chunk(map[string]any{}, "tool_calls")
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// --- helpers -------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
