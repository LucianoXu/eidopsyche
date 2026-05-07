package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// fetchLatestTag returns the tag_name of the latest published release.
// The HTTP call is bounded by `timeout`; any error causes a clean error
// return so callers can swallow it.
func fetchLatestTag(timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LatestReleaseEndpoint, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github releases: status %d", resp.StatusCode)
	}

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	tag := strings.TrimSpace(body.TagName)
	if tag == "" {
		return "", fmt.Errorf("github releases: empty tag_name")
	}
	return tag, nil
}

// isNewerVersion reports whether `latest` is a strictly newer SemVer than
// `current`. Returns false if either side is empty or unparseable —
// developer builds, malformed cache entries, and pre-release weirdness all
// fall through to "no prompt", which is the safe default.
func isNewerVersion(current, latest string) bool {
	cur := normalizeSemver(current)
	lat := normalizeSemver(latest)
	if !semver.IsValid(cur) || !semver.IsValid(lat) {
		return false
	}
	return semver.Compare(cur, lat) < 0
}

// normalizeSemver coerces a tag string into the canonical form expected by
// the semver package. Recognises a leading 'v' and rejects sentinel values
// like "dev" by returning a non-semver string (semver.IsValid will report
// false, and the caller will skip).
func normalizeSemver(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, "v") {
		s = "v" + s
	}
	return s
}
