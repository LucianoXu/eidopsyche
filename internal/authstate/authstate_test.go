package authstate_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/authstate"
)

func TestWriteRead_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth_required.json")
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	if err := authstate.WriteAt(path, now); err != nil {
		t.Fatalf("WriteAt: %v", err)
	}
	got, err := authstate.ReadAt(path)
	if err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if got == nil {
		t.Fatal("ReadAt returned nil after Write")
	}
	if got.Since != now.Unix() {
		t.Errorf("Since = %d, want %d", got.Since, now.Unix())
	}
}

func TestRead_AbsentReturnsNilNil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth_required.json")
	got, err := authstate.ReadAt(path)
	if err != nil {
		t.Fatalf("ReadAt absent: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil State for absent file, got %+v", got)
	}
}

func TestClear_RemovesMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth_required.json")
	if err := authstate.WriteAt(path, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := authstate.ClearAt(path); err != nil {
		t.Fatalf("ClearAt: %v", err)
	}
	got, err := authstate.ReadAt(path)
	if err != nil || got != nil {
		t.Errorf("after Clear: got=%+v err=%v, want nil/nil", got, err)
	}
}

func TestClear_AbsentIsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth_required.json")
	if err := authstate.ClearAt(path); err != nil {
		t.Errorf("Clear on absent file should be no-op, got %v", err)
	}
}
