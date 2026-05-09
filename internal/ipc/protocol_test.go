package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestError_ImplementsError(t *testing.T) {
	var err error = &Error{Code: ErrInvalidParams, Message: "bad"}
	if got, want := err.Error(), "INVALID_PARAMS: bad"; got != want {
		t.Fatalf("Error()=%q, want %q", got, want)
	}
}

func TestError_ErrorsAs(t *testing.T) {
	err := error(&Error{Code: ErrContactNotFound, Message: "alice"})
	var got *Error
	if !errors.As(err, &got) {
		t.Fatal("errors.As failed")
	}
	if got.Code != ErrContactNotFound {
		t.Fatalf("Code=%q, want %q", got.Code, ErrContactNotFound)
	}
}

type echoHandler struct{}

func (echoHandler) Handle(ctx context.Context, conn *Conn, req *Request) (any, *Error) {
	if req.Method == "echo" {
		return map[string]any{"echoed": json.RawMessage(req.Params)}, nil
	}
	if req.Method == "boom" {
		return nil, &Error{Code: ErrInternal, Message: "boom"}
	}
	return nil, &Error{Code: ErrUnknownMethod, Message: req.Method}
}

func TestRoundtrip(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "sock")
	srv := NewServer(sock, echoHandler{})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	time.Sleep(50 * time.Millisecond)

	cl, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	var got map[string]any
	ipcErr, err := cl.Call("echo", map[string]string{"hi": "there"}, &got)
	if err != nil || ipcErr != nil {
		t.Fatalf("call: %v %+v", err, ipcErr)
	}
	if _, ok := got["echoed"]; !ok {
		t.Fatalf("got %+v", got)
	}

	ipcErr, _ = cl.Call("boom", nil, nil)
	if ipcErr == nil || ipcErr.Code != ErrInternal {
		t.Fatalf("expected internal error, got %+v", ipcErr)
	}
}
