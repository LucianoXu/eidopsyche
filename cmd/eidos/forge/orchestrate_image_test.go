package forge

import (
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/version"
)

// TestDefaultImage_DerivesTagFromVersion locks down the host-binary →
// mind-form-image version pairing: a release-built binary
// (Version="v0.X.Y") must pull the matching `:v0.X.Y` image tag, while
// a source build (Version="dev") falls back to `:dev`. The release
// pipeline pushes both `:<release-tag>` and `:latest` per tag-push;
// `make image IMAGE_TAG=dev` produces `:dev` locally.
func TestDefaultImage_DerivesTagFromVersion(t *testing.T) {
	cases := []struct {
		version string
		want    string
	}{
		{"dev", "ghcr.io/lucianoxu/eidopsyche-mindform:dev"},
		{"", "ghcr.io/lucianoxu/eidopsyche-mindform:dev"},
		{"v0.11.2", "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"},
		{"v1.0.0-rc1", "ghcr.io/lucianoxu/eidopsyche-mindform:v1.0.0-rc1"},
	}
	prev := version.Version
	t.Cleanup(func() { version.Version = prev })

	for _, c := range cases {
		version.Version = c.version
		if got := DefaultImage(); got != c.want {
			t.Errorf("DefaultImage() with Version=%q = %q, want %q", c.version, got, c.want)
		}
	}
}
