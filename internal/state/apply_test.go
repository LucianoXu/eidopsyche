package state

import (
	"context"
	"errors"
	"testing"
)

func TestApply_DispatchInvokesRegisteredHook(t *testing.T) {
	r := NewApplyRegistry()
	var gotOld, gotNew any
	called := false
	r.Register("config.heartbeat.interval", func(_ context.Context, _ any, old, new any) error {
		called = true
		gotOld, gotNew = old, new
		return nil
	})
	if err := r.Dispatch(context.Background(), nil, "config.heartbeat.interval", "2h", "1m"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("hook not invoked")
	}
	if gotOld != "2h" || gotNew != "1m" {
		t.Errorf("bad values old=%v new=%v", gotOld, gotNew)
	}
}

func TestApply_MissingHookIsNoOp(t *testing.T) {
	r := NewApplyRegistry()
	if err := r.Dispatch(context.Background(), nil, "config.log_level", nil, "info"); err != nil {
		t.Fatal(err)
	}
}

func TestApply_DepsPassedThrough(t *testing.T) {
	type fakeDeps struct{ token string }
	r := NewApplyRegistry()
	r.Register("p", func(_ context.Context, deps any, _, _ any) error {
		d, ok := deps.(*fakeDeps)
		if !ok {
			t.Errorf("deps cast failed: %T", deps)
			return nil
		}
		if d.token != "hello" {
			t.Errorf("token = %q", d.token)
		}
		return nil
	})
	_ = r.Dispatch(context.Background(), &fakeDeps{token: "hello"}, "p", nil, nil)
}

type rbErr struct{ allow bool }

func (e *rbErr) Error() string  { return "apply failed" }
func (e *rbErr) Rollback() bool { return e.allow }

func TestApply_RollbackerSurfacedNoRollback(t *testing.T) {
	r := NewApplyRegistry()
	r.Register("p", func(_ context.Context, _ any, _, _ any) error {
		return &rbErr{allow: false}
	})
	err := r.Dispatch(context.Background(), nil, "p", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var rb Rollbacker
	if !errors.As(err, &rb) {
		t.Fatalf("error should implement Rollbacker; got %T", err)
	}
	if rb.Rollback() {
		t.Error("Rollback() should be false")
	}
}

func TestApply_HasReportsRegistration(t *testing.T) {
	r := NewApplyRegistry()
	if r.Has("p") {
		t.Error("empty registry should not report Has(p)")
	}
	r.Register("p", func(context.Context, any, any, any) error { return nil })
	if !r.Has("p") {
		t.Error("after register, Has(p) should be true")
	}
}
