package identity

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/store"
	"github.com/LucianoXu/eidopsyche/internal/version"
)

// ErrAlreadyInitialized is returned when Bootstrap is called against a
// state directory that already holds a key file. Callers (gate init,
// firstcontact wizard) distinguish this from a generic I/O failure to
// route the user to the right next action.
var ErrAlreadyInitialized = errors.New("identity already initialized in this state directory")

// Bootstrap mkdirs stateDir, generates and saves a fresh keypair, opens
// and migrates state.db, sets the standard meta rows, inserts the home
// relay, and writes a default config.toml. It returns the new identity's
// npub. Callers (gate init, firstcontact wizard) share this single body.
//
// If a key file already exists at <stateDir>/key, returns
// ErrAlreadyInitialized — Bootstrap will not overwrite an existing
// identity. To reuse an externally-written key, use BootstrapWithExistingKey.
func Bootstrap(stateDir, label, homeRelay string) (string, error) {
	return bootstrap(stateDir, label, homeRelay, false)
}

// BootstrapWithExistingKey is like Bootstrap but expects <stateDir>/key
// to already exist (the caller wrote it). The pre-existing key is loaded
// to derive the npub. Used by the First Contact wizard so the host has
// the MindForm's npub before the in-container init-volume runs.
func BootstrapWithExistingKey(stateDir, label, homeRelay string) (string, error) {
	return bootstrap(stateDir, label, homeRelay, true)
}

func bootstrap(stateDir, label, homeRelay string, fromExisting bool) (string, error) {
	if homeRelay == "" {
		return "", errors.New("home relay must not be empty")
	}
	if u, err := url.Parse(homeRelay); err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return "", fmt.Errorf("invalid home relay %q (need ws:// or wss:// with a host)", homeRelay)
	}
	if label == "" {
		return "", errors.New("label must not be empty")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", err
	}
	keyPath := filepath.Join(stateDir, "key")

	var k *Keypair
	if fromExisting {
		loaded, err := LoadKey(keyPath)
		if err != nil {
			return "", fmt.Errorf("load existing key: %w", err)
		}
		k = loaded
	} else {
		if _, err := os.Stat(keyPath); err == nil {
			return "", ErrAlreadyInitialized
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		fresh, err := Generate()
		if err != nil {
			return "", err
		}
		if err := SaveKey(keyPath, fresh); err != nil {
			return "", err
		}
		k = fresh
	}

	dbPath := filepath.Join(stateDir, "state.db")
	db, err := store.Open(dbPath, false)
	if err != nil {
		return "", err
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		return "", err
	}
	if err := db.SetMeta(ctx, "owner_pubkey", k.PublicHex); err != nil {
		return "", err
	}
	if err := db.SetMeta(ctx, "created_at", strconv.FormatInt(time.Now().Unix(), 10)); err != nil {
		return "", err
	}
	if err := db.SetMeta(ctx, "mindgate_version", version.Version); err != nil {
		return "", err
	}
	if err := db.SetMeta(ctx, "label", label); err != nil {
		return "", err
	}
	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO own_relays(relay_url,role,added_at) VALUES(?,?,?)`,
		homeRelay, "home", time.Now().Unix()); err != nil {
		return "", err
	}

	cfg := config.Defaults()
	if err := config.Save(filepath.Join(stateDir, "config.toml"), cfg); err != nil {
		return "", err
	}

	return k.Npub, nil
}
