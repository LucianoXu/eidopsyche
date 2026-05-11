package forge

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// ---------------------------------------------------------------------------
// Fake client for orchestration unit tests
// ---------------------------------------------------------------------------

type fakeClient struct {
	volExists  bool
	volCreated bool
	contExists bool
	pulled     []string
	inits      []forgectl.RunInitOpts
	initStdin  [][]byte // captured stdin bytes (one entry per RunInit call)
}

func (f *fakeClient) VolumeExists(_ context.Context, _ string) (bool, error) {
	return f.volExists, nil
}
func (f *fakeClient) VolumeCreate(_ context.Context, _ string) error {
	f.volCreated = true
	return nil
}
func (f *fakeClient) VolumeRemove(_ context.Context, _ string) error { return nil }
func (f *fakeClient) ContainerExists(_ context.Context, _ string) (bool, error) {
	return f.contExists, nil
}
func (f *fakeClient) ContainerInspectState(_ context.Context, _ string) (string, error) {
	return "absent", nil
}
func (f *fakeClient) ContainerCreate(_ context.Context, _ forgectl.CreateOpts) error { return nil }
func (f *fakeClient) ContainerStart(_ context.Context, _ string) error               { return nil }
func (f *fakeClient) ContainerStop(_ context.Context, _ string, _ int) error         { return nil }
func (f *fakeClient) ContainerRemove(_ context.Context, _ string) error              { return nil }
func (f *fakeClient) ImageExists(_ context.Context, _ string) (bool, error) {
	// Default: image absent → orchestrator falls through to ImagePull,
	// preserving the existing test's pull-was-called assertions.
	return false, nil
}
func (f *fakeClient) ImagePull(_ context.Context, ref string, _ io.Writer) error {
	f.pulled = append(f.pulled, ref)
	return nil
}
func (f *fakeClient) RunInit(_ context.Context, opts forgectl.RunInitOpts) (forgectl.RunInitResult, error) {
	// Capture stdin so tests can introspect the tar bytes the orchestrator
	// streams in (the wizard's JournalEntry-as-tar-entry plumbing relies
	// on this).
	if opts.Stdin != nil {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, opts.Stdin)
		f.initStdin = append(f.initStdin, buf.Bytes())
	} else {
		f.initStdin = append(f.initStdin, nil)
	}
	f.inits = append(f.inits, opts)
	return forgectl.RunInitResult{ExitCode: 0}, nil
}
func (f *fakeClient) ContainerExec(_ context.Context, _ string, _ []string) (forgectl.ExecResult, error) {
	return forgectl.ExecResult{}, nil
}
func (f *fakeClient) VolumeList(_ context.Context, _ string) ([]string, error) { return nil, nil }
func (f *fakeClient) ContainerLogs(_ context.Context, _ string, _ bool, _ io.Writer) error {
	return nil
}
func (f *fakeClient) CopyFromContainer(_ context.Context, _ string, _ string, _ io.Writer) error {
	return nil
}

// fakeClientPullErr wraps fakeClient to override ImagePull with an error func.
type fakeClientPullErr struct {
	fakeClient
	pullErr func(context.Context, string, io.Writer) error
}

func (f *fakeClientPullErr) ImagePull(ctx context.Context, ref string, w io.Writer) error {
	return f.pullErr(ctx, ref, w)
}

func TestCreateFlagValidation(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing all flags",
			args:    []string{"create", "alice"},
			wantErr: "owner",
		},
		{
			name:    "missing relay",
			args:    []string{"create", "alice", "--owner", "npub1ownertest"},
			wantErr: "relay",
		},
		{
			name:    "missing name",
			args:    []string{"create", "--owner", "npub1x", "--relay", "wss://x"},
			wantErr: "name",
		},
		{
			name:    "invalid name",
			args:    []string{"create", "Alice", "--owner", "npub1x", "--relay", "wss://x"},
			wantErr: "invalid mind-form name",
		},
		{
			name:    "invalid owner",
			args:    []string{"create", "alice", "--owner", "not-an-npub", "--relay", "wss://x"},
			wantErr: "owner",
		},
		{
			name:    "invalid relay scheme",
			args:    []string{"create", "alice", "--owner", "npub1ownertest", "--relay", "http://x"},
			wantErr: "relay must be ws:// or wss://",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := Command()
			cmd.SetArgs(c.args)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("want error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error = %q; want substring %q", err.Error(), c.wantErr)
			}
		})
	}
}

func TestCreateRefusesIfVolumeExists(t *testing.T) {
	f := &fakeClient{volExists: true}
	err := Orchestrate(context.Background(), f, "alice", CreateOpts{
		Owner: "npub1ownertest", Relay: "wss://r", Label: "alice", NoLogin: true, Image: "img:dev",
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want exists error, got %v", err)
	}
}

// TestCreateSkipsPullWhenImagePresentLocally covers the path that fixed
// the ghcr.io denied error: when ImageExists reports the image is already
// in the local store, orchestrate must not call ImagePull. (Without this,
// `forge create` against a locally-built dev image fails because Docker
// tries to authenticate against a private registry that does not host
// the tag.)
func TestCreateSkipsPullWhenImagePresentLocally(t *testing.T) {
	f := &fakeClientLocalImage{}
	err := Orchestrate(context.Background(), f, "alice", CreateOpts{
		Owner: "npub1ownertest", Relay: "wss://r", Label: "alice", NoLogin: true, Image: "img:dev",
	})
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	if len(f.pulled) != 0 {
		t.Errorf("pulled = %v; expected no pull when image exists locally", f.pulled)
	}
	if len(f.inits) != 1 {
		t.Errorf("init container runs = %d, want 1", len(f.inits))
	}
}

type fakeClientLocalImage struct{ fakeClient }

func (f *fakeClientLocalImage) ImageExists(_ context.Context, _ string) (bool, error) {
	return true, nil
}

func TestCreateOrchestratesAllSteps(t *testing.T) {
	f := &fakeClient{}
	err := Orchestrate(context.Background(), f, "alice", CreateOpts{
		Owner: "npub1ownertest", Relay: "wss://r", Label: "alice", NoLogin: true, Image: "img:dev",
	})
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	if !f.volCreated {
		t.Errorf("volume not created")
	}
	if len(f.pulled) != 1 || f.pulled[0] != "img:dev" {
		t.Errorf("pulled = %v", f.pulled)
	}
	if len(f.inits) != 1 {
		t.Fatalf("init container runs = %d, want 1", len(f.inits))
	}
	init := f.inits[0]
	if init.Mount.Target != "/eidos" {
		t.Errorf("init mount target = %q", init.Mount.Target)
	}
	if init.Image != "img:dev" {
		t.Errorf("init image = %q", init.Image)
	}
	if init.Mount.VolumeName != "eidos-mindform-alice" {
		t.Errorf("init mount volume = %q", init.Mount.VolumeName)
	}
}

func TestCreateImagePullErrorsBubbled(t *testing.T) {
	f := &fakeClient{}
	want := errors.New("net down")
	pullErrFn := func(_ context.Context, _ string, _ io.Writer) error { return want }
	wrapped := &fakeClientPullErr{fakeClient: *f, pullErr: pullErrFn}
	err := Orchestrate(context.Background(), wrapped, "alice", CreateOpts{
		Owner: "npub1ownertest", Relay: "wss://r", Label: "alice", NoLogin: true, Image: "img:dev",
	})
	if err == nil || !errors.Is(err, want) {
		t.Errorf("expected wrapped pull error, got %v", err)
	}
}

// TestCreateInitContainerRunsAsRoot pins the requirement that init-volume
// runs as User: "0:0" so it can extract / chown the volume regardless of
// the image's USER directive.
func TestCreateInitContainerRunsAsRoot(t *testing.T) {
	f := &fakeClient{}
	err := Orchestrate(context.Background(), f, "alice", CreateOpts{
		Owner: "npub1ownertest", Relay: "wss://r", Label: "alice", NoLogin: true, Image: "img:dev",
	})
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	if len(f.inits) != 1 {
		t.Fatalf("init runs = %d, want 1", len(f.inits))
	}
	if f.inits[0].User != "0:0" {
		t.Errorf("init User = %q, want %q", f.inits[0].User, "0:0")
	}
}

// TestCreateModelEnvPlumbing pins that --model is plumbed through to
// init-volume's env as EIDOS_FORGE_MODEL.
func TestCreateModelEnvPlumbing(t *testing.T) {
	f := &fakeClient{}
	err := Orchestrate(context.Background(), f, "alice", CreateOpts{
		Owner: "npub1ownertest", Relay: "wss://r", Label: "alice",
		NoLogin: true, Image: "img:dev", Model: "claude-sonnet-4-7",
	})
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	if len(f.inits) != 1 {
		t.Fatalf("init runs = %d, want 1", len(f.inits))
	}
	want := "EIDOS_FORGE_MODEL=claude-sonnet-4-7"
	found := false
	for _, e := range f.inits[0].Env {
		if e == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("init env missing %q; got %v", want, f.inits[0].Env)
	}
}

// TestCreateModelFlagValidation checks the create command rejects bad
// --model values at the cobra layer before any docker work.
func TestCreateModelFlagValidation(t *testing.T) {
	cmd := Command()
	cmd.SetArgs([]string{
		"create", "alice",
		"--owner", "npub1ownertest",
		"--relay", "wss://r",
		"--model", "garbage",
	})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	err := cmd.Execute()
	if err == nil {
		t.Fatal("garbage --model should be rejected")
	}
	if !strings.Contains(err.Error(), "model") {
		t.Errorf("error should mention Model: %v", err)
	}
}

func TestValidateModel_Helper(t *testing.T) {
	if err := validateModel(""); err != nil {
		t.Errorf("empty should pass: %v", err)
	}
	if err := validateModel("claude-sonnet-4-7"); err != nil {
		t.Errorf("valid should pass: %v", err)
	}
	if err := validateModel("garbage"); err == nil {
		t.Error("garbage should fail validation")
	}
}

// TestOrchestrate_KeyHexFlowsToInitEnv pins the wizard's keypair-injection
// path: when CreateOpts.KeyHex is set, the env passed to init-volume
// must include EIDOS_FORGE_KEY_HEX so the in-container gate init
// adopts it instead of generating a fresh keypair.
func TestOrchestrate_KeyHexFlowsToInitEnv(t *testing.T) {
	f := &fakeClient{}
	err := Orchestrate(context.Background(), f, "alice", CreateOpts{
		Owner: "npub1ownertest", Relay: "wss://r", Label: "alice",
		Image: "img:dev", KeyHex: "deadbeef",
	})
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	init := f.inits[0]
	if !envContains(init.Env, "EIDOS_FORGE_KEY_HEX=deadbeef") {
		t.Errorf("init env missing EIDOS_FORGE_KEY_HEX=deadbeef: %v", init.Env)
	}
}

// TestOrchestrate_NoKeyHexOmitsEnv: the legacy `eidos forge create`
// path (no wizard, no KeyHex) must NOT inject EIDOS_FORGE_KEY_HEX —
// otherwise init-volume would try to load a non-existent file.
func TestOrchestrate_NoKeyHexOmitsEnv(t *testing.T) {
	f := &fakeClient{}
	err := Orchestrate(context.Background(), f, "alice", CreateOpts{
		Owner: "npub1ownertest", Relay: "wss://r", Label: "alice", Image: "img:dev",
	})
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	init := f.inits[0]
	for _, e := range init.Env {
		if strings.HasPrefix(e, "EIDOS_FORGE_KEY_HEX=") {
			t.Errorf("EIDOS_FORGE_KEY_HEX present despite KeyHex unset: %q", e)
		}
	}
}

// TestOrchestrate_JournalEntryLandsInTar: the wizard's rendered
// summoning book must reach the volume verbatim, including any `{{`
// literals in the user-provided text.
func TestOrchestrate_JournalEntryLandsInTar(t *testing.T) {
	f := &fakeClient{}
	const journal = "# 召唤书\n\nThis has {{.Literal}} that must NOT expand.\n"
	err := Orchestrate(context.Background(), f, "alice", CreateOpts{
		Owner: "npub1ownertest", Relay: "wss://r", Label: "alice",
		Image: "img:dev", JournalEntry: journal,
	})
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	if len(f.initStdin) != 1 {
		t.Fatalf("captured stdin entries = %d, want 1", len(f.initStdin))
	}
	got := tarEntryFromBytes(t, f.initStdin[0], "journal/0000-summoning.md")
	if got != journal {
		t.Errorf("journal entry in tar = %q, want %q", got, journal)
	}
}

func envContains(env []string, target string) bool {
	for _, e := range env {
		if e == target {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Failure-mode rollback tests
// ---------------------------------------------------------------------------

// fakeClientRunInitErr makes RunInit fail; orchestrate must reverse-undo
// the volume it created in the prior step.
type fakeClientRunInitErr struct {
	fakeClient
	volumeRemoved bool
}

func (f *fakeClientRunInitErr) VolumeRemove(_ context.Context, _ string) error {
	f.volumeRemoved = true
	return nil
}
func (f *fakeClientRunInitErr) RunInit(_ context.Context, _ forgectl.RunInitOpts) (forgectl.RunInitResult, error) {
	return forgectl.RunInitResult{Stderr: []byte("boom")}, fmt.Errorf("init failed")
}

func TestOrchestrate_RollsBackVolumeOnInitFailure(t *testing.T) {
	f := &fakeClientRunInitErr{}
	err := Orchestrate(context.Background(), f, "alice", CreateOpts{
		Label: "alice", Owner: "npub1ownertest", Relay: "wss://x", Image: "img:dev",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !f.volumeRemoved {
		t.Fatal("expected volume to be rolled back after RunInit failure")
	}
}

// fakeClientContainerCreateErr makes ContainerCreate fail after the volume
// has been created and init-volume succeeded. Orchestrate must roll back
// the volume so the operator can retry cleanly without manual cleanup.
type fakeClientContainerCreateErr struct {
	fakeClient
	volumeRemoved    bool
	containerRemoved bool
}

func (f *fakeClientContainerCreateErr) VolumeRemove(_ context.Context, _ string) error {
	f.volumeRemoved = true
	return nil
}
func (f *fakeClientContainerCreateErr) ContainerRemove(_ context.Context, _ string) error {
	f.containerRemoved = true
	return nil
}
func (f *fakeClientContainerCreateErr) ContainerCreate(_ context.Context, _ forgectl.CreateOpts) error {
	return fmt.Errorf("create failed")
}

func TestOrchestrate_RollsBackVolumeOnContainerCreateFailure(t *testing.T) {
	f := &fakeClientContainerCreateErr{}
	err := Orchestrate(context.Background(), f, "alice", CreateOpts{
		Label: "alice", Owner: "npub1ownertest", Relay: "wss://x", Image: "img:dev",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !f.volumeRemoved {
		t.Fatal("expected volume to be rolled back after ContainerCreate failure")
	}
}

// TestOrchestrate_NoVolumeCreatedOnImagePullFailure pins the invariant
// that an image-pull failure happens BEFORE any volume work, so there is
// nothing to roll back. (Failing this would mean volume creation moved
// ahead of image-existence-check, which a regression would otherwise
// silently allow.)
func TestOrchestrate_NoVolumeCreatedOnImagePullFailure(t *testing.T) {
	f := &fakeClient{}
	pullErrFn := func(_ context.Context, _ string, _ io.Writer) error {
		return errors.New("net down")
	}
	wrapped := &fakeClientPullErr{fakeClient: *f, pullErr: pullErrFn}
	err := Orchestrate(context.Background(), wrapped, "alice", CreateOpts{
		Label: "alice", Owner: "npub1ownertest", Relay: "wss://x", Image: "img:dev",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if wrapped.volCreated {
		t.Fatal("volume must not be created when image pull fails")
	}
}

func tarEntryFromBytes(t *testing.T, body []byte, name string) string {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(body))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		if h.Name == name {
			b, err := io.ReadAll(tr)
			if err != nil {
				t.Fatalf("read entry: %v", err)
			}
			return string(b)
		}
	}
	t.Fatalf("tar entry %q not in stream", name)
	return ""
}
