// Package httpcommon collects the shared HTTP, SSE, and event-emission
// primitives used by the OpenAI and Hermes backends. Both backends are
// HTTP+JSON+SSE shaped over Bearer-token auth, so the request builder,
// transport, SSE scanner, and protocol.Event emitter live here once
// rather than being duplicated.
package httpcommon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client is a thin Bearer-token HTTP client scoped to a base URL. The
// API key is mutable so auth-recovery flows (the TUI's API-key modal)
// can swap it without rebuilding the surrounding backend.
type Client struct {
	httpClient *http.Client
	baseURL    string

	mu     sync.RWMutex
	apiKey string
}

// Options bundles the per-Client configuration.
type Options struct {
	// BaseURL is the OpenAI-compatible endpoint root (e.g.
	// "http://localhost:11434/v1"). Required.
	BaseURL string

	// APIKey is sent as `Authorization: Bearer <key>`. May be empty
	// for endpoints that accept anonymous requests.
	APIKey string

	// HTTPClient lets callers inject a fake transport in tests.
	// Defaults to a client with the connect-timeout-bounded transport.
	HTTPClient *http.Client

	// ConnectTimeout bounds TCP dial and TLS handshake on the default
	// transport. Zero leaves Go's defaults in place. Ignored when
	// HTTPClient is supplied.
	ConnectTimeout time.Duration
}

// NewClient builds a Client. BaseURL must be non-empty.
func NewClient(opts Options) (*Client, error) {
	if opts.BaseURL == "" {
		return nil, fmt.Errorf("httpcommon: BaseURL is required")
	}
	c := opts.HTTPClient
	if c == nil {
		c = &http.Client{Transport: NewDefaultTransport(opts.ConnectTimeout)}
	}
	return &Client{
		httpClient: c,
		baseURL:    opts.BaseURL,
		apiKey:     opts.APIKey,
	}, nil
}

// BaseURL returns the configured endpoint root, trailing slash trimmed.
func (c *Client) BaseURL() string {
	return strings.TrimRight(c.baseURL, "/")
}

// APIKey returns the current API key. Safe for concurrent use.
func (c *Client) APIKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiKey
}

// SetAPIKey replaces the in-memory API key. Auth-recovery flows call
// this when the user enters a new key on the modal.
func (c *Client) SetAPIKey(key string) {
	c.mu.Lock()
	c.apiKey = key
	c.mu.Unlock()
}

// NewRequest constructs an HTTP request with the auth header
// pre-populated. body, when non-nil, is JSON-encoded and the
// Content-Type header is set.
func (c *Client) NewRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	u := c.BaseURL() + "/" + strings.TrimLeft(path, "/")
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key := c.APIKey(); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req, nil
}

// Do executes the request via the underlying http.Client.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	return c.httpClient.Do(req)
}

// NewDefaultTransport clones http.DefaultTransport and overlays
// connect-time deadlines. Streaming reads remain unbounded — only
// dial and TLS handshake are constrained, so a slow or unreachable
// backend gives up at the user-configured bound but a long-running
// chat completion is not cut short.
func NewDefaultTransport(connectTimeout time.Duration) http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	t := base.Clone()
	if connectTimeout > 0 {
		dialer := &net.Dialer{
			Timeout:   connectTimeout,
			KeepAlive: 30 * time.Second,
		}
		t.DialContext = dialer.DialContext
		t.TLSHandshakeTimeout = connectTimeout
	}
	return t
}
