package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

func TestStateGet_Registered(t *testing.T) {
	if _, ok := methodTable["state.get"]; !ok {
		t.Fatal("state.get must be registered in methodTable")
	}
}

type stubContrib struct {
	path string
	data any
}

func (s *stubContrib) Path() string                            { return s.path }
func (s *stubContrib) Snapshot(_ context.Context) (any, error) { return s.data, nil }

func TestStateGet_FullSnapshot(t *testing.T) {
	d := newTestDaemon(t)
	d.RegisterStateContributor(&stubContrib{path: "x", data: map[string]any{"k": "v"}})

	raw, _ := json.Marshal(map[string]any{})
	out, ipcErr := stateGet(context.Background(), d, nil, raw)
	if ipcErr != nil {
		t.Fatal(ipcErr)
	}
	m, _ := out.(map[string]any)
	x, _ := m["x"].(map[string]any)
	if x["k"] != "v" {
		t.Errorf("got %+v", out)
	}
}

func TestStateGet_DottedPath(t *testing.T) {
	d := newTestDaemon(t)
	d.RegisterStateContributor(&stubContrib{
		path: "config",
		data: map[string]any{"heartbeat": map[string]any{"interval": "1m"}},
	})

	raw, _ := json.Marshal(StateGetParams{Path: "config.heartbeat.interval"})
	out, ipcErr := stateGet(context.Background(), d, nil, raw)
	if ipcErr != nil {
		t.Fatal(ipcErr)
	}
	if out != "1m" {
		t.Errorf("got %v", out)
	}
}

func TestStateGet_UnknownPathReturnsPathNotFound(t *testing.T) {
	d := newTestDaemon(t)
	raw, _ := json.Marshal(StateGetParams{Path: "nope"})
	_, ipcErr := stateGet(context.Background(), d, nil, raw)
	if ipcErr == nil {
		t.Fatal("expected error")
	}
	if ipcErr.Code != ipc.ErrPathNotFound {
		t.Errorf("got code %q want %q", ipcErr.Code, ipc.ErrPathNotFound)
	}
}

type erroringContrib struct{ err error }

func (e *erroringContrib) Path() string                            { return "broken" }
func (e *erroringContrib) Snapshot(_ context.Context) (any, error) { return nil, e.err }

func TestStateGet_ContributorErrorSurfacesAsInternal(t *testing.T) {
	d := newTestDaemon(t)
	d.RegisterStateContributor(&erroringContrib{err: errors.New("disk fail")})

	raw, _ := json.Marshal(StateGetParams{Path: "broken"})
	_, ipcErr := stateGet(context.Background(), d, nil, raw)
	if ipcErr == nil {
		t.Fatal("expected error")
	}
	if ipcErr.Code != ipc.ErrInternal {
		t.Errorf("got code %q want %q", ipcErr.Code, ipc.ErrInternal)
	}
}
