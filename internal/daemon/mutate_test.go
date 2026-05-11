package daemon

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/config"
	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

func TestMutate_HappyPath(t *testing.T) {
	d := newTestDaemon(t)
	d.SetContext(config.ContainerCtx)

	applied := atomic.Bool{}
	d.RegisterApply("p", func(_ context.Context, _ any, old, new any) error {
		applied.Store(true)
		if old != "old" || new != "new" {
			t.Errorf("bad values old=%v new=%v", old, new)
		}
		return nil
	})

	written := atomic.Bool{}
	err := d.Mutate(context.Background(), "p", config.ContainerCtx,
		func() (any, any, error) {
			written.Store(true)
			return "old", "new", nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !written.Load() {
		t.Error("write fn not invoked")
	}
	if !applied.Load() {
		t.Error("apply not invoked")
	}
}

func TestMutate_ContextMismatchOnHost(t *testing.T) {
	d := newTestDaemon(t)
	d.SetContext(config.HostCtx)

	called := atomic.Bool{}
	err := d.Mutate(context.Background(), "config.heartbeat.interval", config.ContainerCtx,
		func() (any, any, error) {
			called.Store(true)
			return nil, nil, nil
		})
	if err == nil {
		t.Fatal("expected error")
	}
	if called.Load() {
		t.Error("write fn must not run when context mismatches")
	}
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) || ipcErr.Code != ipc.ErrContextMismatch {
		t.Fatalf("expected ErrContextMismatch; got %v", err)
	}
	if !strings.Contains(ipcErr.Message, "forge config") {
		t.Errorf("error should hint at forge config; got %q", ipcErr.Message)
	}
}

func TestMutate_ContextMismatchOnContainerForHostOnlyKey(t *testing.T) {
	d := newTestDaemon(t)
	d.SetContext(config.ContainerCtx)

	err := d.Mutate(context.Background(), "p", config.HostCtx,
		func() (any, any, error) { return nil, nil, nil })
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) || ipcErr.Code != ipc.ErrContextMismatch {
		t.Fatalf("expected ErrContextMismatch; got %v", err)
	}
}

func TestMutate_BothCtxAllowedFromEither(t *testing.T) {
	for _, ctx := range []config.Context{config.HostCtx, config.ContainerCtx} {
		ctx := ctx
		t.Run(strings.ReplaceAll(strings.ReplaceAll(string(rune(ctx)), "\x01", "Host"), "\x02", "Container"), func(t *testing.T) {
			d := newTestDaemon(t)
			d.SetContext(ctx)
			err := d.Mutate(context.Background(), "p", config.BothCtx,
				func() (any, any, error) { return nil, nil, nil })
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMutate_WriteErrorAborts(t *testing.T) {
	d := newTestDaemon(t)
	d.SetContext(config.ContainerCtx)

	applied := atomic.Bool{}
	d.RegisterApply("p", func(_ context.Context, _ any, _, _ any) error {
		applied.Store(true)
		return nil
	})

	want := errors.New("disk full")
	err := d.Mutate(context.Background(), "p", config.ContainerCtx,
		func() (any, any, error) { return nil, nil, want })
	if !errors.Is(err, want) {
		t.Fatalf("expected wrapped %v; got %v", want, err)
	}
	if applied.Load() {
		t.Error("apply must not run when write fails")
	}
}

func TestMutate_ApplyFailureRollsBackByDefault(t *testing.T) {
	d := newTestDaemon(t)
	d.SetContext(config.ContainerCtx)

	writeCalls := atomic.Int32{}
	d.RegisterApply("p", func(_ context.Context, _ any, _, _ any) error {
		return errors.New("apply boom")
	})

	err := d.Mutate(context.Background(), "p", config.ContainerCtx,
		func() (any, any, error) {
			writeCalls.Add(1)
			return "old", "new", nil
		})
	if err == nil {
		t.Fatal("expected error")
	}
	if writeCalls.Load() != 2 {
		t.Errorf("write should run twice (initial + rollback); got %d", writeCalls.Load())
	}
}

type noRollbackErr struct{}

func (noRollbackErr) Error() string  { return "irreversible" }
func (noRollbackErr) Rollback() bool { return false }

func TestMutate_ApplyFailureRollbackerOptOut(t *testing.T) {
	d := newTestDaemon(t)
	d.SetContext(config.ContainerCtx)

	writeCalls := atomic.Int32{}
	d.RegisterApply("p", func(_ context.Context, _ any, _, _ any) error {
		return noRollbackErr{}
	})

	err := d.Mutate(context.Background(), "p", config.ContainerCtx,
		func() (any, any, error) {
			writeCalls.Add(1)
			return "old", "new", nil
		})
	if err == nil {
		t.Fatal("expected error")
	}
	if writeCalls.Load() != 1 {
		t.Errorf("write should run once (no rollback); got %d", writeCalls.Load())
	}
}

func TestMutate_RollbackAlsoFailsReturnsInconsistent(t *testing.T) {
	d := newTestDaemon(t)
	d.SetContext(config.ContainerCtx)

	writeCalls := atomic.Int32{}
	d.RegisterApply("p", func(_ context.Context, _ any, _, _ any) error {
		return errors.New("apply boom")
	})

	err := d.Mutate(context.Background(), "p", config.ContainerCtx,
		func() (any, any, error) {
			n := writeCalls.Add(1)
			if n == 2 { // rollback call also fails
				return nil, nil, errors.New("disk dead")
			}
			return "old", "new", nil
		})
	if err == nil {
		t.Fatal("expected error")
	}
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) || ipcErr.Code != ipc.ErrInconsistent {
		t.Fatalf("expected ErrInconsistent; got %v", err)
	}
}

func TestMutate_RequiresZeroSkipsContextCheck(t *testing.T) {
	d := newTestDaemon(t)
	d.SetContext(0) // explicitly clear the default
	err := d.Mutate(context.Background(), "p", 0,
		func() (any, any, error) { return nil, nil, nil })
	if err != nil {
		t.Fatal(err)
	}
}
