package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/ipc"
)

// register synthetic methods for these tests so we don't depend on the
// real method set or any *Daemon state.
func init() {
	register("__test_echo", func(_ context.Context, _ *Daemon, _ *ipc.Conn, params json.RawMessage) (any, *ipc.Error) {
		var p struct{ X int }
		if len(params) > 0 {
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, &ipc.Error{Code: ipc.ErrInvalidParams, Message: err.Error()}
			}
		}
		return map[string]int{"x": p.X * 2}, nil
	})
	register("__test_boom", func(_ context.Context, _ *Daemon, _ *ipc.Conn, _ json.RawMessage) (any, *ipc.Error) {
		return nil, &ipc.Error{Code: ipc.ErrContactNotFound, Message: "alice"}
	})
}

func TestCall_UnknownMethod(t *testing.T) {
	d := &Daemon{}
	err := d.Call(context.Background(), "no.such.method", nil, nil)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) {
		t.Fatalf("want *ipc.Error, got %T (%v)", err, err)
	}
	if ipcErr.Code != ipc.ErrUnknownMethod {
		t.Fatalf("Code=%q, want %q", ipcErr.Code, ipc.ErrUnknownMethod)
	}
}

func TestCall_SuccessMarshalsRoundTrip(t *testing.T) {
	d := &Daemon{}
	var out struct {
		X int `json:"x"`
	}
	if err := d.Call(context.Background(), "__test_echo", map[string]int{"X": 21}, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.X != 42 {
		t.Fatalf("out.X=%d, want 42", out.X)
	}
}

func TestCall_NilOutDiscardsResult(t *testing.T) {
	d := &Daemon{}
	if err := d.Call(context.Background(), "__test_echo", map[string]int{"X": 5}, nil); err != nil {
		t.Fatalf("Call: %v", err)
	}
}

func TestCall_PropagatesIPCError(t *testing.T) {
	d := &Daemon{}
	err := d.Call(context.Background(), "__test_boom", nil, nil)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) {
		t.Fatalf("want *ipc.Error, got %T (%v)", err, err)
	}
	if ipcErr.Code != ipc.ErrContactNotFound {
		t.Fatalf("Code=%q, want %q", ipcErr.Code, ipc.ErrContactNotFound)
	}
	if ipcErr.Message != "alice" {
		t.Fatalf("Message=%q, want %q", ipcErr.Message, "alice")
	}
}

func TestCall_BadParamsMarshalReturnsInvalidParams(t *testing.T) {
	d := &Daemon{}
	// chan values are not marshalable by encoding/json.
	err := d.Call(context.Background(), "__test_echo", make(chan int), nil)
	var ipcErr *ipc.Error
	if !errors.As(err, &ipcErr) {
		t.Fatalf("want *ipc.Error, got %T (%v)", err, err)
	}
	if ipcErr.Code != ipc.ErrInvalidParams {
		t.Fatalf("Code=%q, want %q", ipcErr.Code, ipc.ErrInvalidParams)
	}
}
