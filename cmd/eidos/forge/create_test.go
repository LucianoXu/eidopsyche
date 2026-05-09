package forge

import (
	"context"
	"errors"
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
func (f *fakeClient) ImagePull(_ context.Context, ref string, _ io.Writer) error {
	f.pulled = append(f.pulled, ref)
	return nil
}
func (f *fakeClient) RunInit(_ context.Context, opts forgectl.RunInitOpts) (forgectl.RunInitResult, error) {
	// drain stdin so producers don't block
	if opts.Stdin != nil {
		_, _ = io.Copy(io.Discard, opts.Stdin)
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
	err := orchestrate(context.Background(), f, "alice", createOpts{
		owner: "npub1ownertest", relay: "wss://r", label: "alice", noLogin: true, image: "img:dev",
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want exists error, got %v", err)
	}
}

func TestCreateOrchestratesAllSteps(t *testing.T) {
	f := &fakeClient{}
	err := orchestrate(context.Background(), f, "alice", createOpts{
		owner: "npub1ownertest", relay: "wss://r", label: "alice", noLogin: true, image: "img:dev",
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
	err := orchestrate(context.Background(), wrapped, "alice", createOpts{
		owner: "npub1ownertest", relay: "wss://r", label: "alice", noLogin: true, image: "img:dev",
	})
	if err == nil || !errors.Is(err, want) {
		t.Errorf("expected wrapped pull error, got %v", err)
	}
}
