package forge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// fakeUpgradeClient records every Client call and serves canned responses.
type fakeUpgradeClient struct {
	mu sync.Mutex

	containerImages  map[string]string // container name → image ref
	containerStates  map[string]string // container name → "running"/"exited"/"absent"
	imageIDs         map[string]string // image ref → image ID (sha256)
	imageLabels      map[string]map[string]string
	imageExistsLocal map[string]bool

	// started tracks whether ContainerStart has been called, used by the
	// ContainerExec stub to distinguish health-probe (post-start, returns
	// healthy idle JSON) from wait-idle probe (pre-start, returns empty
	// to simulate a busy agentloop).
	started bool

	// lastCreateMounts captures the Mounts passed to the most recent
	// ContainerCreate so tests can assert on the post-recreate mount list.
	lastCreateMounts []forgectl.Mount

	calls []string
}

func (f *fakeUpgradeClient) record(name string) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
}

func (f *fakeUpgradeClient) ContainerInspectState(ctx context.Context, name string) (string, error) {
	f.record("ContainerInspectState:" + name)
	s, ok := f.containerStates[name]
	if !ok {
		return "absent", nil
	}
	return s, nil
}

func (f *fakeUpgradeClient) ContainerInspectImage(ctx context.Context, name string) (string, string, error) {
	f.record("ContainerInspectImage:" + name)
	ref, ok := f.containerImages[name]
	if !ok {
		return "", "", nil
	}
	return f.imageIDs[ref], ref, nil
}

func (f *fakeUpgradeClient) ContainerInspectMounts(ctx context.Context, name string) ([]forgectl.Mount, error) {
	return nil, nil
}

func (f *fakeUpgradeClient) ImageExists(ctx context.Context, ref string) (bool, error) {
	f.record("ImageExists:" + ref)
	return f.imageExistsLocal[ref], nil
}

func (f *fakeUpgradeClient) ImagePull(ctx context.Context, ref string, out io.Writer) error {
	f.record("ImagePull:" + ref)
	f.imageExistsLocal[ref] = true
	return nil
}

func (f *fakeUpgradeClient) ImageInspectLabels(ctx context.Context, ref string) (map[string]string, error) {
	f.record("ImageInspectLabels:" + ref)
	return f.imageLabels[ref], nil
}

func (f *fakeUpgradeClient) ImageInspectID(ctx context.Context, ref string) (string, error) {
	f.record("ImageInspectID:" + ref)
	return f.imageIDs[ref], nil
}

func (f *fakeUpgradeClient) ContainerStop(ctx context.Context, name string, grace int) error {
	f.record(fmt.Sprintf("ContainerStop:%s:%d", name, grace))
	f.containerStates[name] = "exited"
	return nil
}

func (f *fakeUpgradeClient) ContainerRemove(ctx context.Context, name string) error {
	f.record("ContainerRemove:" + name)
	delete(f.containerStates, name)
	delete(f.containerImages, name)
	return nil
}

func (f *fakeUpgradeClient) ContainerCreate(ctx context.Context, opts forgectl.CreateOpts) error {
	f.record("ContainerCreate:" + opts.Name + ":" + opts.Image)
	f.mu.Lock()
	f.lastCreateMounts = append([]forgectl.Mount(nil), opts.Mounts...)
	f.mu.Unlock()
	f.containerStates[opts.Name] = "exited"
	f.containerImages[opts.Name] = opts.Image
	return nil
}

func (f *fakeUpgradeClient) ContainerStart(ctx context.Context, name string) error {
	f.record("ContainerStart:" + name)
	f.mu.Lock()
	f.containerStates[name] = "running"
	f.started = true
	f.mu.Unlock()
	return nil
}

// Stubs for Client methods Upgrade doesn't call but the interface requires.
func (f *fakeUpgradeClient) ContainerExists(ctx context.Context, name string) (bool, error) {
	return f.containerStates[name] != "" && f.containerStates[name] != "absent", nil
}
func (f *fakeUpgradeClient) ContainerExec(ctx context.Context, name string, cmd []string) (forgectl.ExecResult, error) {
	f.record("ContainerExec:" + name)
	f.mu.Lock()
	started := f.started
	f.mu.Unlock()
	// Health-probe path: once ContainerStart has been called the new
	// container is "live" — return a healthy idle-phase JSON so
	// waitHealthy passes immediately.
	// Wait-idle path (pre-start): return empty to simulate a busy
	// agentloop, letting WaitIdleTimeout tests expire cleanly.
	if started {
		return forgectl.ExecResult{ExitCode: 0, Stdout: []byte(`{"v":2,"phase":"idle"}`)}, nil
	}
	return forgectl.ExecResult{}, nil
}
func (f *fakeUpgradeClient) VolumeExists(ctx context.Context, name string) (bool, error) {
	return true, nil
}
func (f *fakeUpgradeClient) VolumeCreate(ctx context.Context, name string) error { return nil }
func (f *fakeUpgradeClient) VolumeRemove(ctx context.Context, name string) error { return nil }
func (f *fakeUpgradeClient) VolumeList(ctx context.Context, prefix string) ([]string, error) {
	return nil, nil
}
func (f *fakeUpgradeClient) ContainerLogs(ctx context.Context, name string, follow bool, w io.Writer) error {
	return nil
}
func (f *fakeUpgradeClient) CopyFromContainer(ctx context.Context, name, srcPath string, w io.Writer) error {
	return nil
}
func (f *fakeUpgradeClient) RunInit(ctx context.Context, opts forgectl.RunInitOpts) (forgectl.RunInitResult, error) {
	return forgectl.RunInitResult{}, nil
}

func newFakeUpgradeClient() *fakeUpgradeClient {
	return &fakeUpgradeClient{
		containerImages:  map[string]string{},
		containerStates:  map[string]string{},
		imageIDs:         map[string]string{},
		imageLabels:      map[string]map[string]string{},
		imageExistsLocal: map[string]bool{},
	}
}

// hasCall returns true if any recorded call contains the substring.
func (f *fakeUpgradeClient) hasCall(substr string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func TestIsIdlePhase(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"pretty-printed idle", "{\n  \"phase\": \"idle\"\n}", true},
		{"pretty-printed sleeping", "{\n  \"phase\": \"sleeping\",\n  \"v\": 2\n}", true},
		{"compact idle (legacy)", `{"phase":"idle"}`, true},
		{"awake", `{"phase":"awake"}`, false},
		{"in_turn", `{"phase": "in_turn"}`, false},
		{"empty", ``, false},
		{"malformed", `not json`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isIdlePhase([]byte(tc.input)); got != tc.want {
				t.Errorf("isIdlePhase(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestUpgrade_HappyPath(t *testing.T) {
	c := newFakeUpgradeClient()
	c.containerImages["eidos-mindform-alice"] = "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"
	c.containerStates["eidos-mindform-alice"] = "running"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = "sha256:aaa"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = "sha256:bbb"
	c.imageExistsLocal["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = true
	c.imageExistsLocal["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = true
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
	if !c.hasCall("ContainerStop:eidos-mindform-alice") {
		t.Error("missing ContainerStop")
	}
	if !c.hasCall("ContainerRemove:eidos-mindform-alice") {
		t.Error("missing ContainerRemove")
	}
	if !c.hasCall("ContainerCreate:eidos-mindform-alice:ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3") {
		t.Error("missing ContainerCreate with new image")
	}
	if !c.hasCall("ContainerStart:eidos-mindform-alice") {
		t.Error("missing ContainerStart")
	}
}

func TestUpgrade_DryRunDoesNotMutate(t *testing.T) {
	c := newFakeUpgradeClient()
	c.containerImages["eidos-mindform-alice"] = "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"
	c.containerStates["eidos-mindform-alice"] = "running"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = "sha256:aaa"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = "sha256:bbb"
	c.imageExistsLocal["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = true
	c.imageExistsLocal["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = true
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
	c := newFakeUpgradeClient()
	c.containerImages["eidos-mindform-alice"] = "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"
	c.containerStates["eidos-mindform-alice"] = "running"
	// Same image ID for both refs.
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = "sha256:same"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:latest"] = "sha256:same"
	c.imageExistsLocal["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = true
	c.imageExistsLocal["ghcr.io/lucianoxu/eidopsyche-mindform:latest"] = true
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

func TestUpgrade_WaitIdleTimeout(t *testing.T) {
	c := newFakeUpgradeClient()
	c.containerImages["eidos-mindform-alice"] = "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"
	c.containerStates["eidos-mindform-alice"] = "running"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = "sha256:aaa"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = "sha256:bbb"
	c.imageExistsLocal["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = true
	c.imageExistsLocal["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = true
	c.imageLabels["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = map[string]string{}
	c.imageLabels["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = map[string]string{}
	// ContainerExec stub always returns non-idle, so wait-idle never advances.

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

func TestUpgrade_IdempotentReentryWhenContainerAbsent(t *testing.T) {
	c := newFakeUpgradeClient()
	// No containerImages entry, no containerStates entry — container is absent
	// (e.g. previous Upgrade run was SIGINT'd between Remove and Create).
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = "sha256:new"
	c.imageExistsLocal["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = true
	c.imageLabels["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = map[string]string{
		"org.eidopsyche.eidos-version":       "v0.11.3",
		"org.eidopsyche.claude-code-version": "2.1.140",
	}

	res, err := Upgrade(context.Background(), c, UpgradeOpts{
		Name:  "alice",
		Image: "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3",
	})
	if err != nil {
		t.Fatalf("Upgrade should recover from absent container, got: %v", err)
	}
	if res.Skipped {
		t.Error("Skipped should be false on idempotent re-entry to a missing container")
	}
	if res.OldImage != "" {
		t.Errorf("OldImage should be empty when container was absent; got %q", res.OldImage)
	}
	if !c.hasCall("ContainerCreate:eidos-mindform-alice:ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3") {
		t.Error("expected ContainerCreate with new image even on idempotent re-entry")
	}
	if !c.hasCall("ContainerStart:eidos-mindform-alice") {
		t.Error("expected ContainerStart even on idempotent re-entry")
	}
	if c.hasCall("ContainerStop") || c.hasCall("ContainerRemove") {
		t.Errorf("absent-container path must NOT call Stop/Remove; calls: %v", c.calls)
	}
}

func TestUpgrade_PreservesWorkspaceMounts(t *testing.T) {
	c := newFakeUpgradeClient()
	c.containerImages["eidos-mindform-alice"] = "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"
	c.containerStates["eidos-mindform-alice"] = "running"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = "sha256:aaa"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = "sha256:bbb"
	c.imageExistsLocal["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = true
	c.imageExistsLocal["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = true
	c.imageLabels["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = map[string]string{}
	c.imageLabels["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = map[string]string{}

	_, err := Upgrade(context.Background(), c, UpgradeOpts{
		Name:  "alice",
		Image: "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3",
		Grace: 10,
		Workspaces: []config.WorkspaceMount{
			{Name: "proj-x", HostPath: "/home/op/code/project-x", Mode: "rw"},
			{Name: "photos", HostPath: "/home/op/Pictures", Mode: "ro"},
		},
	})
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	want := []forgectl.Mount{
		{Type: forgectl.MountVolume, Source: forgectl.VolumeName("alice"), Target: "/eidos"},
		{Type: forgectl.MountBind, Source: "/home/op/code/project-x", Target: "/workspace/proj-x", ReadOnly: false},
		{Type: forgectl.MountBind, Source: "/home/op/Pictures", Target: "/workspace/photos", ReadOnly: true},
	}
	if len(c.lastCreateMounts) != len(want) {
		t.Fatalf("mount count: want %d got %d (%#v)", len(want), len(c.lastCreateMounts), c.lastCreateMounts)
	}
	for i := range want {
		if c.lastCreateMounts[i] != want[i] {
			t.Errorf("mount[%d]: want %#v got %#v", i, want[i], c.lastCreateMounts[i])
		}
	}
}

func TestUpgrade_NilWorkspaces_OntologyOnly(t *testing.T) {
	c := newFakeUpgradeClient()
	c.containerImages["eidos-mindform-alice"] = "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"
	c.containerStates["eidos-mindform-alice"] = "running"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = "sha256:aaa"
	c.imageIDs["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = "sha256:bbb"
	c.imageExistsLocal["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = true
	c.imageExistsLocal["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = true
	c.imageLabels["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.2"] = map[string]string{}
	c.imageLabels["ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"] = map[string]string{}

	if _, err := Upgrade(context.Background(), c, UpgradeOpts{
		Name:  "alice",
		Image: "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3",
		Grace: 10,
		// Workspaces left nil.
	}); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if len(c.lastCreateMounts) != 1 || c.lastCreateMounts[0].Target != "/eidos" {
		t.Errorf("nil-workspaces upgrade should mount only /eidos; got %#v", c.lastCreateMounts)
	}
}
