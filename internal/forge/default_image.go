package forge

import "github.com/LucianoXu/eidopsyche/internal/version"

// MindFormImageRepo is the container repository on ghcr.io that the
// release pipeline pushes mind-form images to (release.yml's
// `mindform-image` job tags `<repo>:<release-tag>` and `<repo>:latest`).
const MindFormImageRepo = "ghcr.io/lucianoxu/eidopsyche-mindform"

// DefaultImage returns the container image tag a fresh `forge create`
// or a default `forge upgrade` resolves to when the operator does not
// pass --image.
//
// The tag is derived from the host binary's version so a v0.11.2 host
// and a v0.11.2 mind-form image are paired automatically (the release
// pipeline pushes `<repo>:v0.11.2` on each tag push). Source builds
// (`version.Version == "dev"`) fall back to the `:dev` tag, which is
// what `make image IMAGE_TAG=dev` produces locally — preserving the
// developer workflow where you `make image && eidos forge create`.
//
// A function (rather than a package-level var) so tests can override
// `version.Version` and observe the tag flip without ldflags.
func DefaultImage() string {
	tag := version.Version
	if tag == "" || tag == "dev" {
		tag = "dev"
	}
	return MindFormImageRepo + ":" + tag
}
