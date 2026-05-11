package state

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeContrib struct {
	path string
	data any
	err  error
}

func (f *fakeContrib) Path() string { return f.path }
func (f *fakeContrib) Snapshot(_ context.Context) (any, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.data, nil
}

func TestTree_RootSnapshotMergesContributors(t *testing.T) {
	tr := NewTree()
	tr.Register(&fakeContrib{path: "config", data: map[string]any{"log_level": "info"}})
	tr.Register(&fakeContrib{path: "identity", data: map[string]any{"label": "alice"}})
	snap, err := tr.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	m, ok := snap.(map[string]any)
	if !ok {
		t.Fatalf("expected map; got %T", snap)
	}
	if m["config"] == nil || m["identity"] == nil {
		t.Errorf("missing top-level keys: %+v", m)
	}
}

func TestTree_ExactContributorPathReturnsItsSnapshot(t *testing.T) {
	tr := NewTree()
	tr.Register(&fakeContrib{path: "config", data: map[string]any{"k": "v"}})
	snap, err := tr.Snapshot(context.Background(), "config")
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := snap.(map[string]any); m["k"] != "v" {
		t.Errorf("got %+v", snap)
	}
}

func TestTree_DottedPathIntoMap(t *testing.T) {
	tr := NewTree()
	tr.Register(&fakeContrib{path: "config", data: map[string]any{
		"heartbeat": map[string]any{"interval": "1m"},
	}})
	v, err := tr.Snapshot(context.Background(), "config.heartbeat.interval")
	if err != nil {
		t.Fatal(err)
	}
	if v != "1m" {
		t.Errorf("got %v", v)
	}
}

type fakeConfig struct {
	LogLevel  string             `toml:"log_level"`
	Heartbeat fakeHeartbeatBlock `toml:"heartbeat"`
}
type fakeHeartbeatBlock struct {
	Interval string `toml:"interval"`
}

func TestTree_DottedPathIntoStructWithTomlTags(t *testing.T) {
	tr := NewTree()
	tr.Register(&fakeContrib{path: "config", data: fakeConfig{
		LogLevel:  "info",
		Heartbeat: fakeHeartbeatBlock{Interval: "1m"},
	}})
	v, err := tr.Snapshot(context.Background(), "config.heartbeat.interval")
	if err != nil {
		t.Fatal(err)
	}
	if v != "1m" {
		t.Errorf("got %v", v)
	}
}

func TestTree_UnknownTopLevelPath(t *testing.T) {
	tr := NewTree()
	tr.Register(&fakeContrib{path: "config", data: map[string]any{}})
	_, err := tr.Snapshot(context.Background(), "nope")
	if !errors.Is(err, ErrPathNotFound) {
		t.Errorf("expected ErrPathNotFound; got %v", err)
	}
}

func TestTree_UnknownNestedPath(t *testing.T) {
	tr := NewTree()
	tr.Register(&fakeContrib{path: "config", data: map[string]any{"a": 1}})
	_, err := tr.Snapshot(context.Background(), "config.b")
	if !errors.Is(err, ErrPathNotFound) {
		t.Errorf("expected ErrPathNotFound; got %v", err)
	}
}

func TestTree_ContributorErrorPropagatedOnFullSnapshot(t *testing.T) {
	tr := NewTree()
	tr.Register(&fakeContrib{path: "broken", err: errors.New("boom")})
	tr.Register(&fakeContrib{path: "ok", data: map[string]any{"x": 1}})
	_, err := tr.Snapshot(context.Background(), "")
	if err == nil {
		t.Error("expected error from broken contributor")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("error should name the failing contributor; got %v", err)
	}
}

func TestTree_ContributorErrorOnlyAffectsItsSubtree(t *testing.T) {
	tr := NewTree()
	tr.Register(&fakeContrib{path: "broken", err: errors.New("boom")})
	tr.Register(&fakeContrib{path: "ok", data: map[string]any{"x": 1}})
	// Querying "ok" should still work even though "broken" is broken.
	v, err := tr.Snapshot(context.Background(), "ok")
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := v.(map[string]any); m["x"] != 1 {
		t.Errorf("got %v", v)
	}
}

func TestTree_DottedContributorPath(t *testing.T) {
	tr := NewTree()
	tr.Register(&fakeContrib{path: "lifecycle.wakes", data: []any{"w1", "w2"}})
	v, err := tr.Snapshot(context.Background(), "lifecycle.wakes")
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := v.([]any); len(s) != 2 {
		t.Errorf("got %v", v)
	}
}

func TestTree_DuplicateRegistrationPanics(t *testing.T) {
	tr := NewTree()
	tr.Register(&fakeContrib{path: "x"})
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	tr.Register(&fakeContrib{path: "x"})
}
