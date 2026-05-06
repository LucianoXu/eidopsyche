package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenAndMigrate(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	v, err := db.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != SchemaVersion {
		t.Fatalf("schema version got %d want %d", v, SchemaVersion)
	}
}

func TestMetaRoundtrip(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := db.SetMeta(ctx, "label", "alice"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	v, err := db.GetMeta(ctx, "label")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if v != "alice" {
		t.Fatalf("got %q want %q", v, "alice")
	}
}
