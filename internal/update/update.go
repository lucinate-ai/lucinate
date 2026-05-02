// Package update fetches a small JSON manifest from the project
// website and reports whether a newer release is available. It is
// designed to fail quietly: a network blip, a captive portal, a
// malformed manifest — none of these should ever surface to the user.
package update

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// DefaultManifestURL is where lucinate looks for the latest-version
// manifest in production. Tests inject their own URL.
const DefaultManifestURL = "https://lucinate.ai/latest.json"

// DefaultRequestTimeout bounds the whole request — a slow network
// shouldn't delay TUI startup.
const DefaultRequestTimeout = 5 * time.Second

// DisableEnvVar is the environment variable that unconditionally
// disables the update check, regardless of saved preferences. Any
// truthy value (1/true/yes/on, case-insensitive) opts out.
const DisableEnvVar = "LUCINATE_DISABLE_UPDATE_CHECK"

// maxBodyBytes caps the response body. The manifest is tiny; anything
// larger is a misconfiguration or hostile.
const maxBodyBytes = 4096

// Manifest is the JSON shape published at DefaultManifestURL.
type Manifest struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

// Result describes the outcome of a successful check. Newer is true
// only when Latest is strictly greater than the current version.
type Result struct {
	Latest string
	URL    string
	Newer  bool
}

// Disabled reports whether the environment variable opt-out is set.
func Disabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(DisableEnvVar))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Check fetches the manifest at manifestURL and compares it against
// current. It returns (nil, nil) for any non-actionable outcome:
// network failure, parse failure, non-semver current, or no newer
// version. It only returns a non-nil Result with Newer=true when
// there is something worth telling the user.
//
// The caller's context is wrapped with DefaultRequestTimeout.
func Check(ctx context.Context, manifestURL, current string) (*Result, error) {
	if !shouldCheckCurrent(current) {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, DefaultRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "lucinate/"+current)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}
	// Captive portals love returning HTML with a 200. Reject anything
	// that isn't claiming JSON before we waste a json.Decoder on it.
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(strings.ToLower(ct), "application/json") {
		return nil, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, nil
	}

	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, nil
	}
	if !semver.IsValid(m.Version) {
		return nil, nil
	}

	res := &Result{Latest: m.Version, URL: m.URL}
	res.Newer = semver.Compare(m.Version, current) > 0
	if !res.Newer {
		return nil, nil
	}
	return res, nil
}

// shouldCheckCurrent returns true only for clean stable release
// versions. Dev builds, git-describe pseudo-versions, and any
// pre-release tag are skipped so we never falsely badge a build
// that is already ahead of the published latest.
func shouldCheckCurrent(current string) bool {
	if current == "" || current == "dev" {
		return false
	}
	if !semver.IsValid(current) {
		return false
	}
	if semver.Prerelease(current) != "" {
		return false
	}
	if semver.Build(current) != "" {
		return false
	}
	return true
}
