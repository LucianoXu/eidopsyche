// Package update implements the prompt-based auto-update flow for the eidos
// CLI: an asynchronous check against GitHub Releases backed by a 24-hour
// cache, with a stderr prompt when a newer release is available.
//
// The package keeps strict boundaries:
//   - No update logic in cmd/eidos beyond two thin call sites.
//   - All network I/O lives behind a 2-second timeout and is best-effort:
//     errors never propagate to the user's command.
//   - All filesystem I/O lives inside the per-user cache directory and never
//     touches state owned by other subcommands.
package update

import (
	"io"
	"os"
	"sync"
	"time"
)

// BuildInfo carries the running binary's version metadata. Fed in by
// cmd/eidos/version.go so this package never imports main.
type BuildInfo struct {
	Version   string // e.g. "v0.1.0", or "dev" for source builds
	Commit    string
	BuildDate string
}

// CacheTTL is how long a cached check result is considered fresh.
const CacheTTL = 24 * time.Hour

// PromptCooldown is how long after a prompt is shown before showing it again
// for the same known-available version.
const PromptCooldown = 24 * time.Hour

// FetchTimeout caps the GitHub Releases API request. Errors past this point
// are swallowed silently; the user must not pay a UX cost for network flakes.
const FetchTimeout = 2 * time.Second

// LatestReleaseEndpoint is the GitHub Releases "latest" endpoint for the
// canonical Eidopsyche repository. Public, no auth required (60 req/h/IP).
const LatestReleaseEndpoint = "https://api.github.com/repos/LucianoXu/eidopsyche/releases/latest"

// ReleasePageURL formats the URL to a particular release's page on github.com.
func ReleasePageURL(tag string) string {
	return "https://github.com/LucianoXu/eidopsyche/releases/tag/" + tag
}

var refreshOnce sync.Once

// MaybeRefreshAsync starts a background refresh of the update-check cache if
// (a) the user has not opted out, (b) the build is not a developer build, and
// (c) the cache is stale or absent. Returns immediately; callers never wait
// on its result. Safe to invoke multiple times — the refresh runs at most
// once per process.
func MaybeRefreshAsync(b BuildInfo) {
	if SkipReason(b) != "" {
		return
	}
	cache, _ := loadCache()
	if cache != nil && time.Since(cache.CheckedAt) < CacheTTL {
		return
	}
	refreshOnce.Do(func() {
		go refreshInBackground(b)
	})
}

// MaybePrompt prints an "update available" line to w if all of the following
// hold: not opted out, not a dev build, cache reports a newer version, and
// either force is true or the prompt cooldown has elapsed. Returns silently
// otherwise — never errors, never panics.
func MaybePrompt(b BuildInfo, w io.Writer, force bool) {
	if SkipReason(b) != "" {
		return
	}
	cache, err := loadCache()
	if err != nil || cache == nil {
		return
	}
	if !isNewerVersion(b.Version, cache.LatestVersion) {
		return
	}
	if !force && time.Since(cache.LastShownAt) < PromptCooldown {
		return
	}
	writePrompt(w, b.Version, cache.LatestVersion)
	cache.LastShownAt = time.Now().UTC()
	_ = saveCache(cache) // best-effort; a failed save just means we may re-show next invocation.
}

// refreshInBackground performs the network fetch + cache update. Errors are
// intentionally not surfaced to the user.
func refreshInBackground(b BuildInfo) {
	defer func() {
		_ = recover() // never let a panic in the update path crash the user's command.
	}()
	latest, err := fetchLatestTag(FetchTimeout)
	if err != nil {
		return
	}
	cache, _ := loadCache()
	if cache == nil {
		cache = &Cache{}
	}
	cache.CheckedAt = time.Now().UTC()
	cache.CurrentVersion = b.Version
	cache.LatestVersion = latest
	_ = saveCache(cache)
}

// writePrompt formats and writes the user-facing notification.
func writePrompt(w io.Writer, current, latest string) {
	if w == nil {
		w = os.Stderr
	}
	_, _ = io.WriteString(w, "\n")
	_, _ = io.WriteString(w, "A new release of eidos is available: "+current+" → "+latest+"\n")
	_, _ = io.WriteString(w, "To upgrade, run: eidos self-update\n")
	_, _ = io.WriteString(w, ReleasePageURL(latest)+"\n")
}
