//go:build !windows

package supervisor

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/wake"
)

func TestDrainBirth_RunsHandlerThenClearsBirthJSON(t *testing.T) {
	wakeD := t.TempDir()
	ontD := t.TempDir()
	if err := wake.WriteBirth(wakeD, wake.BirthSignal{V: 1, OperatorNpub: "npub1op", TriggeredAt: 1}); err != nil {
		t.Fatalf("seed birth: %v", err)
	}
	called := false
	handler := func(_ context.Context, _ wake.BirthSignal, ont string) error {
		called = true
		// Mimic the agent's last action: write born_at.
		if err := os.MkdirAll(filepath.Join(ont, "essence"), 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(ont, "essence/born_at"), []byte("1\n"), 0o600)
	}
	if err := drainBirthIfPresent(context.Background(), wakeD, ontD, handler); err != nil {
		t.Fatalf("drainBirthIfPresent: %v", err)
	}
	if !called {
		t.Errorf("handler not invoked")
	}
	if _, err := os.Stat(filepath.Join(wakeD, wake.BirthFileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("birth.json should be cleared after success")
	}
}

func TestDrainBirth_NoSignalIsNoop(t *testing.T) {
	wakeD := t.TempDir()
	ontD := t.TempDir()
	called := false
	handler := func(_ context.Context, _ wake.BirthSignal, _ string) error {
		called = true
		return nil
	}
	if err := drainBirthIfPresent(context.Background(), wakeD, ontD, handler); err != nil {
		t.Fatalf("drainBirthIfPresent: %v", err)
	}
	if called {
		t.Errorf("handler should not run when birth.json absent")
	}
}

func TestDrainBirth_RefusesWhenBornAtExists(t *testing.T) {
	wakeD := t.TempDir()
	ontD := t.TempDir()
	if err := wake.WriteBirth(wakeD, wake.BirthSignal{V: 1, OperatorNpub: "npub1op"}); err != nil {
		t.Fatalf("seed birth: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(ontD, "essence"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ontD, "essence/born_at"), []byte("999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	handler := func(_ context.Context, _ wake.BirthSignal, _ string) error {
		called = true
		return nil
	}
	if err := drainBirthIfPresent(context.Background(), wakeD, ontD, handler); err != nil {
		t.Fatalf("drainBirthIfPresent: %v", err)
	}
	if called {
		t.Errorf("handler should NOT run when essence/born_at already exists")
	}
	if _, err := os.Stat(filepath.Join(wakeD, wake.BirthFileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stale birth.json should be cleared even when refused")
	}
}

func TestDrainBirth_HandlerErrorLeavesBirthJSON(t *testing.T) {
	wakeD := t.TempDir()
	ontD := t.TempDir()
	if err := wake.WriteBirth(wakeD, wake.BirthSignal{V: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := func(_ context.Context, _ wake.BirthSignal, _ string) error {
		return errors.New("agent crashed")
	}
	err := drainBirthIfPresent(context.Background(), wakeD, ontD, handler)
	if err == nil {
		t.Errorf("expected error from handler")
	}
	// Crucial: birth.json must still be present so the next supervisor
	// iteration retries.
	if _, statErr := os.Stat(filepath.Join(wakeD, wake.BirthFileName)); statErr != nil {
		t.Errorf("birth.json should remain after handler error: %v", statErr)
	}
}
