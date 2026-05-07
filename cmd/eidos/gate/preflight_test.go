package gate

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// freeListener returns a TCP listener bound to a random port on loopback.
// The returned listener is owned by the caller — close it to free the port.
func freeListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not bind a free port: %v", err)
	}
	return ln
}

// writeRelayConfig drops a minimal config.toml into dir with the supplied
// listen address.
func writeRelayConfig(t *testing.T, dir, listen string) {
	t.Helper()
	body := "[relay]\nlisten = \"" + listen + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
}

func TestPreflightRelayPort_Free(t *testing.T) {
	ln := freeListener(t)
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	dir := t.TempDir()
	writeRelayConfig(t, dir, addr)

	if err := preflightRelayPort(dir); err != nil {
		t.Errorf("free port should preflight clean; got: %v", err)
	}
}

func TestPreflightRelayPort_Conflict(t *testing.T) {
	ln := freeListener(t)
	defer ln.Close()
	addr := ln.Addr().String()

	dir := t.TempDir()
	writeRelayConfig(t, dir, addr)

	err := preflightRelayPort(dir)
	if err == nil {
		t.Fatal("expected error when port is already bound")
	}
	for _, want := range []string{
		"already in use",
		addr,
		"lsof",
		"eidos gate config set",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q in:\n%v", want, err)
		}
	}
}

func TestPreflightRelayPort_NoConfig(t *testing.T) {
	dir := t.TempDir()
	// Empty state dir — no config.toml. preflight must not error so the
	// downstream daemon/relay can surface the real "no key, run gate init"
	// problem.
	if err := preflightRelayPort(dir); err != nil {
		t.Errorf("missing config should be a silent skip; got: %v", err)
	}
}

func TestPreflightRelayPort_EmptyListen(t *testing.T) {
	dir := t.TempDir()
	// Config exists but [relay] section omits listen — fall through to the
	// package default; any failure to bind there is a real error, not a
	// preflight bug. We can't test that without knowing whether the default
	// port is free on the host, so just assert this code path doesn't panic.
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("# empty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = preflightRelayPort(dir) // either nil or an EADDRINUSE error against the default — both acceptable
}

func TestPortFromHostPort(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:22895": "22895",
		"0.0.0.0:8080":    "8080",
		"[::1]:9000":      "9000",
		"not-a-host-port": "not-a-host-port",
		"":                "",
	}
	for in, want := range cases {
		if got := portFromHostPort(in); got != want {
			t.Errorf("portFromHostPort(%q) = %q, want %q", in, got, want)
		}
	}
}
