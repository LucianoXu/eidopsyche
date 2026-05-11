package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestCaptureServer_HappyPath: a POST to /v1/messages?beta=true with a
// known JSON body returns 200 + valid SSE, and the body is captured
// verbatim into the server's captured field.
func TestCaptureServer_HappyPath(t *testing.T) {
	srv := newCaptureServer()
	httpSrv := srv.start(t)
	defer httpSrv.Close()

	want := map[string]any{
		"model":    "claude-sonnet-4-7",
		"system":   "you are a fox spirit",
		"messages": []any{map[string]any{"role": "user", "content": "ping"}},
	}
	wantBody, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp, err := http.Post(httpSrv.URL+"/v1/messages?beta=true",
		"application/json", bytes.NewReader(wantBody))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream prefix", ct)
	}
	respBody, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(respBody), "message_stop") {
		t.Errorf("SSE body missing message_stop terminator:\n%s", respBody)
	}

	select {
	case <-srv.done:
	case <-time.After(time.Second):
		t.Fatal("capture done not signaled within 1s")
	}

	if string(srv.captured) != string(wantBody) {
		t.Errorf("captured body mismatch.\nwant: %s\ngot:  %s", wantBody, srv.captured)
	}

	_ = context.TODO()
}

// TestCaptureServer_OnlyMessagesCaptures: POSTs to non-/v1/messages
// paths must not populate srv.captured and must not signal srv.done.
func TestCaptureServer_OnlyMessagesCaptures(t *testing.T) {
	srv := newCaptureServer()
	httpSrv := srv.start(t)
	defer httpSrv.Close()

	for _, path := range []string{"/v1/models", "/v1/mcp_servers", "/v1/whatever"} {
		resp, err := http.Post(httpSrv.URL+path, "application/json",
			strings.NewReader(`{"probe":true}`))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("POST %s status = %d, want 200", path, resp.StatusCode)
		}
	}

	if srv.captured != nil {
		t.Errorf("captured populated by non-messages traffic: %s", srv.captured)
	}
	select {
	case <-srv.done:
		t.Fatal("done channel signaled by non-messages traffic")
	default:
	}
}

// TestCaptureServer_BodyTooLarge: a POST with a body above the
// maxBodyBytes cap must return 413 and leave srv.captured nil.
func TestCaptureServer_BodyTooLarge(t *testing.T) {
	srv := newCaptureServer()
	httpSrv := srv.start(t)
	defer httpSrv.Close()

	big := bytes.Repeat([]byte("x"), maxBodyBytes+1)
	resp, err := http.Post(httpSrv.URL+"/v1/messages",
		"application/octet-stream", bytes.NewReader(big))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", resp.StatusCode)
	}
	if srv.captured != nil {
		t.Errorf("captured populated despite oversize body")
	}
}

// TestBuildEnvelope: wraps a captured body with metadata and returns a
// well-formed JSON document. captured_at must be RFC3339; request must
// be the parsed body, not a string blob.
func TestBuildEnvelope(t *testing.T) {
	captured := []byte(`{"model":"claude-sonnet-4-7","system":"foo"}`)
	meta := envelopeMeta{
		ClaudeVersion: "2.1.138",
		ClaudePath:    "/usr/bin/claude",
		ClaudeArgs:    []string{"--model", "sonnet", "-p", "ping"},
		Host:          hostInfo{Platform: "linux", CWD: "/data/eidopsyche"},
		CapturedAt:    time.Date(2026, 5, 11, 17, 23, 45, 0, time.UTC),
	}
	out, err := buildEnvelope(meta, captured)
	if err != nil {
		t.Fatalf("buildEnvelope: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out)
	}
	if got := parsed["captured_at"]; got != "2026-05-11T17:23:45Z" {
		t.Errorf("captured_at = %v, want 2026-05-11T17:23:45Z", got)
	}
	req, ok := parsed["request"].(map[string]any)
	if !ok {
		t.Fatalf("request not an object: %T", parsed["request"])
	}
	if req["model"] != "claude-sonnet-4-7" {
		t.Errorf("request.model = %v, want claude-sonnet-4-7", req["model"])
	}
}

// TestBuildEnvelope_RawBodyFallback: if the captured body is not valid
// JSON, the envelope must include it under raw_body and omit request.
func TestBuildEnvelope_RawBodyFallback(t *testing.T) {
	out, err := buildEnvelope(envelopeMeta{
		ClaudeVersion: "2.1.138",
		CapturedAt:    time.Now().UTC(),
	}, []byte("not json at all"))
	if err != nil {
		t.Fatalf("buildEnvelope: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("envelope itself not JSON: %v", err)
	}
	if _, has := parsed["request"]; has {
		t.Error("request field present on malformed body; should be absent")
	}
	if parsed["raw_body"] != "not json at all" {
		t.Errorf("raw_body = %v, want 'not json at all'", parsed["raw_body"])
	}
}
