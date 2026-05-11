package daemon

import (
	"context"
	"strings"
	"testing"
)

// TestStateContributors_AllPathsResolveForFreshDaemon is the end-to-end
// guard for Phase B.2: every domain mounted as a contributor must
// resolve via state.get on a freshly constructed test daemon. Catches
// (1) missing registration, (2) contributors that error on empty
// stores, (3) Mutate / state.get path mismatches.
func TestStateContributors_AllPathsResolveForFreshDaemon(t *testing.T) {
	d := newTestDaemon(t)
	d.registerCoreStateContributors()

	if err := sanityCheckContributors(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

func TestStateContributors_IdentitySnapshot(t *testing.T) {
	d := newTestDaemon(t)
	d.registerCoreStateContributors()

	snap, err := d.stateTree.Snapshot(context.Background(), "identity")
	if err != nil {
		t.Fatal(err)
	}
	m, ok := snap.(map[string]any)
	if !ok {
		t.Fatalf("expected map; got %T", snap)
	}
	if m["pubkey"] == "" || m["npub"] == "" {
		t.Errorf("identity missing pubkey/npub: %+v", m)
	}
	if m["card"] == nil {
		t.Errorf("identity.card not populated: %+v", m)
	}
}

func TestStateContributors_DottedPathScalar(t *testing.T) {
	d := newTestDaemon(t)
	d.registerCoreStateContributors()

	v, err := d.stateTree.Snapshot(context.Background(), "identity.pubkey")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := v.(string)
	if len(s) != 64 {
		t.Errorf("identity.pubkey expected 64 hex; got %q", s)
	}
}

func TestStateContributors_ConfigScalarViaDottedPath(t *testing.T) {
	d := newTestDaemon(t)
	d.registerCoreStateContributors()
	// Snapshot loads from disk; new tempdir has no config.toml yet, so
	// it'll return defaults via config.Load's missing-file path. Run a
	// minimum spot-check.
	_, err := d.stateTree.Snapshot(context.Background(), "config")
	if err != nil && !strings.Contains(err.Error(), "no such file") {
		t.Fatal(err)
	}
}

func TestStateContributors_ContactsEmpty(t *testing.T) {
	d := newTestDaemon(t)
	d.registerCoreStateContributors()
	v, err := d.stateTree.Snapshot(context.Background(), "contacts")
	if err != nil {
		t.Fatal(err)
	}
	m, _ := v.(map[string]any)
	if len(m) != 0 {
		t.Errorf("fresh daemon should have no contacts; got %+v", m)
	}
}

func TestStateContributors_ServiceVersionPresent(t *testing.T) {
	d := newTestDaemon(t)
	d.registerCoreStateContributors()
	v, err := d.stateTree.Snapshot(context.Background(), "service.version")
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(map[string]string)
	if !ok {
		t.Fatalf("expected map[string]string; got %T", v)
	}
	if _, has := m["version"]; !has {
		t.Errorf("service.version missing 'version': %+v", m)
	}
}

func TestStateContributors_JsonRoundTrip(t *testing.T) {
	d := newTestDaemon(t)
	d.registerCoreStateContributors()
	// Full root should marshal without error.
	snap, err := d.stateTree.Snapshot(context.Background(), "")
	if err != nil && !strings.Contains(err.Error(), "no such file") {
		t.Fatal(err)
	}
	if jsonMustMarshal(snap) == "" && snap != nil {
		t.Errorf("snapshot did not marshal: %+v", snap)
	}
}
