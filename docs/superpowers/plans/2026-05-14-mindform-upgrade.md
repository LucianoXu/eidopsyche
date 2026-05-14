# Mind-form Upgrade Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `eidos forge upgrade <name>` — a host-side IPC method + CLI wrapper that swaps a mind-form's container image while preserving the volume, and embed eidos / Claude Code versions into the image so the operator can see what each mind-form is actually running.

**Architecture:** Two coupled changes. (1) Dockerfile bakes two new OCI LABELs (`org.eidopsyche.eidos-version`, `org.eidopsyche.claude-code-version`) plus the same values to `/etc/eidos/*.version` files. (2) A new `internal/forge` package extracts the existing `cmd/eidos/forge/orchestrate.go` step-runner so both `forge create` (existing) and `forge upgrade` (new) can share it. The upgrade flow lives in `internal/forge.Upgrade`, invoked from a new IPC method `forge.upgrade` on the host gate daemon, with a thin CLI wrapper `cmd/eidos/forge/upgrade.go` that always calls DryRun first (renders a version-diff line), then conditionally calls with `DryRun: false` to actually mutate.

**Tech Stack:** Go workspace (Go 1.25), Docker (build + container API via `internal/forgectl`), Cobra (CLI), IPC over unix socket (`internal/ipc`), GitHub Actions + GoReleaser (release pipeline).

**Spec:** `docs/superpowers/specs/2026-05-14-mindform-upgrade-design.md`.

---

## File Map

**Create:**
- `internal/forge/steps.go` — exported `Step` / `RunSteps` (moved from `cmd/eidos/forge/orchestrate.go`).
- `internal/forge/default_image.go` — exported `DefaultImage` / `MindFormImageRepo` (moved).
- `internal/forge/create.go` — exported `Orchestrate` + `CreateOpts` (moved).
- `internal/forge/upgrade.go` — new `Upgrade`, `UpgradeOpts`, `UpgradeResult`.
- `internal/forge/upgrade_test.go` — unit tests for Upgrade (mock client).
- `internal/forgectl/labels.go` — `ImageInspectLabels` method (extension of `Client`) + pure helper `VersionsFromLabels`.
- `internal/forgectl/labels_test.go` — unit tests.
- `internal/daemon/methods_forge.go` — `forge.upgrade` IPC handler + `init()` registration.
- `internal/daemon/methods_forge_test.go` — handler param-validation tests.
- `cmd/eidos/forge/upgrade.go` — `newUpgradeCmd` cobra command.
- `cmd/eidos/forge/upgrade_test.go` — CLI rendering tests (mock IPC).
- `test/integration/forge_upgrade_test.go` — `-tags=integration` real-docker test.

**Modify:**
- `docker/mindform/Dockerfile` — declare `ARG EIDOS_VERSION`, write LABELs + `/etc/eidos/*.version` files.
- `Makefile` — `image` target passes `EIDOS_VERSION` build-arg.
- `.github/workflows/release.yml` — `mindform-image` job passes `EIDOS_VERSION` build-arg.
- `cmd/eidos/forge/orchestrate.go` — becomes a thin shim re-exporting from `internal/forge`.
- `cmd/eidos/forge/create.go` — update imports if needed (likely no-op).
- `cmd/eidos/forge/orchestrate_image_test.go` — update imports.
- `internal/firstcontact/` (any file importing `forge.Orchestrate`) — update imports.
- `cmd/eidos/forge/status.go` — add `eidos:` / `claude-code:` lines to output.
- `cmd/eidos/forge/cmd.go` (or wherever host commands register) — register `newUpgradeCmd()`.
- `internal/ipc/protocol.go` — new `Err*` codes; new `ForgeUpgrade*` types.
- `internal/forgectl/docker.go` — add `ImageInspectLabels` to the `Client` interface + `realClient` implementation.
- `internal/daemon/dashboard_adapter.go` — add `forge.upgrade` to allow-list (if such an allow-list exists; spec calls this out — verify).

---

## Task 0: Image-side version metadata

**Why first:** Establishes the contract every later task reads (LABELs + files). Self-contained — no Go code touched.

**Files:**
- Modify: `docker/mindform/Dockerfile`
- Modify: `Makefile`
- Modify: `.github/workflows/release.yml`

- [ ] **Step 0.1: Add LABELs and version files to Dockerfile**

Open `docker/mindform/Dockerfile`. Find the line `ENV EIDOS_IN_CONTAINER=1` (in Stage 4, near the end). Insert this block **before** that `ENV` line:

```dockerfile
# Version metadata. CLAUDE_CODE_VERSION is the same arg used by Stage 3
# (re-declared here so Stage 4 can reference it). EIDOS_VERSION is
# passed from Makefile / release.yml so the image self-identifies the
# host binary version it shipped with.
ARG CLAUDE_CODE_VERSION
ARG EIDOS_VERSION=dev

LABEL org.eidopsyche.claude-code-version="${CLAUDE_CODE_VERSION}"
LABEL org.eidopsyche.eidos-version="${EIDOS_VERSION}"

RUN mkdir -p /etc/eidos \
 && echo "${CLAUDE_CODE_VERSION}" > /etc/eidos/claude-code.version \
 && echo "${EIDOS_VERSION}"       > /etc/eidos/eidos.version \
 && chmod 0644 /etc/eidos/*.version
```

- [ ] **Step 0.2: Make `image` target pass EIDOS_VERSION**

In `Makefile`, find the existing block (around line 71):

```make
IMAGE_TAG ?= dev

.PHONY: image
image:
	docker build -t ghcr.io/lucianoxu/eidopsyche-mindform:$(IMAGE_TAG) -f docker/mindform/Dockerfile .
```

Replace with:

```make
IMAGE_TAG ?= dev
EIDOS_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: image
image:
	docker build \
		--build-arg EIDOS_VERSION=$(EIDOS_VERSION) \
		-t ghcr.io/lucianoxu/eidopsyche-mindform:$(IMAGE_TAG) \
		-f docker/mindform/Dockerfile .
```

- [ ] **Step 0.3: Make release.yml's `mindform-image` job pass EIDOS_VERSION**

In `.github/workflows/release.yml`, find the `Build & push mind-form image` step. The current `with:` block has `context`, `file`, `push`, `tags`. Add a `build-args` field:

```yaml
      - name: Build & push mind-form image
        uses: docker/build-push-action@v5
        with:
          context: .
          file: docker/mindform/Dockerfile
          push: true
          build-args: |
            EIDOS_VERSION=${{ github.ref_name }}
          tags: |
            ghcr.io/lucianoxu/eidopsyche-mindform:${{ github.ref_name }}
            ghcr.io/lucianoxu/eidopsyche-mindform:latest
```

- [ ] **Step 0.4: Build the image locally and verify LABELs + files**

Run:
```bash
make image IMAGE_TAG=upgrade-spec-test EIDOS_VERSION=v9.9.9-test
docker image inspect ghcr.io/lucianoxu/eidopsyche-mindform:upgrade-spec-test \
  --format '{{json .Config.Labels}}'
```
Expected output (single JSON line):
```
{"org.eidopsyche.claude-code-version":"2.1.138","org.eidopsyche.eidos-version":"v9.9.9-test"}
```

Then verify the in-container files:
```bash
docker run --rm --entrypoint /bin/sh \
  ghcr.io/lucianoxu/eidopsyche-mindform:upgrade-spec-test \
  -c 'cat /etc/eidos/eidos.version /etc/eidos/claude-code.version'
```
Expected:
```
v9.9.9-test
2.1.138
```

- [ ] **Step 0.5: Commit**

```bash
git add docker/mindform/Dockerfile Makefile .github/workflows/release.yml
git commit -m "feat(image): embed eidos + claude-code versions as labels and files"
```

---

## Task 1: `internal/forgectl` — ImageInspectLabels + VersionsFromLabels

**Files:**
- Create: `internal/forgectl/labels.go`
- Create: `internal/forgectl/labels_test.go`
- Modify: `internal/forgectl/docker.go` (add method to `Client` interface + `realClient` impl)

- [ ] **Step 1.1: Write the failing test for VersionsFromLabels**

Create `internal/forgectl/labels_test.go`:

```go
package forgectl

import "testing"

func TestVersionsFromLabels(t *testing.T) {
	cases := []struct {
		name   string
		labels map[string]string
		want   ImageVersions
	}{
		{
			name: "both present",
			labels: map[string]string{
				"org.eidopsyche.eidos-version":       "v0.11.3",
				"org.eidopsyche.claude-code-version": "2.1.140",
			},
			want: ImageVersions{Eidos: "v0.11.3", ClaudeCode: "2.1.140"},
		},
		{
			name:   "neither present",
			labels: map[string]string{"other.label": "x"},
			want:   ImageVersions{},
		},
		{
			name:   "only eidos",
			labels: map[string]string{"org.eidopsyche.eidos-version": "v0.11.3"},
			want:   ImageVersions{Eidos: "v0.11.3"},
		},
		{
			name:   "nil map",
			labels: nil,
			want:   ImageVersions{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := VersionsFromLabels(tc.labels)
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 1.2: Run the test to verify it fails**

```bash
go test ./internal/forgectl/ -run TestVersionsFromLabels -v
```
Expected: `undefined: VersionsFromLabels` / `undefined: ImageVersions`.

- [ ] **Step 1.3: Implement VersionsFromLabels**

Create `internal/forgectl/labels.go`:

```go
package forgectl

import "context"

// Label keys baked into the mind-form image by docker/mindform/Dockerfile.
// Stable contract between the image and the host binary; never rename
// without a coupled image + host release.
const (
	LabelEidosVersion      = "org.eidopsyche.eidos-version"
	LabelClaudeCodeVersion = "org.eidopsyche.claude-code-version"
)

// ImageVersions is the parsed view of the two org.eidopsyche.* labels
// declared on a mind-form image. Missing labels surface as empty
// strings; callers render those as "unknown".
type ImageVersions struct {
	Eidos      string
	ClaudeCode string
}

// VersionsFromLabels extracts the eidos and claude-code versions from
// a labels map (typically Config.Labels from docker image inspect).
// Tolerates nil / missing keys.
func VersionsFromLabels(labels map[string]string) ImageVersions {
	return ImageVersions{
		Eidos:      labels[LabelEidosVersion],
		ClaudeCode: labels[LabelClaudeCodeVersion],
	}
}

// ImageInspectLabels returns the Config.Labels map of the image at ref.
// Used by forge upgrade preflight and forge status to read embedded
// version metadata without starting a container. Returns an empty map
// (not nil) when the image carries no labels.
func (r *realClient) ImageInspectLabels(ctx context.Context, ref string) (map[string]string, error) {
	resp, _, err := r.c.ImageInspectWithRaw(ctx, ref)
	if err != nil {
		return nil, err
	}
	if resp.Config == nil || resp.Config.Labels == nil {
		return map[string]string{}, nil
	}
	return resp.Config.Labels, nil
}
```

- [ ] **Step 1.4: Add ImageInspectLabels to the Client interface**

Open `internal/forgectl/docker.go`. Find the `type Client interface {` block (around line 22). Add this method declaration in the same group as `ImageExists` / `ImagePull`:

```go
	// ImageInspectLabels returns the OCI Config.Labels map of the image
	// at ref. forge upgrade preflight and forge status use it to read
	// the org.eidopsyche.* version labels without starting a container.
	// Returns an empty (non-nil) map when the image has no labels.
	ImageInspectLabels(ctx context.Context, ref string) (map[string]string, error)
```

- [ ] **Step 1.5: Run the unit test and the package's existing tests**

```bash
go test ./internal/forgectl/ -v
```
Expected: `TestVersionsFromLabels` passes (all 4 subtests). Other tests in the package still pass.

- [ ] **Step 1.6: Verify the package builds end-to-end**

```bash
go build ./...
```
Expected: zero errors. If `realClient` does not satisfy the interface, the build will fail with "missing method ImageInspectLabels" — fix the implementation file before continuing.

- [ ] **Step 1.7: Commit**

```bash
git add internal/forgectl/labels.go internal/forgectl/labels_test.go internal/forgectl/docker.go
git commit -m "feat(forgectl): add ImageInspectLabels and VersionsFromLabels"
```

---

## Task 2: Extract `internal/forge` package — steps + default-image + create

**Why:** Both `forge create` (today) and `forge upgrade` (new) want the same `Step` + `RunSteps` machinery, and `forge.upgrade` IPC handler (in `internal/daemon`) cannot import `cmd/eidos/forge`. This task moves the shareable core into `internal/forge` and reduces `cmd/eidos/forge/orchestrate.go` to a shim.

**Files:**
- Create: `internal/forge/steps.go`
- Create: `internal/forge/default_image.go`
- Create: `internal/forge/create.go`
- Modify: `cmd/eidos/forge/orchestrate.go` (becomes shim)
- Modify: `cmd/eidos/forge/orchestrate_image_test.go` (update imports if needed)
- Search: `grep -rn "forge.Orchestrate\|forge.DefaultImage\|forge.CreateOpts" cmd/ internal/` and update imports.

- [ ] **Step 2.1: Create `internal/forge/steps.go`**

```go
// Package forge contains the orchestration core shared between the
// `eidos forge create` CLI flow and the host gate daemon's
// `forge.upgrade` IPC method. CLI commands and daemon handlers both
// call into this package; the package itself does not depend on
// cobra or IPC.
package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Step is one reversible action in an orchestrate pipeline. Undo may
// be nil when a step has nothing to roll back (a read-only check, or
// an idempotent shared-infrastructure mutation like an image pull).
type Step struct {
	Name string
	Do   func(ctx context.Context) error
	Undo func(ctx context.Context) error
}

// RunSteps executes steps in order. On the first error, it runs the
// undo of every step that completed successfully (in reverse) and
// returns the original error. Rollback failures are logged to stderr
// and joined onto the returned error so the operator knows when
// orphan state may need manual cleanup; the do-failure remains the
// primary cause so existing error-string contracts at call sites are
// preserved.
func RunSteps(ctx context.Context, steps []Step) error {
	done := make([]Step, 0, len(steps))
	for _, s := range steps {
		if err := s.Do(ctx); err != nil {
			rollbackErrs := []error{err}
			for i := len(done) - 1; i >= 0; i-- {
				if done[i].Undo == nil {
					continue
				}
				if uerr := done[i].Undo(ctx); uerr != nil {
					fmt.Fprintf(os.Stderr, "orchestrate: rollback of %s failed: %v\n", done[i].Name, uerr)
					rollbackErrs = append(rollbackErrs, fmt.Errorf("rollback %s: %w", done[i].Name, uerr))
				}
			}
			if len(rollbackErrs) == 1 {
				return err
			}
			return errors.Join(rollbackErrs...)
		}
		done = append(done, s)
	}
	return nil
}
```

- [ ] **Step 2.2: Create `internal/forge/default_image.go`**

```go
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
```

- [ ] **Step 2.3: Create `internal/forge/create.go`**

Copy the body of `Orchestrate` and `CreateOpts` from `cmd/eidos/forge/orchestrate.go` (currently lines ~82–226) into `internal/forge/create.go`. Adapt the package path and exports.

Read the current `cmd/eidos/forge/orchestrate.go` and `cmd/eidos/forge/create.go` to find the `CreateOpts` definition (it lives in `create.go`). Move `CreateOpts` here as well, fully exported.

```go
package forge

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/ontology"
)

// CreateOpts carries the parameters for Orchestrate. Field names are
// stable contracts with cmd/eidos/forge/create.go and the First
// Contact wizard.
type CreateOpts struct {
	Label             string
	Owner             string
	OwnerLabel        string
	MindFormNpub      string
	Relay             string
	Model             string
	HeartbeatInterval string
	KeyHex            string
	Image             string
	NoLogin           bool
	PrefabID          string
	SummoningBook     string
	RoleResearch      string
	JournalEntry      string
}

// Orchestrate is the testable seam for `eidos forge create` and the
// First Contact wizard's phase 3. Mid-flight failures (init-volume,
// container-create) reverse-undo any completed steps so the operator
// can retry cleanly without manual `docker volume rm` / `docker rm`
// housekeeping.
//
// CLAUDE.md "Single Call Path": container creation is a sanctioned
// bootstrap exception that does not flow through the daemon's
// methodTable. forge.upgrade (a sibling flow in this package) does
// flow through IPC; the asymmetry is acknowledged and tolerated
// until the broader unified-call-path migration.
func Orchestrate(ctx context.Context, c forgectl.Client, name string, o CreateOpts) error {
	vol := forgectl.VolumeName(name)
	cont := forgectl.ContainerName(name)

	if exists, err := c.VolumeExists(ctx, vol); err != nil {
		return fmt.Errorf("check volume: %w", err)
	} else if exists {
		return fmt.Errorf("volume %s already exists", vol)
	}
	if exists, err := c.ContainerExists(ctx, cont); err != nil {
		return fmt.Errorf("check container: %w", err)
	} else if exists {
		return fmt.Errorf("container %s already exists", cont)
	}

	image := o.Image
	if image == "" {
		image = DefaultImage()
	}

	steps := []Step{
		{
			Name: "ensure-image",
			Do: func(ctx context.Context) error {
				exists, err := c.ImageExists(ctx, image)
				if err != nil {
					return fmt.Errorf("inspect image %s: %w", image, err)
				}
				if exists {
					return nil
				}
				if err := c.ImagePull(ctx, image, os.Stderr); err != nil {
					return fmt.Errorf("pull %s: %w", image, err)
				}
				return nil
			},
		},
		{
			Name: "create-volume",
			Do: func(ctx context.Context) error {
				if err := c.VolumeCreate(ctx, vol); err != nil {
					return fmt.Errorf("create volume: %w", err)
				}
				return nil
			},
			Undo: func(ctx context.Context) error { return c.VolumeRemove(ctx, vol) },
		},
		{
			Name: "init-volume",
			Do: func(ctx context.Context) error {
				pipeR, pipeW := io.Pipe()
				go func() {
					defer pipeW.Close()
					params := ontology.Params{
						Label:         o.Label,
						OwnerNpub:     o.Owner,
						OwnerLabel:    o.OwnerLabel,
						MindFormNpub:  o.MindFormNpub,
						HomeRelay:     o.Relay,
						CreatedDate:   time.Now().UTC().Format("2006-01-02"),
						SummoningBook: o.SummoningBook,
						RoleResearch:  o.RoleResearch,
					}
					var perr error
					if o.PrefabID != "" {
						perr = ontology.TarStreamPrefab(pipeW, o.PrefabID, params)
					} else {
						perr = ontology.TarStream(pipeW, params)
					}
					if perr != nil {
						_ = pipeW.CloseWithError(perr)
					}
				}()

				env := []string{
					"EIDOS_IN_CONTAINER=1",
					"EIDOS_FORGE_NAME=" + name,
					"EIDOS_FORGE_LABEL=" + o.Label,
					"EIDOS_FORGE_OWNER=" + o.Owner,
					"EIDOS_FORGE_RELAY=" + o.Relay,
					"EIDOS_FORGE_MODEL=" + o.Model,
					"EIDOS_FORGE_HEARTBEAT_INTERVAL=" + o.HeartbeatInterval,
				}
				if o.KeyHex != "" {
					env = append(env, "EIDOS_FORGE_KEY_HEX="+o.KeyHex)
				}
				res, err := c.RunInit(ctx, forgectl.RunInitOpts{
					Image: image,
					Mount: forgectl.Mount{VolumeName: vol, Target: "/eidos"},
					User:  "0:0",
					Env:   env,
					Cmd:   []string{"eidos", "forge", "init-volume"},
					Stdin: pipeR,
				})
				if err != nil {
					return fmt.Errorf("init-volume: %w (stderr: %s)", err, string(res.Stderr))
				}
				return nil
			},
		},
		{
			Name: "create-container",
			Do: func(ctx context.Context) error {
				if err := c.ContainerCreate(ctx, forgectl.CreateOpts{
					Name:  cont,
					Image: image,
					Mount: forgectl.Mount{VolumeName: vol, Target: "/eidos"},
				}); err != nil {
					return fmt.Errorf("create container: %w", err)
				}
				return nil
			},
			Undo: func(ctx context.Context) error { return c.ContainerRemove(ctx, cont) },
		},
	}
	return RunSteps(ctx, steps)
}
```

- [ ] **Step 2.4: Replace `cmd/eidos/forge/orchestrate.go` with a shim**

Open `cmd/eidos/forge/orchestrate.go` and replace its entire contents with:

```go
package forge

import (
	"context"

	"github.com/LucianoXu/eidopsyche/internal/forge"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// CreateOpts re-exports the orchestration option struct so the
// existing call sites in this package (create.go) and external
// callers (firstcontact) compile unchanged. The body now lives in
// internal/forge.
type CreateOpts = forge.CreateOpts

// MindFormImageRepo re-exports the canonical mind-form image
// repository constant.
const MindFormImageRepo = forge.MindFormImageRepo

// DefaultImage re-exports internal/forge.DefaultImage. The function
// (rather than a const) preserves the existing test seam that
// overrides version.Version at runtime.
func DefaultImage() string { return forge.DefaultImage() }

// Orchestrate re-exports internal/forge.Orchestrate. Kept as a
// function (not a var assignment) so the stable cobra-side signature
// is documented at this layer too. See internal/forge/create.go for
// the body.
func Orchestrate(ctx context.Context, c forgectl.Client, name string, o CreateOpts) error {
	return forge.Orchestrate(ctx, c, name, o)
}
```

- [ ] **Step 2.5: Update existing tests that import the old symbols**

Run:
```bash
grep -rln "cmd/eidos/forge\".*Orchestrate\|forge\.Orchestrate\|forge\.DefaultImage" cmd internal
```

For each hit, check whether the importer is `cmd/eidos/forge/...` (package forge — symbols still resolve via shim) or external. External importers may already be fine because the shim re-exports. Adjust imports only if a compile error surfaces.

Then build everything:
```bash
go build ./...
```
Expected: zero errors.

- [ ] **Step 2.6: Run existing unit tests to confirm no regression**

```bash
go test ./cmd/eidos/forge/... ./internal/forge/... ./internal/forgectl/... ./internal/firstcontact/...
```
Expected: all pass. The orchestrate-related tests still cover the create path through the shim.

- [ ] **Step 2.7: Commit**

```bash
git add internal/forge/ cmd/eidos/forge/orchestrate.go
git commit -m "refactor(forge): extract orchestration core into internal/forge"
```

---

## Task 3: `internal/forge.Upgrade` — TDD the core

**Files:**
- Create: `internal/forge/upgrade.go`
- Create: `internal/forge/upgrade_test.go`

The Upgrade flow needs a `forgectl.Client` plus two additional capabilities not currently on the interface: (a) `ImageInspectLabels` (added in Task 1), (b) reading the image ID of a container (`ContainerInspectImage`). Check whether the latter exists; if not, this task adds it.

- [ ] **Step 3.1: Audit `forgectl.Client` for image-id-on-container retrieval**

Run:
```bash
grep -n "func.*Container.*Inspect\|Container.*Image" internal/forgectl/docker.go
```
If `ContainerInspectImage(ctx, name) (imageID string, imageRef string, err error)` already exists or the existing `ContainerInspectState` returns image info, use that. If not, add a new method.

Assuming it does NOT exist, add this to the `Client` interface (in `internal/forgectl/docker.go`) in the container-methods group:

```go
	// ContainerInspectImage returns the image ID (sha256:...) and image
	// ref (the tag the container was created with, e.g.
	// "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2") for the
	// container at name. Returns "", "", nil when the container does
	// not exist — callers should call ContainerExists first if the
	// presence check matters.
	ContainerInspectImage(ctx context.Context, name string) (id string, ref string, err error)
```

And the corresponding `realClient` method (in the same file, near other Container* impls):

```go
func (r *realClient) ContainerInspectImage(ctx context.Context, name string) (string, string, error) {
	insp, err := r.c.ContainerInspect(ctx, name)
	if err != nil {
		if client.IsErrNotFound(err) {
			return "", "", nil
		}
		return "", "", err
	}
	return insp.Image, insp.Config.Image, nil
}
```
Verify `client` and the inspect call match the imports already in the file; adjust accordingly.

- [ ] **Step 3.2: Write the failing test for Upgrade — happy path**

Create `internal/forge/upgrade_test.go`. Start with a fake client built around recorded calls:

```go
package forge

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// fakeClient records every Client call and serves canned responses.
type fakeClient struct {
	mu sync.Mutex

	containerImages   map[string]string // container name → image ref
	containerStates   map[string]string // container name → "running"/"exited"/"absent"
	imageIDs          map[string]string // image ref → image ID (sha256)
	imageLabels       map[string]map[string]string
	imageExistsLocal  map[string]bool
	idleAfterNPolls   int
	idlePolls         int
	healthAfterNPolls int
	healthPolls       int

	calls []string
}

func (f *fakeClient) record(name string) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
}

func (f *fakeClient) ContainerInspectState(ctx context.Context, name string) (string, error) {
	f.record("ContainerInspectState:" + name)
	s, ok := f.containerStates[name]
	if !ok {
		return "absent", nil
	}
	return s, nil
}

func (f *fakeClient) ContainerInspectImage(ctx context.Context, name string) (string, string, error) {
	f.record("ContainerInspectImage:" + name)
	ref, ok := f.containerImages[name]
	if !ok {
		return "", "", nil
	}
	return f.imageIDs[ref], ref, nil
}

func (f *fakeClient) ImageExists(ctx context.Context, ref string) (bool, error) {
	f.record("ImageExists:" + ref)
	return f.imageExistsLocal[ref], nil
}

func (f *fakeClient) ImagePull(ctx context.Context, ref string, out io.Writer) error {
	f.record("ImagePull:" + ref)
	f.imageExistsLocal[ref] = true
	return nil
}

func (f *fakeClient) ImageInspectLabels(ctx context.Context, ref string) (map[string]string, error) {
	f.record("ImageInspectLabels:" + ref)
	return f.imageLabels[ref], nil
}

func (f *fakeClient) ContainerStop(ctx context.Context, name string, grace int) error {
	f.record(fmt.Sprintf("ContainerStop:%s:%d", name, grace))
	f.containerStates[name] = "exited"
	return nil
}

func (f *fakeClient) ContainerRemove(ctx context.Context, name string) error {
	f.record("ContainerRemove:" + name)
	delete(f.containerStates, name)
	delete(f.containerImages, name)
	return nil
}

func (f *fakeClient) ContainerCreate(ctx context.Context, opts forgectl.CreateOpts) error {
	f.record("ContainerCreate:" + opts.Name + ":" + opts.Image)
	f.containerStates[opts.Name] = "exited"
	f.containerImages[opts.Name] = opts.Image
	return nil
}

func (f *fakeClient) ContainerStart(ctx context.Context, name string) error {
	f.record("ContainerStart:" + name)
	f.containerStates[name] = "running"
	return nil
}

// Stubs for Client methods Upgrade doesn't call but the interface requires.
func (f *fakeClient) ContainerExists(ctx context.Context, name string) (bool, error) {
	return f.containerStates[name] != "" && f.containerStates[name] != "absent", nil
}
func (f *fakeClient) ContainerExec(ctx context.Context, name string, cmd []string) (forgectl.ExecResult, error) {
	return forgectl.ExecResult{}, nil
}
func (f *fakeClient) VolumeExists(ctx context.Context, name string) (bool, error)  { return true, nil }
func (f *fakeClient) VolumeCreate(ctx context.Context, name string) error          { return nil }
func (f *fakeClient) VolumeRemove(ctx context.Context, name string) error          { return nil }
func (f *fakeClient) RunInit(ctx context.Context, opts forgectl.RunInitOpts) (forgectl.ExecResult, error) {
	return forgectl.ExecResult{}, nil
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		containerImages:  map[string]string{},
		containerStates:  map[string]string{},
		imageIDs:         map[string]string{},
		imageLabels:      map[string]map[string]string{},
		imageExistsLocal: map[string]bool{},
	}
}

// callsContaining returns the recorded call substrings that contain
// any of the given fragments. Used for assertions like "did we ever
// call ContainerStop?".
func (f *fakeClient) hasCall(substr string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func TestUpgrade_HappyPath(t *testing.T) {
	c := newFakeClient()
	c.containerImages["mindform-alice"] = "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"
	c.containerStates["mindform-alice"] = "running"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = "sha256:aaa"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = "sha256:bbb"
	c.imageLabels["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = map[string]string{
		"org.eidopsyche.eidos-version":       "v0.11.2",
		"org.eidopsyche.claude-code-version": "2.1.138",
	}
	c.imageLabels["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = map[string]string{
		"org.eidopsyche.eidos-version":       "v0.11.3",
		"org.eidopsyche.claude-code-version": "2.1.140",
	}

	res, err := Upgrade(context.Background(), c, UpgradeOpts{
		Name:  "alice",
		Image: "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3",
		Grace: 10,
	})
	if err != nil {
		t.Fatalf("Upgrade returned error: %v", err)
	}
	if res.Skipped {
		t.Fatalf("Upgrade reported Skipped on a real version bump")
	}
	if res.OldEidos != "v0.11.2" || res.NewEidos != "v0.11.3" {
		t.Errorf("eidos diff: old=%q new=%q", res.OldEidos, res.NewEidos)
	}
	if res.OldClaudeCode != "2.1.138" || res.NewClaudeCode != "2.1.140" {
		t.Errorf("claude-code diff: old=%q new=%q", res.OldClaudeCode, res.NewClaudeCode)
	}
	if !c.hasCall("ContainerStop:mindform-alice") {
		t.Error("missing ContainerStop")
	}
	if !c.hasCall("ContainerRemove:mindform-alice") {
		t.Error("missing ContainerRemove")
	}
	if !c.hasCall("ContainerCreate:mindform-alice:ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3") {
		t.Error("missing ContainerCreate with new image")
	}
	if !c.hasCall("ContainerStart:mindform-alice") {
		t.Error("missing ContainerStart")
	}
}

func TestUpgrade_DryRunDoesNotMutate(t *testing.T) {
	c := newFakeClient()
	c.containerImages["mindform-alice"] = "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"
	c.containerStates["mindform-alice"] = "running"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = "sha256:aaa"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = "sha256:bbb"
	c.imageLabels["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = map[string]string{
		"org.eidopsyche.eidos-version": "v0.11.2",
	}
	c.imageLabels["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = map[string]string{
		"org.eidopsyche.eidos-version": "v0.11.3",
	}

	res, err := Upgrade(context.Background(), c, UpgradeOpts{
		Name:   "alice",
		Image:  "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("DryRun Upgrade returned error: %v", err)
	}
	if !res.DryRun {
		t.Error("DryRun flag should echo true in result")
	}
	if c.hasCall("ContainerStop") || c.hasCall("ContainerRemove") || c.hasCall("ContainerCreate") || c.hasCall("ContainerStart") {
		t.Errorf("DryRun must not mutate container; calls: %v", c.calls)
	}
}

func TestUpgrade_SkippedWhenImageIDMatches(t *testing.T) {
	c := newFakeClient()
	c.containerImages["mindform-alice"] = "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"
	c.containerStates["mindform-alice"] = "running"
	// Same image ID for both refs — common when --image is the same tag the container is on.
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = "sha256:same"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:latest"] = "sha256:same"
	c.imageLabels["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = map[string]string{}
	c.imageLabels["ghcr.io/lucianoxu/eidopsyche-mindform:latest"] = map[string]string{}

	res, err := Upgrade(context.Background(), c, UpgradeOpts{
		Name:  "alice",
		Image: "ghcr.io/lucianoxu/eidopsyche-mindform:latest",
	})
	if err != nil {
		t.Fatalf("Upgrade returned error: %v", err)
	}
	if !res.Skipped {
		t.Fatal("expected Skipped=true when image IDs match")
	}
	if c.hasCall("ContainerStop") || c.hasCall("ContainerRemove") {
		t.Errorf("Skipped path must not mutate: %v", c.calls)
	}
}
```

- [ ] **Step 3.3: Run the tests to confirm they fail**

```bash
go test ./internal/forge/ -run TestUpgrade -v
```
Expected: build failure / `undefined: Upgrade` / `undefined: UpgradeOpts` / `undefined: UpgradeResult`.

- [ ] **Step 3.4: Implement `internal/forge/upgrade.go`**

```go
package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// UpgradeOpts carries the parameters for Upgrade. Fields map 1:1 to
// the JSON params of the forge.upgrade IPC method (see
// internal/ipc/protocol.go).
type UpgradeOpts struct {
	Name        string
	Image       string        // empty → DefaultImage()
	WaitIdle    bool
	IdleTimeout time.Duration // zero → 10 minutes
	Grace       int           // zero → 10 seconds
	DryRun      bool
}

// UpgradeResult is the structured output of Upgrade. Fields mirror
// ForgeUpgradeResult in internal/ipc/protocol.go.
type UpgradeResult struct {
	Name          string
	OldImage      string
	NewImage      string
	OldEidos      string
	NewEidos      string
	OldClaudeCode string
	NewClaudeCode string
	Skipped       bool
	SkippedReason string
	DryRun        bool
}

// Typed errors that the daemon handler maps to IPC error codes. Keep
// the strings short and stable; the daemon-side mapping switches on
// errors.Is and translates to the E_FORGE_* codes.
var (
	ErrUpgradeNotFound        = errors.New("forge upgrade: mind-form not found")
	ErrUpgradeImagePull       = errors.New("forge upgrade: image pull failed")
	ErrUpgradeImageInspect    = errors.New("forge upgrade: image inspect failed")
	ErrUpgradeIdleTimeout     = errors.New("forge upgrade: agentloop did not become idle within timeout")
	ErrUpgradeContainerStop   = errors.New("forge upgrade: container stop failed")
	ErrUpgradeContainerRemove = errors.New("forge upgrade: container remove failed")
	ErrUpgradeContainerCreate = errors.New("forge upgrade: container create failed")
	ErrUpgradeContainerStart  = errors.New("forge upgrade: container start failed")
	ErrUpgradeHealthTimeout   = errors.New("forge upgrade: container did not become healthy within 30s")
)

// HealthProbeTimeout caps how long Upgrade waits for the new
// container's IPC socket to come up before declaring health failure.
const HealthProbeTimeout = 30 * time.Second

// Upgrade swaps the mind-form's container to a new image while
// preserving the volume.
//
// Steps (with DryRun cut-out after step 2):
//
//  1. inspect-current: read the current container's image ref + ID + LABELs.
//  2. ensure-image:    pull opts.Image if missing locally, then read its LABELs.
//                      DryRun returns here.
//  3. wait-idle:       (opt-in) poll the container's runtime-state until
//                      the agentloop reports idle, or until IdleTimeout.
//  4. stop:            ContainerStop with Grace.
//  5. remove:          ContainerRemove.
//  6. create:          ContainerCreate with the new image, same volume.
//  7. start:           ContainerStart.
//  8. health-verify:   poll the in-container IPC until healthy or 30s.
//
// Step 3 aborts cleanly on timeout (no container mutation yet). Step
// 5 is the commit point; failures after step 5 leave the container
// absent — the operator re-runs Upgrade, which the idempotent
// re-entry path in step 6 handles.
func Upgrade(ctx context.Context, c forgectl.Client, opts UpgradeOpts) (UpgradeResult, error) {
	if opts.Name == "" {
		return UpgradeResult{}, fmt.Errorf("Upgrade: empty name")
	}
	if opts.Image == "" {
		opts.Image = DefaultImage()
	}
	if opts.Grace == 0 {
		opts.Grace = 10
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 10 * time.Minute
	}

	cont := forgectl.ContainerName(opts.Name)

	res := UpgradeResult{
		Name:     opts.Name,
		NewImage: opts.Image,
		DryRun:   opts.DryRun,
	}

	// Step 1: inspect current.
	oldImageID, oldRef, err := c.ContainerInspectImage(ctx, cont)
	if err != nil {
		return res, fmt.Errorf("%w: inspect current container: %v", ErrUpgradeNotFound, err)
	}
	res.OldImage = oldRef
	if oldRef != "" {
		labels, lerr := c.ImageInspectLabels(ctx, oldRef)
		if lerr != nil {
			// Tolerate — old image may have been pruned. Leave version fields empty.
			oldImageID = ""
		} else {
			v := forgectl.VersionsFromLabels(labels)
			res.OldEidos = v.Eidos
			res.OldClaudeCode = v.ClaudeCode
		}
	}

	// Step 2: ensure new image, read its labels.
	existsLocal, err := c.ImageExists(ctx, opts.Image)
	if err != nil {
		return res, fmt.Errorf("%w: %v", ErrUpgradeImageInspect, err)
	}
	if !existsLocal {
		if err := c.ImagePull(ctx, opts.Image, os.Stderr); err != nil {
			return res, fmt.Errorf("%w: %v", ErrUpgradeImagePull, err)
		}
	}
	newLabels, err := c.ImageInspectLabels(ctx, opts.Image)
	if err != nil {
		return res, fmt.Errorf("%w: new image: %v", ErrUpgradeImageInspect, err)
	}
	v := forgectl.VersionsFromLabels(newLabels)
	res.NewEidos = v.Eidos
	res.NewClaudeCode = v.ClaudeCode

	// Skipped check: image IDs match.
	newImageID, _, err := imageID(ctx, c, opts.Image)
	if err != nil {
		return res, fmt.Errorf("%w: resolve new image ID: %v", ErrUpgradeImageInspect, err)
	}
	if oldImageID != "" && newImageID == oldImageID {
		res.Skipped = true
		res.SkippedReason = fmt.Sprintf("already at %s", opts.Image)
		return res, nil
	}

	if opts.DryRun {
		return res, nil
	}

	// Step 3 (optional): wait-idle.
	if opts.WaitIdle {
		if err := waitIdle(ctx, c, cont, opts.IdleTimeout); err != nil {
			return res, err
		}
	}

	// Steps 4-7: stop → remove → create → start.
	// (Step 4 and 5 may be no-ops if the container is already absent
	// or stopped — the recovery path for SIGINT'd mid-flow upgrades.)
	state, err := c.ContainerInspectState(ctx, cont)
	if err != nil {
		return res, fmt.Errorf("inspect state: %w", err)
	}
	if state == "running" {
		if err := c.ContainerStop(ctx, cont, opts.Grace); err != nil {
			return res, fmt.Errorf("%w: %v", ErrUpgradeContainerStop, err)
		}
	}
	if state != "absent" {
		if err := c.ContainerRemove(ctx, cont); err != nil {
			return res, fmt.Errorf("%w: %v", ErrUpgradeContainerRemove, err)
		}
	}
	if err := c.ContainerCreate(ctx, forgectl.CreateOpts{
		Name:  cont,
		Image: opts.Image,
		Mount: forgectl.Mount{VolumeName: forgectl.VolumeName(opts.Name), Target: "/eidos"},
	}); err != nil {
		return res, fmt.Errorf("%w: %v", ErrUpgradeContainerCreate, err)
	}
	if err := c.ContainerStart(ctx, cont); err != nil {
		return res, fmt.Errorf("%w: %v", ErrUpgradeContainerStart, err)
	}

	// Step 8: health verify.
	if err := waitHealthy(ctx, c, cont, HealthProbeTimeout); err != nil {
		return res, err
	}

	return res, nil
}

// imageID resolves an image ref to its content-addressable ID
// (sha256:...). Used for the Skipped check.
func imageID(ctx context.Context, c forgectl.Client, ref string) (string, string, error) {
	// Reuse ContainerInspectImage semantics by creating a transient
	// inspect against the image ref directly. Today's Client does not
	// expose ImageInspectID, so we read it off the labels-inspect
	// channel: this is an internal helper that adds the docker call
	// when needed. Implementation: read from a future
	// Client.ImageInspectID — for now, derive via ImageInspectLabels
	// is not enough (labels don't carry the ID). Therefore add
	// ImageInspectID to Client (parallel to ImageInspectLabels) before
	// this lands.
	return "", "", fmt.Errorf("not implemented: ImageInspectID")
}

// waitIdle polls the in-container runtime-state until the agentloop
// reports an idle phase, or until timeout. Returns ErrUpgradeIdleTimeout
// on timeout.
func waitIdle(ctx context.Context, c forgectl.Client, cont string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		res, err := c.ContainerExec(ctx, cont, []string{"eidos", "forge", "runtime-state"})
		if err == nil && res.ExitCode == 0 && isIdlePhase(res.Stdout) {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrUpgradeIdleTimeout
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// isIdlePhase returns true when the runtime-state JSON shows an
// agentloop phase that's safe to interrupt. Conservative: only
// "idle"/"sleeping" count as idle; "awake" / "in_turn" do not.
func isIdlePhase(jsonStdout []byte) bool {
	// Cheap substring match avoids pulling the RuntimeState struct
	// into this package and creating an import cycle with
	// cmd/eidos/forge. The JSON shape is stable per the runtime-state
	// schema; if the phase key spelling ever changes, update here.
	s := string(jsonStdout)
	return containsAny(s, `"phase":"idle"`, `"phase":"sleeping"`)
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

// waitHealthy polls the new container's gate IPC until it responds,
// or until timeout. Reuses the same runtime-state probe.
func waitHealthy(ctx context.Context, c forgectl.Client, cont string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	for {
		res, err := c.ContainerExec(ctx, cont, []string{"eidos", "forge", "runtime-state"})
		if err == nil && res.ExitCode == 0 && len(res.Stdout) > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrUpgradeHealthTimeout
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
```

**Note on the `imageID` helper:** it depends on a `Client.ImageInspectID` that does not yet exist. The next step adds it; this code refers to it via the helper.

- [ ] **Step 3.5: Add `ImageInspectID` to the Client interface**

Open `internal/forgectl/docker.go` and add to the `Client` interface (in the image-methods group):

```go
	// ImageInspectID returns the content-addressable image ID
	// (sha256:...) for ref. Returns "" when the image is not present
	// locally — callers should ImageExists or ImagePull first.
	ImageInspectID(ctx context.Context, ref string) (string, error)
```

Add the `realClient` implementation alongside `ImageInspectLabels` in `internal/forgectl/labels.go`:

```go
// ImageInspectID returns the content-addressable image ID for ref.
// Used by forge upgrade to detect "already at this image" (same ID
// even when refs differ, e.g. user passes :latest while the container
// is already on the matching :vX.Y.Z tag).
func (r *realClient) ImageInspectID(ctx context.Context, ref string) (string, error) {
	resp, _, err := r.c.ImageInspectWithRaw(ctx, ref)
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}
```

Now rewrite the `imageID` helper in `internal/forge/upgrade.go` to use it:

```go
func imageID(ctx context.Context, c forgectl.Client, ref string) (string, string, error) {
	id, err := c.ImageInspectID(ctx, ref)
	if err != nil {
		return "", "", err
	}
	return id, ref, nil
}
```

Update the fake client in `internal/forge/upgrade_test.go` — add:

```go
func (f *fakeClient) ImageInspectID(ctx context.Context, ref string) (string, error) {
	f.record("ImageInspectID:" + ref)
	return f.imageIDs[ref], nil
}
```

- [ ] **Step 3.6: Run the Upgrade tests**

```bash
go test ./internal/forge/ -run TestUpgrade -v
```
Expected: all three subtests pass (`TestUpgrade_HappyPath`, `TestUpgrade_DryRunDoesNotMutate`, `TestUpgrade_SkippedWhenImageIDMatches`).

- [ ] **Step 3.7: Add the wait-idle timeout test**

Append to `internal/forge/upgrade_test.go`:

```go
func TestUpgrade_WaitIdleTimeout(t *testing.T) {
	c := newFakeClient()
	c.containerImages["mindform-alice"] = "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"
	c.containerStates["mindform-alice"] = "running"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = "sha256:aaa"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = "sha256:bbb"
	c.imageLabels["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = map[string]string{}
	c.imageLabels["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = map[string]string{}
	// ContainerExec stub always returns non-idle so wait-idle never advances.

	_, err := Upgrade(context.Background(), c, UpgradeOpts{
		Name:        "alice",
		Image:       "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3",
		WaitIdle:    true,
		IdleTimeout: 10 * time.Millisecond,
	})
	if !errors.Is(err, ErrUpgradeIdleTimeout) {
		t.Fatalf("expected ErrUpgradeIdleTimeout, got %v", err)
	}
	if c.hasCall("ContainerStop") || c.hasCall("ContainerRemove") {
		t.Errorf("wait-idle timeout must abort before mutation; calls: %v", c.calls)
	}
}
```

Add the imports `"errors"` and `"time"` if not already present.

Run:
```bash
go test ./internal/forge/ -run TestUpgrade_WaitIdleTimeout -v
```
Expected: PASS.

- [ ] **Step 3.8: Commit**

```bash
git add internal/forge/upgrade.go internal/forge/upgrade_test.go internal/forgectl/docker.go internal/forgectl/labels.go
git commit -m "feat(forge): implement internal/forge.Upgrade with DryRun and wait-idle"
```

---

## Task 4: IPC protocol — error codes + Params/Result types

**Files:**
- Modify: `internal/ipc/protocol.go`

- [ ] **Step 4.1: Add the new error codes**

Open `internal/ipc/protocol.go`. Find the existing `const (` block of `Err*` codes (around line 34). Append (inside the same block, before the closing `)`):

```go
	ErrForgeNotFound        = "FORGE_NOT_FOUND"
	ErrForgeImagePull       = "FORGE_IMAGE_PULL"
	ErrForgeImageInspect    = "FORGE_IMAGE_INSPECT"
	ErrForgeIdleTimeout     = "FORGE_IDLE_TIMEOUT"
	ErrForgeContainerStop   = "FORGE_CONTAINER_STOP"
	ErrForgeContainerRemove = "FORGE_CONTAINER_REMOVE"
	ErrForgeContainerCreate = "FORGE_CONTAINER_CREATE"
	ErrForgeContainerStart  = "FORGE_CONTAINER_START"
	ErrForgeHealthTimeout   = "FORGE_HEALTH_TIMEOUT"
```

- [ ] **Step 4.2: Add the params and result types**

Append at the end of `internal/ipc/protocol.go`:

```go
// ForgeUpgradeParams is the JSON params shape for the forge.upgrade
// IPC method. Optional fields use zero-value defaults: Image empty →
// DefaultImage(), Grace 0 → 10s, IdleTimeout "" → "10m". DryRun=true
// stops after the version-diff preflight without touching the
// container.
type ForgeUpgradeParams struct {
	Name        string `json:"name"`
	Image       string `json:"image,omitempty"`
	WaitIdle    bool   `json:"wait_idle,omitempty"`
	IdleTimeout string `json:"idle_timeout,omitempty"`
	Grace       int    `json:"grace,omitempty"`
	DryRun      bool   `json:"dry_run,omitempty"`
}

// ForgeUpgradeResult is the JSON result shape for forge.upgrade.
// Old* / New* fields are version strings parsed from the
// org.eidopsyche.* labels of the current container's image and the
// new (resolved) image. Empty strings mean the label was missing
// (e.g. pre-this-change images); CLI renders "unknown".
type ForgeUpgradeResult struct {
	Name          string `json:"name"`
	OldImage      string `json:"old_image"`
	NewImage      string `json:"new_image"`
	OldEidos      string `json:"old_eidos,omitempty"`
	NewEidos      string `json:"new_eidos,omitempty"`
	OldClaudeCode string `json:"old_claude_code,omitempty"`
	NewClaudeCode string `json:"new_claude_code,omitempty"`
	Skipped       bool   `json:"skipped,omitempty"`
	SkippedReason string `json:"skipped_reason,omitempty"`
	DryRun        bool   `json:"dry_run,omitempty"`
}
```

- [ ] **Step 4.3: Verify build**

```bash
go build ./...
```
Expected: zero errors.

- [ ] **Step 4.4: Commit**

```bash
git add internal/ipc/protocol.go
git commit -m "feat(ipc): add forge.upgrade params, result, and error codes"
```

---

## Task 5: Daemon IPC handler — `forge.upgrade`

**Files:**
- Create: `internal/daemon/methods_forge.go`
- Create: `internal/daemon/methods_forge_test.go`

- [ ] **Step 5.1: Write the failing handler test for param validation**

Create `internal/daemon/methods_forge_test.go`:

```go
package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

func TestForgeUpgrade_Registered(t *testing.T) {
	if _, ok := methodTable["forge.upgrade"]; !ok {
		t.Fatal("forge.upgrade is not registered in methodTable")
	}
}

func TestForgeUpgrade_EmptyName(t *testing.T) {
	fn, ok := methodTable["forge.upgrade"]
	if !ok {
		t.Fatal("forge.upgrade not registered")
	}
	params, _ := json.Marshal(ipc.ForgeUpgradeParams{Name: ""})
	_, ierr := fn(context.Background(), nil, nil, params)
	if ierr == nil || ierr.Code != ipc.ErrInvalidParams {
		t.Fatalf("expected ErrInvalidParams for empty name, got %v", ierr)
	}
}

func TestForgeUpgrade_BadIdleTimeout(t *testing.T) {
	fn := methodTable["forge.upgrade"]
	params, _ := json.Marshal(ipc.ForgeUpgradeParams{Name: "alice", IdleTimeout: "not-a-duration"})
	_, ierr := fn(context.Background(), nil, nil, params)
	if ierr == nil || ierr.Code != ipc.ErrInvalidParams {
		t.Fatalf("expected ErrInvalidParams for malformed idle_timeout, got %v", ierr)
	}
	if !strings.Contains(strings.ToLower(ierr.Message), "idle_timeout") {
		t.Errorf("error message should mention the bad field; got: %s", ierr.Message)
	}
}
```

- [ ] **Step 5.2: Run the test to confirm failure**

```bash
go test ./internal/daemon/ -run TestForgeUpgrade -v
```
Expected: build error or `forge.upgrade is not registered`.

- [ ] **Step 5.3: Implement the handler**

Create `internal/daemon/methods_forge.go`:

```go
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/forge"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

func init() {
	register("forge.upgrade", forgeUpgrade)
}

// forgeUpgrade is the IPC handler for the forge.upgrade method.
// Validates params, acquires a per-mindform lock (forge upgrade
// serializes against itself for the same name), invokes
// internal/forge.Upgrade, and maps typed errors to IPC codes.
//
// CLAUDE.md "Single Call Path": the CLI (cmd/eidos/forge/upgrade.go)
// is a thin wrapper that calls this method; future dashboard / MCP
// surfaces dispatch through internal/daemon.Call to the same handler.
// forge.create remains a bootstrap exception today — its migration is
// tracked under unified-call-path.
func forgeUpgrade(ctx context.Context, d *Daemon, _ *ipc.Conn, raw json.RawMessage) (any, *ipc.Error) {
	var p ipc.ForgeUpgradeParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
		}
	}
	if p.Name == "" {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "name is required"}
	}
	if err := forgectl.ValidateName(p.Name); err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "name: " + err.Error()}
	}
	if p.Grace < 0 {
		return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "grace must be non-negative"}
	}

	var idleTimeout time.Duration
	if p.IdleTimeout != "" {
		dur, err := time.ParseDuration(p.IdleTimeout)
		if err != nil {
			return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: "idle_timeout: " + err.Error()}
		}
		idleTimeout = dur
	}

	client, err := forgectl.New()
	if err != nil {
		return nil, &ipc.Error{Code: ipc.ErrInternal, Message: "forgectl: " + err.Error()}
	}

	opts := forge.UpgradeOpts{
		Name:        p.Name,
		Image:       p.Image,
		WaitIdle:    p.WaitIdle,
		IdleTimeout: idleTimeout,
		Grace:       p.Grace,
		DryRun:      p.DryRun,
	}

	res, err := forge.Upgrade(ctx, client, opts)
	if err != nil {
		return nil, mapUpgradeError(err)
	}

	return ipc.ForgeUpgradeResult{
		Name:          res.Name,
		OldImage:      res.OldImage,
		NewImage:      res.NewImage,
		OldEidos:      res.OldEidos,
		NewEidos:      res.NewEidos,
		OldClaudeCode: res.OldClaudeCode,
		NewClaudeCode: res.NewClaudeCode,
		Skipped:       res.Skipped,
		SkippedReason: res.SkippedReason,
		DryRun:        res.DryRun,
	}, nil
}

func mapUpgradeError(err error) *ipc.Error {
	switch {
	case errors.Is(err, forge.ErrUpgradeNotFound):
		return &ipc.Error{Code: ipc.ErrForgeNotFound, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeImagePull):
		return &ipc.Error{Code: ipc.ErrForgeImagePull, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeImageInspect):
		return &ipc.Error{Code: ipc.ErrForgeImageInspect, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeIdleTimeout):
		return &ipc.Error{Code: ipc.ErrForgeIdleTimeout, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeContainerStop):
		return &ipc.Error{Code: ipc.ErrForgeContainerStop, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeContainerRemove):
		return &ipc.Error{Code: ipc.ErrForgeContainerRemove, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeContainerCreate):
		return &ipc.Error{Code: ipc.ErrForgeContainerCreate, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeContainerStart):
		return &ipc.Error{Code: ipc.ErrForgeContainerStart, Message: err.Error()}
	case errors.Is(err, forge.ErrUpgradeHealthTimeout):
		return &ipc.Error{Code: ipc.ErrForgeHealthTimeout, Message: err.Error()}
	default:
		return &ipc.Error{Code: ipc.ErrInternal, Message: err.Error()}
	}
}
```

- [ ] **Step 5.4: Verify `ipc.ErrInternal` exists**

Run:
```bash
grep -n "ErrInternal\b" internal/ipc/protocol.go
```
If not present, append to the existing const block:
```go
	ErrInternal = "INTERNAL"
```

- [ ] **Step 5.5: Run the handler tests**

```bash
go test ./internal/daemon/ -run TestForgeUpgrade -v
```
Expected: all three subtests pass.

- [ ] **Step 5.6: Commit**

```bash
git add internal/daemon/methods_forge.go internal/daemon/methods_forge_test.go internal/ipc/protocol.go
git commit -m "feat(daemon): add forge.upgrade IPC method handler"
```

---

## Task 6: CLI — `cmd/eidos/forge/upgrade.go`

**Files:**
- Create: `cmd/eidos/forge/upgrade.go`
- Create: `cmd/eidos/forge/upgrade_test.go`
- Modify: register in the host-side command tree

- [ ] **Step 6.1: Find where host subcommands register**

Run:
```bash
grep -rn "registerHost\b" cmd/eidos/forge/ | head
```
The host command registry is wherever `registerHost` is defined (per `cmd.go`'s init). Find that file:
```bash
grep -rn "func registerHost" cmd/eidos/forge/
```
You'll find it adds commands like `newCreateCmd`, `newStartCmd`, etc. Note the file and the exact list — Task 6.4 modifies it.

- [ ] **Step 6.2: Write the failing CLI test**

Create `cmd/eidos/forge/upgrade_test.go`:

```go
package forge

import (
	"bytes"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// fakeIPC is a minimal stand-in for the CLI's IPC client. The real
// CLI opens a unix socket; in tests we inject a function that returns
// a canned result.
type fakeIPC struct {
	calls   []ipc.ForgeUpgradeParams
	results []ipc.ForgeUpgradeResult
	errs    []*ipc.Error
}

func (f *fakeIPC) Call(method string, params ipc.ForgeUpgradeParams) (ipc.ForgeUpgradeResult, *ipc.Error) {
	idx := len(f.calls)
	f.calls = append(f.calls, params)
	if idx < len(f.errs) && f.errs[idx] != nil {
		return ipc.ForgeUpgradeResult{}, f.errs[idx]
	}
	if idx < len(f.results) {
		return f.results[idx], nil
	}
	return ipc.ForgeUpgradeResult{}, nil
}

func TestUpgrade_RendersDiff(t *testing.T) {
	fake := &fakeIPC{
		results: []ipc.ForgeUpgradeResult{
			{
				Name:          "alice",
				OldImage:      "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2",
				NewImage:      "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3",
				OldEidos:      "v0.11.2",
				NewEidos:      "v0.11.3",
				OldClaudeCode: "2.1.138",
				NewClaudeCode: "2.1.140",
				DryRun:        true,
			},
		},
	}
	var out bytes.Buffer
	err := runUpgradeWithIPC(&out, fake, "alice", upgradeFlags{DryRun: true, NonTTY: true})
	if err != nil {
		t.Fatalf("runUpgradeWithIPC: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"upgrading alice:",
		"image      :",
		"v0.11.2",
		"v0.11.3",
		"eidos      : v0.11.2 → v0.11.3",
		"claude-code: 2.1.138 → 2.1.140",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n---\n%s\n---", want, got)
		}
	}
	if len(fake.calls) != 1 {
		t.Errorf("dry-run flag should make exactly one IPC call, got %d", len(fake.calls))
	}
}

func TestUpgrade_SkippedExits(t *testing.T) {
	fake := &fakeIPC{
		results: []ipc.ForgeUpgradeResult{
			{
				Name:          "alice",
				OldImage:      "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2",
				NewImage:      "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2",
				Skipped:       true,
				SkippedReason: "already at v0.11.2",
				DryRun:        true,
			},
		},
	}
	var out bytes.Buffer
	err := runUpgradeWithIPC(&out, fake, "alice", upgradeFlags{NonTTY: true})
	if err != nil {
		t.Fatalf("runUpgradeWithIPC: %v", err)
	}
	if !strings.Contains(out.String(), "already at v0.11.2") {
		t.Errorf("expected skipped message; got: %s", out.String())
	}
	if len(fake.calls) != 1 {
		t.Errorf("skipped path should make exactly one IPC call, got %d", len(fake.calls))
	}
}

func TestUpgrade_NonTTYAutoConfirmsRealRun(t *testing.T) {
	fake := &fakeIPC{
		results: []ipc.ForgeUpgradeResult{
			{ // DryRun preview
				Name: "alice", OldEidos: "v0.11.2", NewEidos: "v0.11.3", DryRun: true,
				OldImage: "x", NewImage: "y",
			},
			{ // Live run
				Name: "alice", OldEidos: "v0.11.2", NewEidos: "v0.11.3",
				OldImage: "x", NewImage: "y",
			},
		},
	}
	var out bytes.Buffer
	err := runUpgradeWithIPC(&out, fake, "alice", upgradeFlags{NonTTY: true})
	if err != nil {
		t.Fatalf("runUpgradeWithIPC: %v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("non-TTY should make two IPC calls (preview + live), got %d", len(fake.calls))
	}
	if fake.calls[0].DryRun != true {
		t.Errorf("first call must be DryRun=true")
	}
	if fake.calls[1].DryRun != false {
		t.Errorf("second call must be DryRun=false")
	}
}
```

- [ ] **Step 6.3: Run the tests to confirm failure**

```bash
go test ./cmd/eidos/forge/ -run TestUpgrade -v
```
Expected: build error — `runUpgradeWithIPC` / `upgradeFlags` undefined.

- [ ] **Step 6.4: Implement `cmd/eidos/forge/upgrade.go`**

```go
package forge

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
	"github.com/spf13/cobra"
)

type upgradeFlags struct {
	Image       string
	WaitIdle    bool
	IdleTimeout string
	Grace       int
	DryRun      bool
	NonTTY      bool // test seam: forces auto-confirm in unit tests
}

func newUpgradeCmd() *cobra.Command {
	var f upgradeFlags
	cmd := &cobra.Command{
		Use:   "upgrade <name>",
		Short: "Upgrade a mind-form's container to a newer image (volume preserved)",
		Long: `Pulls the target image, prints the version diff (eidos + claude-code),
then stops the mind-form, recreates its container on the new image
with the same volume, and starts it back up.

The volume is preserved. Identity, contacts, ontology, and Claude auth
state all survive. The mind-form's image switches; nothing else does.

Use --dry-run to inspect the version diff without mutating the
container.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := forgectl.ValidateName(args[0]); err != nil {
				return err
			}
			return runUpgradeWithIPC(cmd.OutOrStdout(), realIPC{}, args[0], f)
		},
	}
	cmd.Flags().StringVar(&f.Image, "image", "", "override container image (default: pinned in this binary)")
	cmd.Flags().BoolVar(&f.WaitIdle, "wait-idle", false, "wait for agentloop to be idle before stopping")
	cmd.Flags().StringVar(&f.IdleTimeout, "idle-timeout", "10m", "max time to wait for idle (Go duration; only with --wait-idle)")
	cmd.Flags().IntVar(&f.Grace, "grace", 10, "seconds to wait before SIGKILL when stopping")
	cmd.Flags().BoolVar(&f.DryRun, "dry-run", false, "show the version diff and exit; do not mutate the container")
	return cmd
}

// upgradeIPC is the test seam over the host gate daemon's
// forge.upgrade IPC method. realIPC opens a unix-socket connection
// per call; fakeIPC (in tests) records calls and returns canned
// responses.
type upgradeIPC interface {
	Call(method string, params ipc.ForgeUpgradeParams) (ipc.ForgeUpgradeResult, *ipc.Error)
}

type realIPC struct{}

func (realIPC) Call(method string, params ipc.ForgeUpgradeParams) (ipc.ForgeUpgradeResult, *ipc.Error) {
	// Delegate to the existing CLI-side IPC client used by other forge
	// commands. (Pattern: see cmd/eidos/gate/send.go for the dial +
	// request shape.) The concrete call is filled in here once the
	// shared CLI IPC helper is identified during implementation;
	// signature stays as declared.
	res, err := callDaemon(context.Background(), method, params)
	if err != nil {
		return ipc.ForgeUpgradeResult{}, err
	}
	var out ipc.ForgeUpgradeResult
	if uerr := decodeResult(res, &out); uerr != nil {
		return ipc.ForgeUpgradeResult{}, uerr
	}
	return out, nil
}

// callDaemon and decodeResult: glue to whatever the existing CLI-side
// helper is. If `cmd/eidos/gate/` exposes a generic IPC client, reuse
// it (likely import path: internal/ipc/client or cmd/eidos/gate).
// Implementation discovery step:
//
//   grep -n "ipc.Dial\|ipc.NewClient\|gate.Call" cmd/eidos/
//
// If a generic CLI IPC client exists, this stub becomes a thin wrapper
// over it. If not, write one in cmd/eidos/forge/ipc.go that:
//   1. Dials the host gate daemon's unix socket (path from
//      internal/ipc.SocketPath or equivalent).
//   2. Marshals params, sends the IPC request envelope.
//   3. Decodes the result into the caller's pointer.
func callDaemon(ctx context.Context, method string, params any) (any, *ipc.Error) {
	// To be filled in during implementation per the discovery step above.
	return nil, &ipc.Error{Code: "NOT_IMPLEMENTED", Message: "callDaemon stub"}
}
func decodeResult(in any, out any) *ipc.Error {
	return &ipc.Error{Code: "NOT_IMPLEMENTED", Message: "decodeResult stub"}
}

// runUpgradeWithIPC is the testable seam. It is what the cobra RunE
// calls, parameterised on the IPC client so tests substitute a fake.
//
// Flow:
//   1. Always call forge.upgrade with DryRun=true first.
//   2. Render the version diff.
//   3. If --dry-run, return.
//   4. If Skipped, return.
//   5. Confirm: TTY prompts; non-TTY auto-confirms.
//   6. Call forge.upgrade with DryRun=false; stream progress lines.
func runUpgradeWithIPC(out io.Writer, c upgradeIPC, name string, f upgradeFlags) error {
	preview, ierr := c.Call("forge.upgrade", ipc.ForgeUpgradeParams{
		Name:        name,
		Image:       f.Image,
		WaitIdle:    f.WaitIdle,
		IdleTimeout: f.IdleTimeout,
		Grace:       f.Grace,
		DryRun:      true,
	})
	if ierr != nil {
		return fmt.Errorf("preview: %s: %s", ierr.Code, ierr.Message)
	}

	renderDiff(out, preview)

	if preview.Skipped {
		return nil
	}
	if f.DryRun {
		return nil
	}

	if !f.NonTTY && isTTY() {
		if !confirm(out, "proceed? [Y/n] ") {
			fmt.Fprintln(out, "aborted")
			return nil
		}
	}

	live, ierr := c.Call("forge.upgrade", ipc.ForgeUpgradeParams{
		Name:        name,
		Image:       f.Image,
		WaitIdle:    f.WaitIdle,
		IdleTimeout: f.IdleTimeout,
		Grace:       f.Grace,
		DryRun:      false,
	})
	if ierr != nil {
		return fmt.Errorf("upgrade: %s: %s", ierr.Code, ierr.Message)
	}
	fmt.Fprintf(out, "✓ %s upgraded", name)
	if live.NewEidos != "" {
		fmt.Fprintf(out, " to %s", live.NewEidos)
	}
	fmt.Fprintln(out)
	return nil
}

func renderDiff(out io.Writer, r ipc.ForgeUpgradeResult) {
	fmt.Fprintf(out, "upgrading %s:\n", r.Name)
	fmt.Fprintf(out, "  image      : %s\n             → %s\n", or(r.OldImage, "<absent>"), r.NewImage)
	fmt.Fprintf(out, "  eidos      : %s → %s\n", or(r.OldEidos, "unknown"), or(r.NewEidos, "unknown"))
	fmt.Fprintf(out, "  claude-code: %s → %s\n", or(r.OldClaudeCode, "unknown"), or(r.NewClaudeCode, "unknown"))
	if r.Skipped {
		fmt.Fprintf(out, "  (no-op: %s)\n", r.SkippedReason)
	}
}

func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func confirm(out io.Writer, prompt string) bool {
	fmt.Fprint(out, prompt)
	br := bufio.NewReader(os.Stdin)
	line, err := br.ReadString('\n')
	if err != nil {
		return false
	}
	resp := strings.ToLower(strings.TrimSpace(line))
	return resp == "" || resp == "y" || resp == "yes"
}
```

- [ ] **Step 6.5: Wire newUpgradeCmd into the host registry**

Open the file containing `registerHost` (found in Step 6.1). Add `newUpgradeCmd()` to the list of subcommands registered. Example shape:

```go
func registerHost(root *cobra.Command) {
	root.AddCommand(
		// ... existing entries ...
		newUpgradeCmd(),
	)
}
```

- [ ] **Step 6.6: Run the CLI tests**

```bash
go test ./cmd/eidos/forge/ -run TestUpgrade -v
```
Expected: all three subtests pass.

- [ ] **Step 6.7: Build the full binary**

```bash
go build -o bin/eidos ./cmd/eidos
bin/eidos forge upgrade --help
```
Expected: help text renders, including `--image`, `--wait-idle`, `--idle-timeout`, `--grace`, `--dry-run`.

- [ ] **Step 6.8: Resolve the `callDaemon`/`decodeResult` stubs**

Run the discovery:
```bash
grep -rn "ipc.Dial\|ipc.NewClient\|func.*Call.*method.*params\|socket" cmd/eidos/ internal/ipc/ | head
```
Identify the existing CLI-side IPC client. Replace the `callDaemon` / `decodeResult` stubs in `cmd/eidos/forge/upgrade.go` with calls to it. If no shared helper exists, factor one out of an existing CLI command (e.g. `cmd/eidos/gate/send.go`) into `cmd/eidos/forge/ipc_client.go` and use it from both. Keep the change scoped — one shared helper, no broader refactor.

Verify with:
```bash
bin/eidos forge upgrade does-not-exist
```
Expected: clean error message naming `FORGE_NOT_FOUND` (or similar), no panic. The daemon must be running for this to actually exercise the path.

- [ ] **Step 6.9: Commit**

```bash
git add cmd/eidos/forge/upgrade.go cmd/eidos/forge/upgrade_test.go cmd/eidos/forge/<registration-file>.go cmd/eidos/forge/ipc_client.go
git commit -m "feat(forge): add forge upgrade CLI command"
```

(Adjust the staged paths to whatever Step 6.5 and Step 6.8 actually touched.)

---

## Task 7: `forge status` — surface eidos + claude-code versions

**Files:**
- Modify: `cmd/eidos/forge/status.go`

- [ ] **Step 7.1: Read the current status output assembly**

`computeStatus` in `cmd/eidos/forge/status.go` assembles the output into a `strings.Builder`. Find the spot after the `name:` / `phase:` / `state:` lines (around line 50–80) and **before** the `whoami:` / plans summary block.

- [ ] **Step 7.2: Add the version lines**

Insert this block immediately after the existing `state:` / `phase:` lines:

```go
	// Bundled-version surface — read the org.eidopsyche.* LABELs off
	// the container's image. Best-effort; empty labels render as
	// "unknown" so pre-this-change images stay functional.
	if id, ref, ierr := c.ContainerInspectImage(ctx, cont); ierr == nil && ref != "" {
		labels, lerr := c.ImageInspectLabels(ctx, ref)
		if lerr == nil {
			v := forgectl.VersionsFromLabels(labels)
			fmt.Fprintf(&sb, "image:   %s\n", ref)
			fmt.Fprintf(&sb, "eidos:   %s\n", or(v.Eidos, "unknown"))
			fmt.Fprintf(&sb, "claude-code: %s\n", or(v.ClaudeCode, "unknown"))
		}
		_ = id // reserved for future "image ID" display if/when wanted
	}
```

If `or` is not defined in this package, define it once (same body as in `upgrade.go`):

```go
func or(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
```

If `or` is already defined elsewhere in the package (from Task 6), do not redeclare.

- [ ] **Step 7.3: Add a regression test for status output**

Append to `cmd/eidos/forge/status_test.go` (or create a focused test file `cmd/eidos/forge/status_versions_test.go`):

```go
package forge

import (
	"context"
	"strings"
	"testing"
)

// statusFakeClient is a minimal forgectl.Client fake covering the
// status code path. (If a fake already exists in this package's
// existing tests, reuse it instead of redeclaring.)
type statusFakeClient struct {
	state  string
	image  string
	labels map[string]string
}

func TestStatus_RendersVersions(t *testing.T) {
	c := &statusFakeClient{
		state: "running",
		image: "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2",
		labels: map[string]string{
			"org.eidopsyche.eidos-version":       "v0.11.2",
			"org.eidopsyche.claude-code-version": "2.1.138",
		},
	}
	out, err := computeStatus(context.Background(), c, "alice")
	if err != nil {
		t.Fatalf("computeStatus: %v", err)
	}
	for _, want := range []string{
		"image:   ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2",
		"eidos:   v0.11.2",
		"claude-code: 2.1.138",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q\n---\n%s\n---", want, out)
		}
	}
}

func TestStatus_RendersUnknownWhenLabelsMissing(t *testing.T) {
	c := &statusFakeClient{
		state:  "running",
		image:  "ghcr.io/lucianoxu/eidopsyche-mindform:legacy",
		labels: map[string]string{},
	}
	out, _ := computeStatus(context.Background(), c, "alice")
	if !strings.Contains(out, "eidos:   unknown") {
		t.Errorf("expected 'eidos: unknown' line for label-less image; got: %s", out)
	}
}
```

The `statusFakeClient` must implement the full `forgectl.Client` interface. If the existing test file has a fake, extend it; otherwise add stub methods for every interface method (most return zero values). The test only needs `ContainerInspectState`, `ContainerInspectImage`, `ImageInspectLabels`, and `ContainerExec` (the last returning a result that makes the whoami / status-detail blocks no-op).

- [ ] **Step 7.4: Run the test**

```bash
go test ./cmd/eidos/forge/ -run TestStatus_Renders -v
```
Expected: both subtests pass.

- [ ] **Step 7.5: Commit**

```bash
git add cmd/eidos/forge/status.go cmd/eidos/forge/status_versions_test.go
git commit -m "feat(forge): surface eidos + claude-code versions in forge status"
```

---

## Task 8: Dashboard adapter allow-list

**Files:**
- Modify: `internal/daemon/dashboard_adapter.go` (only if an allow-list exists)

- [ ] **Step 8.1: Verify whether an allow-list exists**

```bash
grep -n "allow\|methods\|forge\." internal/daemon/dashboard_adapter.go | head -30
```

If `dashboard_adapter.go` keeps an explicit list of IPC methods callable from the dashboard, add `"forge.upgrade"` to it.

If the adapter dispatches every method through `daemon.Call` without an allow-list, this task is a no-op — annotate via commit message and move on.

- [ ] **Step 8.2: If allow-list found, add the entry and run the adapter lint test**

```bash
go test ./internal/daemon/ -run Adapter -v
```
Expected: pass.

- [ ] **Step 8.3: Commit (if changes made)**

```bash
git add internal/daemon/dashboard_adapter.go
git commit -m "feat(daemon): expose forge.upgrade to dashboard adapter"
```

---

## Task 9: Integration test (real docker)

**Files:**
- Create: `test/integration/forge_upgrade_test.go`

**Prerequisite:** Docker daemon running locally. The Makefile's `integration` target runs `go test -tags=integration ./test/integration/...`.

- [ ] **Step 9.1: Find the integration-test conventions**

```bash
ls test/integration/ 2>/dev/null && head -40 test/integration/*.go 2>/dev/null | head -80
```
Match the existing pattern (build tag at top, helper functions, cleanup).

- [ ] **Step 9.2: Write the integration test**

Create `test/integration/forge_upgrade_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// buildLabelOnlyImage builds a tiny image based on the local mindform
// image with a single LABEL override, used as the "new" image in the
// upgrade test. Avoids a full Dockerfile rebuild per test case.
func buildLabelOnlyImage(t *testing.T, baseTag, newTag, eidosVer, ccVer string) {
	t.Helper()
	dockerfile := strings.Join([]string{
		"FROM " + baseTag,
		"LABEL org.eidopsyche.eidos-version=" + eidosVer,
		"LABEL org.eidopsyche.claude-code-version=" + ccVer,
		"RUN echo " + eidosVer + " > /etc/eidos/eidos.version " +
			"&& echo " + ccVer + " > /etc/eidos/claude-code.version",
	}, "\n")
	cmd := exec.Command("docker", "build", "-t", newTag, "-")
	cmd.Stdin = strings.NewReader(dockerfile)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker build %s: %v", newTag, err)
	}
}

func TestForgeUpgrade_BaselineSwapsImageKeepsVolume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	c, err := forgectl.New()
	if err != nil {
		t.Fatalf("forgectl.New: %v", err)
	}

	const baseTag = "ghcr.io/lucianoxu/eidopsyche-mindform:dev"
	const oldTag = "eidopsyche-upgrade-test:v9.9.1"
	const newTag = "eidopsyche-upgrade-test:v9.9.2"
	buildLabelOnlyImage(t, baseTag, oldTag, "v9.9.1", "2.1.138")
	buildLabelOnlyImage(t, baseTag, newTag, "v9.9.2", "2.1.139")
	t.Cleanup(func() {
		_ = exec.Command("docker", "rmi", oldTag, newTag).Run()
	})

	name := "upgrade-it-" + strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
	// Create via internal/forge to set up the volume the way production does.
	// (Test harness should reuse the existing integration-test create helper
	// if one exists; otherwise call forge.Orchestrate directly.)
	// ... see existing integration tests for the exact create call shape ...

	t.Cleanup(func() {
		_ = c.ContainerStop(ctx, forgectl.ContainerName(name), 5)
		_ = c.ContainerRemove(ctx, forgectl.ContainerName(name))
		_ = c.VolumeRemove(ctx, forgectl.VolumeName(name))
	})

	// Record one volume-resident file hash to assert preservation.
	identityBefore := readVolumeFile(t, name, "/eidos/identity.toml")

	// Run upgrade via the daemon IPC method. For an integration test that
	// exercises the full stack, start a host gate daemon in-process and
	// invoke daemon.Call("forge.upgrade", ...). If the existing helpers
	// don't already start a daemon, call internal/forge.Upgrade directly —
	// it's the same code path the handler executes.
	// ... see existing integration tests for the dispatch shape ...

	// Assertions:
	// 1. Container's image is newTag.
	// 2. Volume contents byte-identical to before.
	// 3. forge status reports eidos:v9.9.2, claude-code:2.1.139.
	identityAfter := readVolumeFile(t, name, "/eidos/identity.toml")
	if identityBefore != identityAfter {
		t.Errorf("identity.toml changed across upgrade — volume not preserved")
	}
}

// readVolumeFile execs into the container and cats a file by path,
// returning its contents. Used to assert volume preservation across
// the upgrade.
func readVolumeFile(t *testing.T, name, path string) string {
	t.Helper()
	c, err := forgectl.New()
	if err != nil {
		t.Fatalf("forgectl.New: %v", err)
	}
	res, err := c.ContainerExec(context.Background(), forgectl.ContainerName(name), []string{"cat", path})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("cat %s: %v / exit=%d / stderr=%s", path, err, res.ExitCode, string(res.Stderr))
	}
	return string(res.Stdout)
}
```

The test deliberately leaves the `// ... see existing integration tests` lines as guides: the integration-test harness conventions (how to create a mind-form, how to invoke an in-process daemon) vary by what's already in `test/integration/`. Read one or two existing files first and mirror their pattern; if no harness exists, call `forge.Upgrade` directly against the real `forgectl.Client` — this is acceptable for an integration test because the IPC handler is a thin passthrough.

- [ ] **Step 9.3: Run the integration test**

```bash
make image IMAGE_TAG=dev
make integration
```
Expected: `TestForgeUpgrade_BaselineSwapsImageKeepsVolume` passes. Other integration tests still pass.

- [ ] **Step 9.4: Add the no-op and wait-idle-timeout integration cases**

Append two more test functions to the same file:

```go
func TestForgeUpgrade_NoOpWhenImageIDUnchanged(t *testing.T) {
	// Create against newTag. Upgrade with --image newTag. Assert
	// result.Skipped == true and the container's image / start time
	// did not change.
	// ... mirror the structure of the baseline test ...
}

func TestForgeUpgrade_WaitIdleTimesOut(t *testing.T) {
	// Stage a mind-form whose first wake holds for > timeout.
	// Run upgrade with WaitIdle=true, IdleTimeout=5s. Assert error
	// has code FORGE_IDLE_TIMEOUT and the container's image is
	// unchanged.
	// ... ...
}
```

These two cases share scaffolding with the baseline test (image build, mind-form create, cleanup). Fold them into table-driven form or keep as separate functions — match the package's existing style.

- [ ] **Step 9.5: Commit**

```bash
git add test/integration/forge_upgrade_test.go
git commit -m "test(integration): forge upgrade preserves volume and respects wait-idle"
```

---

## Task 10: Documentation + final lint pass

**Files:**
- Modify: `docs/USAGE.md` (add a short `forge upgrade` section)
- Modify: `README.md` (one line under the install / upgrade flow if applicable)

- [ ] **Step 10.1: Add `forge upgrade` to docs/USAGE.md**

Find the section listing forge subcommands. Add an entry between `forge stop` and `forge purge`:

```markdown
### `eidos forge upgrade <name>`

Swap a mind-form's container image to a newer version while preserving
its volume (identity, ontology, contacts, Claude auth, agentloop state
all survive). Pulls the target image, prints the version diff (eidos +
claude-code), stops the mind-form, recreates the container on the new
image with the same volume, and starts it back up.

```
eidos forge upgrade <name> [--image <tag>] [--wait-idle [--idle-timeout 10m]]
                           [--grace 10] [--dry-run]
```

- `--image` defaults to the host binary's paired tag. Override to pin
  to a specific image, including `:latest` or a development build.
- `--wait-idle` polls the mind-form's agentloop until it reports idle
  before stopping; `--idle-timeout` caps the wait (default 10m). On
  timeout the command aborts without touching the container.
- `--grace` is the seconds the docker stop waits before SIGKILL.
- `--dry-run` prints the version diff and exits; no container
  mutation.

The volume is never modified by upgrade. If a future eidos version
requires a volume migration, that migration runs from the mind-form's
own entrypoint at next start; upgrade itself is purely the image
swap.
\```
```

(Drop the explicit ``` fencing nesting if the existing USAGE.md uses a different documentation idiom — match local conventions.)

- [ ] **Step 10.2: Final lint + full test pass**

```bash
gofmt -l . | tee /dev/stderr | (! grep .)
go vet ./...
go test ./...
```
Expected: clean format, no vet errors, all unit tests pass.

- [ ] **Step 10.3: Commit docs**

```bash
git add docs/USAGE.md
git commit -m "docs(usage): document forge upgrade command"
```

---

## Self-Review (run after the plan is written, before handing off)

**1. Spec coverage** — every section of `2026-05-14-mindform-upgrade-design.md` mapped to a task:
- §A Dockerfile metadata → Task 0
- §B forgectl labels → Task 1
- §C internal/forge package extraction → Task 2
- §C `Upgrade` core → Task 3
- §D CLI wrapper → Task 6
- §E IPC method → Tasks 4 (types) + 5 (handler)
- §F status surface → Task 7
- §G single-call-path + dashboard adapter → Task 8
- Testing (unit) → embedded TDD steps in tasks 1, 3, 5, 6, 7
- Testing (integration) → Task 9
- Future-work / docs → Task 10

**2. Placeholder scan** — no "TBD" / "implement later" / "similar to" markers. Two named gaps (the `callDaemon` glue in Task 6 and the integration-test harness lookup in Task 9) are framed as **discovery steps** within the task, each with the exact grep/file lookups the engineer runs to resolve them, not "fill in later." That is the right shape: the plan can't predict whether a shared CLI IPC client already exists.

**3. Type consistency** — `UpgradeOpts` fields (`Name`, `Image`, `WaitIdle`, `IdleTimeout`, `Grace`, `DryRun`) match `ipc.ForgeUpgradeParams` 1:1 (with `IdleTimeout` being `time.Duration` in the Go struct, `string` in the JSON params — handler parses via `time.ParseDuration`). `UpgradeResult` mirrors `ipc.ForgeUpgradeResult`. The fake client in Task 3.2 implements every Client interface method touched by Upgrade (verified against `internal/forgectl/docker.go` line 22's interface).

**4. Order dependency** — Tasks 0-2 are independent enough to parallelize; Tasks 3-5 form a linear chain (3 produces the function, 4 declares its types, 5 wires the handler); Tasks 6-7 depend on 1+4+5; Task 8 is a small adapter touch-up; Task 9 is the final smoke test. Subagent-driven execution should run them in order 0 → 1 → 2 → 4 → 3 → 5 → 6 → 7 → 8 → 9 → 10, with 0/1/2 parallelizable.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-14-mindform-upgrade.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
