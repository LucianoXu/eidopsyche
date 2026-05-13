package promptcapture

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestProxyCapturesFirstPOST(t *testing.T) {
	srv := newCaptureServer()
	addr, shutdown, err := srv.listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer shutdown(context.Background())

	resp, err := http.Post(addr+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"x"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "message_start") {
		t.Fatalf("expected minimal SSE, got: %s", body)
	}
	select {
	case <-srv.done:
	default:
		t.Fatal("done channel not closed after first POST")
	}
	if !bytes.Contains(srv.captured, []byte(`"model":"x"`)) {
		t.Fatalf("captured=%q", srv.captured)
	}
}

func TestProxyIgnoresSecondPOST(t *testing.T) {
	srv := newCaptureServer()
	addr, shutdown, err := srv.listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer shutdown(context.Background())

	_, _ = http.Post(addr+"/v1/messages", "application/json",
		strings.NewReader(`{"first":true}`))
	_, _ = http.Post(addr+"/v1/messages", "application/json",
		strings.NewReader(`{"second":true}`))

	if !bytes.Contains(srv.captured, []byte(`"first":true`)) {
		t.Fatalf("first body not retained: %q", srv.captured)
	}
	if bytes.Contains(srv.captured, []byte(`"second":true`)) {
		t.Fatalf("second body leaked into captured: %q", srv.captured)
	}
}

func TestProxyRejectsNonPOST(t *testing.T) {
	srv := newCaptureServer()
	addr, shutdown, err := srv.listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer shutdown(context.Background())

	resp, err := http.Get(addr + "/v1/messages")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}
