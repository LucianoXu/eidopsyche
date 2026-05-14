package forge

import (
	"context"
	"reflect"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// capturingFakeClient wraps fakeClient and exposes a helper to retrieve
// the mounts from the last ContainerCreate call.
type capturingFakeClient struct {
	fakeClient
}

func newCapturingFakeClient(_ *testing.T) *capturingFakeClient {
	return &capturingFakeClient{}
}

func (c *capturingFakeClient) lastCreateMounts() []forgectl.Mount {
	if len(c.createCalls) == 0 {
		return nil
	}
	return c.createCalls[len(c.createCalls)-1].Mounts
}

func TestOrchestrate_AssemblesWorkspaceMounts(t *testing.T) {
	cfg := &config.Config{
		Forge: map[string]config.ForgeMindForm{
			"alice": {Workspaces: []config.WorkspaceMount{
				{Name: "proj-x", HostPath: "/home/op/code/project-x", Mode: "rw"},
				{Name: "photos", HostPath: "/home/op/Pictures", Mode: "ro"},
			}},
		},
	}

	fc := newCapturingFakeClient(t)
	if err := Orchestrate(context.Background(), fc, "alice", CreateOpts{Label: "alice"}, cfg); err != nil {
		t.Fatal(err)
	}
	want := []forgectl.Mount{
		{Type: forgectl.MountVolume, Source: forgectl.VolumeName("alice"), Target: "/eidos"},
		{Type: forgectl.MountBind, Source: "/home/op/code/project-x", Target: "/workspace/proj-x", ReadOnly: false},
		{Type: forgectl.MountBind, Source: "/home/op/Pictures", Target: "/workspace/photos", ReadOnly: true},
	}
	if !reflect.DeepEqual(fc.lastCreateMounts(), want) {
		t.Fatalf("mount list mismatch\nwant %#v\n got %#v", want, fc.lastCreateMounts())
	}
}

func TestOrchestrate_NilConfig_OntologyOnly(t *testing.T) {
	fc := newCapturingFakeClient(t)
	if err := Orchestrate(context.Background(), fc, "alice", CreateOpts{Label: "alice"}, nil); err != nil {
		t.Fatal(err)
	}
	want := []forgectl.Mount{
		{Type: forgectl.MountVolume, Source: forgectl.VolumeName("alice"), Target: "/eidos"},
	}
	if !reflect.DeepEqual(fc.lastCreateMounts(), want) {
		t.Fatalf("nil-config mount list:\nwant %#v\n got %#v", want, fc.lastCreateMounts())
	}
}
