package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

// Open opens or creates a SQLite database at path. ReadOnly opens it RO.
func Open(path string, readOnly bool) (*DB, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	if readOnly {
		dsn = "file:" + path + "?mode=ro&_pragma=foreign_keys(1)"
	}
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := d.Ping(); err != nil {
		_ = d.Close()
		return nil, err
	}
	return &DB{d}, nil
}

func (db *DB) Migrate(ctx context.Context) error {
	if _, err := db.ExecContext(ctx, schemaV1); err != nil {
		return fmt.Errorf("apply schema v1: %w", err)
	}
	current, err := db.SchemaVersion(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if current == 0 {
		_, err := db.ExecContext(ctx,
			`INSERT INTO meta(key,value) VALUES('schema_version', ?)`, strconv.Itoa(SchemaVersion))
		if err != nil {
			return fmt.Errorf("write schema_version: %w", err)
		}
	}
	return nil
}

func (db *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='schema_version'`).Scan(&v)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

func (db *DB) SetMeta(ctx context.Context, key, value string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value)
	return err
}

func (db *DB) GetMeta(ctx context.Context, key string) (string, error) {
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}
