//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/forge"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// requireBaseImage skips the test if the base mind-form image is not
// present locally. Returns the base tag on success.
func requireBaseImage(t *testing.T) string {
	t.Helper()
	const baseTag = "ghcr.io/lucianoxu/eidopsyche-mindform:dev"
	cmd := exec.Command("docker", "image", "inspect", baseTag)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		t.Skipf("base image %s not present locally (run 'make image IMAGE_TAG=dev' first)", baseTag)
	}
	return baseTag
}

// buildLabelOnlyImage builds a minimal derivative image on top of baseTag
// that overrides the org.eidopsyche.* LABELs and the version files under
// /etc/eidos/. The resulting image is tagged newTag and cleaned up via
// t.Cleanup.
func buildLabelOnlyImage(t *testing.T, baseTag, newTag, eidosVer, ccVer string) {
	t.Helper()
	dockerfile := strings.Join([]string{
		"FROM " + baseTag,
		"USER 0:0",
		fmt.Sprintf("RUN mkdir -p /etc/eidos && echo %s > /etc/eidos/eidos.version && echo %s > /etc/eidos/claude-code.version", eidosVer, ccVer),
		"USER 1000:1000",
		fmt.Sprintf("LABEL org.eidopsyche.eidos-version=%s", eidosVer),
		fmt.Sprintf("LABEL org.eidopsyche.claude-code-version=%s", ccVer),
	}, "\n")
	cmd := exec.Command("docker", "build", "-t", newTag, "-")
	cmd.Stdin = strings.NewReader(dockerfile)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker build %s: %v", newTag, err)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rmi", "-f", newTag).Run()
	})
}

// uniqueName returns a collision-resistant name for a test mind-form.
func uniqueName(prefix string) string {
	return prefix + "-" + strings.ReplaceAll(time.Now().Format("150405.000"), ".", "")
}

// createMindForm calls forge.Orchestrate to create a stopped mind-form
// using the given image tag, and registers teardown via t.Cleanup.
func createMindForm(t *testing.T, c forgectl.Client, name, image string) {
	t.Helper()
	if err := forge.Orchestrate(context.Background(), c, name, forge.CreateOpts{
		Label:             name,
		Owner:             "npub1w7syejupdm7alpyt9mz9kke283z946qw7wr84hyvd5j0xl6vq95q9x0rnd",
		OwnerLabel:        "tester",
		MindFormNpub:      "npub1w7syejupdm7alpyt9mz9kke283z946qw7wr84hyvd5j0xl6vq95q9x0rnd",
		Relay:             "wss://relay.example",
		Model:             "claude-sonnet-4-5",
		HeartbeatInterval: "1m",
		Image:             image,
		NoLogin:           true,
	}); err != nil {
		t.Fatalf("Orchestrate %s on %s: %v", name, image, err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_ = c.ContainerStop(ctx, forgectl.ContainerName(name), 5)
		_ = c.ContainerRemove(ctx, forgectl.ContainerName(name))
		_ = c.VolumeRemove(ctx, forgectl.VolumeName(name))
	})
}

// sha256OfVolumeFile reads a file from the running container via
// docker exec and returns its SHA-256 hex digest.
func sha256OfVolumeFile(t *testing.T, c forgectl.Client, name, path string) string {
	t.Helper()
	res, err := c.ContainerExec(context.Background(), forgectl.ContainerName(name), []string{"cat", path})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("cat %s in %s: %v / exit=%d / stderr=%s", path, name, err, res.ExitCode, string(res.Stderr))
	}
	sum := sha256.Sum256(res.Stdout)
	return hex.EncodeToString(sum[:])
}

// TestForgeUpgrade_BaselineSwapsImageKeepsVolume verifies the happy path:
//   - Container image ref switches from v9.9.1 → v9.9.2.
//   - Volume contents (identity.toml) are byte-preserved across upgrade.
//   - UpgradeResult carries the correct old/new version strings.
func TestForgeUpgrade_BaselineSwapsImageKeepsVolume(t *testing.T) {
	base := requireBaseImage(t)
	const oldTag = "eidopsyche-upgrade-it:v9.9.1"
	const newTag = "eidopsyche-upgrade-it:v9.9.2"
	buildLabelOnlyImage(t, base, oldTag, "v9.9.1", "2.1.138")
	buildLabelOnlyImage(t, base, newTag, "v9.9.2", "2.1.139")

	c, err := forgectl.New()
	if err != nil {
		t.Fatalf("forgectl.New: %v", err)
	}

	name := uniqueName("upgrade-baseline")
	createMindForm(t, c, name, oldTag)

	// Start so the volume is fully populated (init-volume runs during
	// Orchestrate but the container must be running for ContainerExec).
	if err := c.ContainerStart(context.Background(), forgectl.ContainerName(name)); err != nil {
		t.Fatalf("ContainerStart: %v", err)
	}
	// Give the container a moment to settle before reading from the volume.
	time.Sleep(2 * time.Second)

	before := sha256OfVolumeFile(t, c, name, "/eidos/ontology/self/identity.toml")

	res, err := forge.Upgrade(context.Background(), c, forge.UpgradeOpts{
		Name:  name,
		Image: newTag,
		Grace: 5,
	})
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if res.Skipped {
		t.Fatal("expected Skipped=false on a real version bump")
	}
	if res.OldEidos != "v9.9.1" || res.NewEidos != "v9.9.2" {
		t.Errorf("eidos version diff: OldEidos=%q NewEidos=%q (want v9.9.1→v9.9.2)", res.OldEidos, res.NewEidos)
	}
	if res.OldClaudeCode != "2.1.138" || res.NewClaudeCode != "2.1.139" {
		t.Errorf("claude-code diff: OldClaudeCode=%q NewClaudeCode=%q (want 2.1.138→2.1.139)", res.OldClaudeCode, res.NewClaudeCode)
	}

	// Volume preservation: identity.toml must be unchanged.
	after := sha256OfVolumeFile(t, c, name, "/eidos/ontology/self/identity.toml")
	if before != after {
		t.Errorf("identity.toml changed across upgrade — volume not preserved\n  before=%s\n  after =%s", before, after)
	}

	// Container image ref must now point at the new tag.
	_, ref, err := c.ContainerInspectImage(context.Background(), forgectl.ContainerName(name))
	if err != nil {
		t.Fatalf("ContainerInspectImage: %v", err)
	}
	if ref != newTag {
		t.Errorf("container image not switched: got %q want %q", ref, newTag)
	}
}

// TestForgeUpgrade_NoOpWhenImageIDUnchanged verifies that upgrading to
// the same image tag returns Skipped=true and leaves the container
// image ref untouched.
func TestForgeUpgrade_NoOpWhenImageIDUnchanged(t *testing.T) {
	base := requireBaseImage(t)
	const tag = "eidopsyche-upgrade-it:v9.9.3"
	buildLabelOnlyImage(t, base, tag, "v9.9.3", "2.1.140")

	c, err := forgectl.New()
	if err != nil {
		t.Fatalf("forgectl.New: %v", err)
	}

	name := uniqueName("upgrade-noop")
	createMindForm(t, c, name, tag)

	// Container is stopped; Upgrade should still detect same image ID.
	res, err := forge.Upgrade(context.Background(), c, forge.UpgradeOpts{
		Name:  name,
		Image: tag,
	})
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if !res.Skipped {
		t.Error("expected Skipped=true when image IDs match")
	}

	// Container image ref must remain the original tag.
	_, ref, err := c.ContainerInspectImage(context.Background(), forgectl.ContainerName(name))
	if err != nil {
		t.Fatalf("ContainerInspectImage: %v", err)
	}
	if ref != tag {
		t.Errorf("container image changed unexpectedly: got %q want %q", ref, tag)
	}
}

// TestForgeUpgrade_WaitIdleTimesOut verifies that when WaitIdle=true
// and the agentloop never reports an idle phase within IdleTimeout,
// Upgrade returns ErrUpgradeIdleTimeout without mutating the container.
//
// The container is deliberately left stopped. ContainerExec cannot exec
// into a stopped container, so every wait-idle probe fails immediately
// without ever observing an idle phase. This guarantees the timeout
// fires regardless of how fast the agentloop would otherwise settle.
func TestForgeUpgrade_WaitIdleTimesOut(t *testing.T) {
	base := requireBaseImage(t)
	const oldTag = "eidopsyche-upgrade-it:v9.9.4"
	const newTag = "eidopsyche-upgrade-it:v9.9.5"
	buildLabelOnlyImage(t, base, oldTag, "v9.9.4", "2.1.141")
	buildLabelOnlyImage(t, base, newTag, "v9.9.5", "2.1.142")

	c, err := forgectl.New()
	if err != nil {
		t.Fatalf("forgectl.New: %v", err)
	}

	name := uniqueName("upgrade-waitidle")
	createMindForm(t, c, name, oldTag)
	// Deliberately do NOT start the container. With the container in
	// stopped state, the wait-idle probe (ContainerExec calling
	// `eidos forge runtime-state`) fails on every poll, so waitIdle
	// never observes the idle phase and trips the IdleTimeout. This
	// is the scenario the test was originally trying to exercise — a
	// mind-form whose agentloop is unreachable within the deadline.

	_, err = forge.Upgrade(context.Background(), c, forge.UpgradeOpts{
		Name:        name,
		Image:       newTag,
		WaitIdle:    true,
		IdleTimeout: 2 * time.Second,
	})
	if !errors.Is(err, forge.ErrUpgradeIdleTimeout) {
		t.Fatalf("expected ErrUpgradeIdleTimeout, got %v", err)
	}

	// Container must remain on the original image (no mutation occurred).
	_, ref, err := c.ContainerInspectImage(context.Background(), forgectl.ContainerName(name))
	if err != nil {
		t.Fatalf("ContainerInspectImage: %v", err)
	}
	if ref != oldTag {
		t.Errorf("container image changed despite wait-idle timeout: got %q want %q", ref, oldTag)
	}
}
