package forge

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// ---------------------------------------------------------------------------
// Minimal fake client for restart tests
// ---------------------------------------------------------------------------

type restartFakeClient struct {
	ops                []string
	createImage        string
	createMounts       []forgectl.Mount
	inspectImageID     string // image ID (sha256:...) returned by ContainerInspectImage
	inspectImageReturn string // image ref (tag) returned by ContainerInspectImage
	inspectImageErr    error
}

func (f *restartFakeClient) ContainerStop(_ context.Context, name string, _ int) error {
	f.ops = append(f.ops, "stop:"+name)
	return nil
}
func (f *restartFakeClient) ContainerInspectImage(_ context.Context, name string) (string, string, error) {
	f.ops = append(f.ops, "inspect-image:"+name)
	return f.inspectImageID, f.inspectImageReturn, f.inspectImageErr
}
func (f *restartFakeClient) ContainerRemove(_ context.Context, name string) error {
	f.ops = append(f.ops, "remove:"+name)
	return nil
}
func (f *restartFakeClient) ContainerCreate(_ context.Context, opts forgectl.CreateOpts) error {
	f.ops = append(f.ops, "create:"+opts.Name)
	f.createImage = opts.Image
	f.createMounts = opts.Mounts
	return nil
}
func (f *restartFakeClient) ContainerStart(_ context.Context, name string) error {
	f.ops = append(f.ops, "start:"+name)
	return nil
}

// Zero-value stubs for the rest of forgectl.Client.
func (f *restartFakeClient) VolumeCreate(_ context.Context, _ string) error { return nil }
func (f *restartFakeClient) VolumeExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (f *restartFakeClient) VolumeRemove(_ context.Context, _ string) error { return nil }
func (f *restartFakeClient) VolumeList(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (f *restartFakeClient) ContainerExists(_ context.Context, _ string) (bool, error) {
	return true, nil
}
func (f *restartFakeClient) ContainerInspectState(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (f *restartFakeClient) ContainerInspectMounts(_ context.Context, _ string) ([]forgectl.Mount, error) {
	return nil, nil
}
func (f *restartFakeClient) ContainerExec(_ context.Context, _ string, _ []string) (forgectl.ExecResult, error) {
	return forgectl.ExecResult{}, nil
}
func (f *restartFakeClient) RunInit(_ context.Context, _ forgectl.RunInitOpts) (forgectl.RunInitResult, error) {
	return forgectl.RunInitResult{}, nil
}
func (f *restartFakeClient) CopyFromContainer(_ context.Context, _, _ string, _ io.Writer) error {
	return nil
}
func (f *restartFakeClient) ImageExists(_ context.Context, _ string) (bool, error)    { return false, nil }
func (f *restartFakeClient) ImagePull(_ context.Context, _ string, _ io.Writer) error { return nil }
func (f *restartFakeClient) ImageInspectLabels(_ context.Context, _ string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (f *restartFakeClient) ImageInspectID(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (f *restartFakeClient) ContainerLogs(_ context.Context, _ string, _ bool, _ io.Writer) error {
	return nil
}

func newRestartFakeClient(t *testing.T) *restartFakeClient {
	t.Helper()
	return &restartFakeClient{}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestRestart_PreservesImage_AppliesNewMounts confirms the recreate
// flow preserves the previous container's image while picking up the
// latest workspace mount list from config.
func TestRestart_PreservesImage_AppliesNewMounts(t *testing.T) {
	fc := newRestartFakeClient(t)
	fc.inspectImageReturn = "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"

	cfg := &config.Config{
		Forge: map[string]config.ForgeMindForm{
			"alice": {Workspaces: []config.WorkspaceMount{
				{Name: "proj-x", HostPath: "/home/op/code/project-x", Mode: "rw"},
			}},
		},
	}
	if err := Restart(context.Background(), fc, "alice", cfg, 10); err != nil {
		t.Fatal(err)
	}
	wantOps := []string{
		"stop:" + forgectl.ContainerName("alice"),
		"inspect-image:" + forgectl.ContainerName("alice"),
		"remove:" + forgectl.ContainerName("alice"),
		"create:" + forgectl.ContainerName("alice"),
		"start:" + forgectl.ContainerName("alice"),
	}
	if !reflect.DeepEqual(fc.ops, wantOps) {
		t.Fatalf("ops\nwant %v\n got %v", wantOps, fc.ops)
	}
	if fc.createImage != "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3" {
		t.Errorf("create image = %q; want preserved", fc.createImage)
	}
	wantMounts := []forgectl.Mount{
		{Type: forgectl.MountVolume, Source: forgectl.VolumeName("alice"), Target: "/eidos"},
		{Type: forgectl.MountBind, Source: "/home/op/code/project-x", Target: "/workspace/proj-x", ReadOnly: false},
	}
	if !reflect.DeepEqual(fc.createMounts, wantMounts) {
		t.Errorf("create mounts\nwant %#v\n got %#v", wantMounts, fc.createMounts)
	}
}

// TestRestart_ImageInspectError_AbortsCleanly: if reading the current
// image fails, we must abort before doing rm — container still exists.
func TestRestart_ImageInspectError_AbortsCleanly(t *testing.T) {
	fc := newRestartFakeClient(t)
	fc.inspectImageErr = errors.New("docker oops")
	err := Restart(context.Background(), fc, "alice", &config.Config{}, 10)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, op := range fc.ops {
		if op == "remove:"+forgectl.ContainerName("alice") {
			t.Errorf("must not remove after inspect-image failure: ops=%v", fc.ops)
		}
	}
}

// TestRestart_PrefersImageIDOverRef confirms the recreate uses the
// content-addressable image ID (sha256:...) when available, so mutable
// tags like :dev or :latest can't silently move the mind-form to a
// rebuilt image between create and restart.
func TestRestart_PrefersImageIDOverRef(t *testing.T) {
	fc := newRestartFakeClient(t)
	fc.inspectImageID = "sha256:abc123"
	fc.inspectImageReturn = "ghcr.io/lucianoxu/eidopsyche-mindform:dev"

	if err := Restart(context.Background(), fc, "alice", &config.Config{}, 10); err != nil {
		t.Fatal(err)
	}
	if fc.createImage != "sha256:abc123" {
		t.Errorf("create image = %q; want the ID (sha256:abc123), not the mutable ref", fc.createImage)
	}
}

// TestRestart_FallsBackToRefWhenIDMissing confirms that if the docker
// client returns an empty ID for any reason, the ref is used so the
// command still completes rather than refusing.
func TestRestart_FallsBackToRefWhenIDMissing(t *testing.T) {
	fc := newRestartFakeClient(t)
	fc.inspectImageID = ""
	fc.inspectImageReturn = "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3"

	if err := Restart(context.Background(), fc, "alice", &config.Config{}, 10); err != nil {
		t.Fatal(err)
	}
	if fc.createImage != "ghcr.io/lucianoxu/eidopsyche-mindform:v0.11.3" {
		t.Errorf("create image = %q; want fallback to ref", fc.createImage)
	}
}
