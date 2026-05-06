package ipc

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

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
