package relay

import (
	"os"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/identity"
	"github.com/LucianoXu/eidopsyche/internal/relaycfg"
)

// encodeNpubForTest is a thin wrapper so tests don't reach into identity
// directly more than once.
func encodeNpubForTest(hexPub string) (string, error) {
	return identity.EncodeNpub(hexPub)
}

// 64-char hex pubkey used across paired-mode tests. Arbitrary value;
// matches the on-the-wire shape of an event PubKey field.
const testOwnerHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestInitWritesConfigPaired(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOpts{
		dir:    dir,
		mode:   "paired",
		listen: "127.0.0.1:9001",
		owner:  testOwnerHex,
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := relaycfg.Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Relay.Mode != "paired" || cfg.Relay.OwnerPubkey != testOwnerHex {
		t.Errorf("unexpected cfg: %+v", cfg.Relay)
	}
	if _, err := os.Stat(relaycfg.EventStorePath(dir)); err != nil {
		t.Errorf("event store dir not created: %v", err)
	}
}

func TestInitAcceptsNpubOwner(t *testing.T) {
	dir := t.TempDir()
	// Bech32-encoded form of testOwnerHex via internal/identity.
	hexStr := testOwnerHex
	npub, err := encodeNpubForTest(hexStr)
	if err != nil {
		t.Fatal(err)
	}
	if err := runInit(initOpts{
		dir:    dir,
		mode:   "paired",
		listen: "127.0.0.1:9001",
		owner:  npub,
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := relaycfg.Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Relay.OwnerPubkey != hexStr {
		t.Errorf("npub --owner not decoded to hex: got %q want %q", cfg.Relay.OwnerPubkey, hexStr)
	}
}

func TestInitRejectsBadOwner(t *testing.T) {
	dir := t.TempDir()
	// 14-char string is neither npub nor 64-char hex.
	err := runInit(initOpts{dir: dir, mode: "paired", listen: ":7777", owner: "not-a-pubkey"})
	if err == nil {
		t.Fatal("expected malformed --owner to fail")
	}
}

func TestInitRejectsPairedWithoutOwner(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOpts{dir: dir, mode: "paired", listen: ":7777"}); err == nil {
		t.Fatal("expected paired-without-owner to fail")
	}
}

func TestInitRejectsPublicWithOwner(t *testing.T) {
	dir := t.TempDir()
	err := runInit(initOpts{dir: dir, mode: "public", listen: ":7777", owner: "abc"})
	if err == nil {
		t.Fatal("expected public-with-owner to fail")
	}
}

func TestInitRejectsExistingConfig(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(initOpts{dir: dir, mode: "public", listen: ":7777"}); err != nil {
		t.Fatal(err)
	}
	if err := runInit(initOpts{dir: dir, mode: "public", listen: ":7777"}); err == nil {
		t.Fatal("expected re-init to fail without --force")
	}
	if err := runInit(initOpts{dir: dir, mode: "public", listen: ":8888", force: true}); err != nil {
		t.Fatalf("--force should overwrite: %v", err)
	}
	// Verify the listen address actually changed.
	cfg, err := relaycfg.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Relay.Listen != ":8888" {
		t.Errorf("force re-init did not update listen: %q", cfg.Relay.Listen)
	}
}
