package identity_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/store"
)

func TestBootstrap_FreshDir_WritesAllArtifacts(t *testing.T) {
	dir := t.TempDir()
	npub, err := identity.Bootstrap(dir, "alice", "wss://relay.example/")
	if err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}
	if npub == "" {
		t.Fatalf("Bootstrap returned empty npub")
	}
	for _, name := range []string{"key", "state.db", "config.toml"} {
		if _, statErr := os.Stat(filepath.Join(dir, name)); statErr != nil {
			t.Errorf("expected %s to exist after Bootstrap: %v", name, statErr)
		}
	}
	db, err := store.Open(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatalf("open state.db: %v", err)
	}
	defer db.Close()
	label, err := db.GetMeta(context.Background(), "label")
	if err != nil || label != "alice" {
		t.Errorf("label meta = %q, %v; want %q, nil", label, err, "alice")
	}
}

func TestBootstrap_AlreadyInitialized_ReturnsTypedError(t *testing.T) {
	dir := t.TempDir()
	if _, err := identity.Bootstrap(dir, "alice", "wss://r/"); err != nil {
		t.Fatalf("first Bootstrap: %v", err)
	}
	_, err := identity.Bootstrap(dir, "alice", "wss://r/")
	if !errors.Is(err, identity.ErrAlreadyInitialized) {
		t.Errorf("second Bootstrap err = %v, want ErrAlreadyInitialized", err)
	}
}

func TestBootstrap_RejectsBadRelay(t *testing.T) {
	dir := t.TempDir()
	if _, err := identity.Bootstrap(dir, "alice", "http://nope/"); err == nil {
		t.Errorf("expected error for non-ws scheme")
	}
	if _, err := identity.Bootstrap(dir, "alice", ""); err == nil {
		t.Errorf("expected error for empty relay")
	}
}

func TestBootstrap_RejectsBlankLabel(t *testing.T) {
	dir := t.TempDir()
	if _, err := identity.Bootstrap(dir, "", "wss://r/"); err == nil {
		t.Errorf("expected error for empty label")
	}
}

func TestBootstrapWithExistingKey_UsesExistingKey(t *testing.T) {
	dir := t.TempDir()
	pre, err := identity.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := identity.SaveKey(filepath.Join(dir, "key"), pre); err != nil {
		t.Fatalf("SaveKey: %v", err)
	}
	npub, err := identity.BootstrapWithExistingKey(dir, "alice", "wss://relay/")
	if err != nil {
		t.Fatalf("BootstrapWithExistingKey: %v", err)
	}
	if npub != pre.Npub {
		t.Errorf("returned npub = %q, want %q", npub, pre.Npub)
	}
}

func TestBootstrapWithExistingKey_RequiresKeyFile(t *testing.T) {
	dir := t.TempDir()
	_, err := identity.BootstrapWithExistingKey(dir, "alice", "wss://relay/")
	if err == nil {
		t.Fatalf("expected error when key file is absent")
	}
}
