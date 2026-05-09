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

func TestMigrateFresh(t *testing.T) {
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
	// Verify v2 tables exist by inserting a row.
	_, err = db.ExecContext(ctx,
		`INSERT INTO invites(id, created_at, expires_at, max_uses, uses, issuer_label, redeemer_label)
		 VALUES('testid', 1, 0, 1, 0, 'issuer', 'redeemer')`)
	if err != nil {
		t.Fatalf("insert into invites: %v", err)
	}
	// Verify schema_version matches the current SchemaVersion.
	v, err := db.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != SchemaVersion {
		t.Fatalf("schema version got %d want %d", v, SchemaVersion)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	// Run Migrate twice; should be idempotent.
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
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

func TestSchemaV3DropsRelayWhitelistView(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "state.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	var name string
	err = db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='view' AND name='relay_whitelist'`,
	).Scan(&name)
	if err == nil {
		t.Errorf("relay_whitelist view still exists after Migrate; got %q", name)
	}
	// sql.ErrNoRows is the expected outcome — the view must not exist.
}
