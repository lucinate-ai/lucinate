package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheck_ReportsNewer(t *testing.T) {
	srv := jsonServer(`{"version":"v1.2.0","url":"https://example/v1.2.0"}`)
	defer srv.Close()

	res, err := Check(context.Background(), srv.URL, "v1.1.0")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res == nil {
		t.Fatal("want a result, got nil")
	}
	if !res.Newer || res.Latest != "v1.2.0" || res.URL != "https://example/v1.2.0" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestCheck_ReturnsNilWhenNotNewer(t *testing.T) {
	cases := []struct {
		name, manifest, current string
	}{
		{"equal", `{"version":"v1.2.0","url":"x"}`, "v1.2.0"},
		{"older manifest", `{"version":"v1.0.0","url":"x"}`, "v1.2.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := jsonServer(c.manifest)
			defer srv.Close()
			res, err := Check(context.Background(), srv.URL, c.current)
			if err != nil || res != nil {
				t.Fatalf("want nil/nil, got %+v / %v", res, err)
			}
		})
	}
}

func TestCheck_SkipsNonStableCurrent(t *testing.T) {
	cases := []string{
		"",
		"dev",
		"not-a-version",
		"1.2.3",            // missing v prefix
		"v1.2.3-rc.1",      // prerelease
		"v1.2.3-1-gabcdef", // git-describe pseudo
		"v1.2.3+meta",      // build metadata
	}
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"v9.9.9","url":"x"}`))
	}))
	defer srv.Close()

	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			res, err := Check(context.Background(), srv.URL, c)
			if err != nil || res != nil {
				t.Fatalf("want nil/nil for current=%q, got %+v / %v", c, res, err)
			}
		})
	}
	if hits != 0 {
		t.Fatalf("expected zero HTTP requests, got %d", hits)
	}
}

func TestCheck_QuietFailureModes(t *testing.T) {
	t.Run("non-200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()
		assertCheckNil(t, srv.URL, "v1.0.0")
	})

	t.Run("html content-type", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html>captive portal</html>`))
		}))
		defer srv.Close()
		assertCheckNil(t, srv.URL, "v1.0.0")
	})

	t.Run("malformed json", func(t *testing.T) {
		srv := jsonServer(`not json`)
		defer srv.Close()
		assertCheckNil(t, srv.URL, "v1.0.0")
	})

	t.Run("invalid manifest version", func(t *testing.T) {
		srv := jsonServer(`{"version":"banana","url":"x"}`)
		defer srv.Close()
		assertCheckNil(t, srv.URL, "v1.0.0")
	})

	t.Run("unreachable host", func(t *testing.T) {
		// Non-routable address; relies on the request timeout.
		assertCheckNil(t, "http://127.0.0.1:1/none", "v1.0.0")
	})
}

func TestCheck_BodySizeIsCapped(t *testing.T) {
	huge := strings.Repeat("x", maxBodyBytes*4)
	srv := jsonServer(huge)
	defer srv.Close()
	assertCheckNil(t, srv.URL, "v1.0.0")
}

func TestCheck_RespectsContextDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"v9.9.9","url":"x"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	res, err := Check(ctx, srv.URL, "v1.0.0")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res != nil {
		t.Fatalf("want nil result, got %+v", res)
	}
}

func TestDisabled(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Setenv(DisableEnvVar, v)
		if !Disabled() {
			t.Fatalf("Disabled()=false for %q, want true", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "off", "maybe"} {
		t.Setenv(DisableEnvVar, v)
		if Disabled() {
			t.Fatalf("Disabled()=true for %q, want false", v)
		}
	}
}

func jsonServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func assertCheckNil(t *testing.T, srvURL, current string) {
	t.Helper()
	res, err := Check(context.Background(), srvURL, current)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res != nil {
		t.Fatalf("want nil result, got %+v", res)
	}
}
