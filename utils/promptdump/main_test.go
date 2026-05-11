package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

// startTestServer is a test-helper that starts the capture server via
// httptest and returns the *httptest.Server so the test can read its
// URL. Lives in the test file so the production binary doesn't import
// net/http/httptest. Production wiring uses captureServer.listen().
func startTestServer(_ testing.TB, s *captureServer) *httptest.Server {
	return httptest.NewServer(s.handler())
}

// TestCaptureServer_HappyPath: a POST to /v1/messages?beta=true with a
// known JSON body returns 200 + valid SSE, and the body is captured
// verbatim into the server's captured field.
func TestCaptureServer_HappyPath(t *testing.T) {
	srv := newCaptureServer()
	httpSrv := startTestServer(t, srv)
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
}

// TestCaptureServer_OnlyMessagesCaptures: POSTs to non-/v1/messages
// paths must not populate srv.captured and must not signal srv.done.
func TestCaptureServer_OnlyMessagesCaptures(t *testing.T) {
	srv := newCaptureServer()
	httpSrv := startTestServer(t, srv)
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
	httpSrv := startTestServer(t, srv)
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

// TestCaptureServer_ListenLocalhost: production-side listen helper binds
// to 127.0.0.1 on a random port, returns a working URL, and is
// shutdownable.
func TestCaptureServer_ListenLocalhost(t *testing.T) {
	srv := newCaptureServer()
	addr, shutdown, err := srv.listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer shutdown(context.Background())

	if !strings.HasPrefix(addr, "http://127.0.0.1:") {
		t.Errorf("addr = %q, want http://127.0.0.1: prefix", addr)
	}

	resp, err := http.Post(addr+"/v1/messages",
		"application/json", strings.NewReader(`{"ping":true}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// TestFilterEnv: pre-existing entries with the named keys are dropped;
// entries without `=` are preserved; unrelated keys are preserved.
func TestFilterEnv(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_BASE_URL=https://api.anthropic.com",
		"FOO=bar",
		"ANTHROPIC_API_KEY=sk-real",
		"WEIRD_ENTRY_NO_EQUALS",
		"ANTHROPIC_BASE_URL=second-occurrence", // duplicate, must also go
	}
	got := filterEnv(in, "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY")
	want := []string{"PATH=/usr/bin", "FOO=bar", "WEIRD_ENTRY_NO_EQUALS"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filterEnv(...) = %v, want %v", got, want)
	}
}

// TestRunCapture_Smoke: end-to-end against the real claude binary.
// Opt-in via PROMPTDUMP_SMOKE=1 because a system can have claude on PATH
// but be unauthenticated / misconfigured, in which case the child times
// out without ever POSTing /v1/messages. Also skipped in -short mode
// and when claude is not on PATH.
func TestRunCapture_Smoke(t *testing.T) {
	if testing.Short() {
		t.Skip("smoke test requires real claude binary")
	}
	if os.Getenv("PROMPTDUMP_SMOKE") != "1" {
		t.Skip("set PROMPTDUMP_SMOKE=1 to enable")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	jsonBytes, _, err := runCapture(ctx, runOpts{
		Prompt:      "ping",
		ExtraArgs:   nil,
		NoPOSTAfter: 8 * time.Second,
		Verbose:     false,
	})
	if err != nil {
		t.Fatalf("runCapture: %v", err)
	}

	var envelope map[string]any
	if err := json.Unmarshal(jsonBytes, &envelope); err != nil {
		t.Fatalf("envelope not valid JSON: %v\n%s", err, jsonBytes)
	}
	req, ok := envelope["request"].(map[string]any)
	if !ok {
		t.Fatalf("envelope.request not an object; envelope=%v", envelope)
	}
	if _, has := req["model"]; !has {
		t.Errorf("envelope.request.model missing; keys=%v", mapKeys(req))
	}
	if _, has := req["system"]; !has {
		t.Errorf("envelope.request.system missing; keys=%v", mapKeys(req))
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestParseFlags: covers the CLI surface contract independent of HTTP /
// exec. Verifies -- splits promptdump flags from claude passthrough,
// and default values.
func TestParseFlags(t *testing.T) {
	tests := []struct {
		name     string
		argv     []string
		wantOpts runOpts
		wantOut  string
	}{
		{
			name:     "defaults",
			argv:     []string{"promptdump"},
			wantOpts: runOpts{Prompt: "ping", NoPOSTAfter: 8 * time.Second},
			wantOut:  "",
		},
		{
			name:     "prompt and output",
			argv:     []string{"promptdump", "-p", "hi", "-o", "/tmp/x.json"},
			wantOpts: runOpts{Prompt: "hi", NoPOSTAfter: 8 * time.Second},
			wantOut:  "/tmp/x.json",
		},
		{
			name: "passthrough after double-dash",
			argv: []string{"promptdump", "-v", "--", "--model", "sonnet", "--effort", "medium"},
			wantOpts: runOpts{
				Prompt:      "ping",
				ExtraArgs:   []string{"--model", "sonnet", "--effort", "medium"},
				NoPOSTAfter: 8 * time.Second,
				Verbose:     true,
			},
			wantOut: "",
		},
		{
			name:     "keep-going",
			argv:     []string{"promptdump", "--keep-going"},
			wantOpts: runOpts{Prompt: "ping", NoPOSTAfter: 8 * time.Second, KeepGoing: true},
			wantOut:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, out, err := parseFlags(tt.argv)
			if err != nil {
				t.Fatalf("parseFlags: %v", err)
			}
			if !reflect.DeepEqual(opts, tt.wantOpts) {
				t.Errorf("opts = %+v, want %+v", opts, tt.wantOpts)
			}
			if out != tt.wantOut {
				t.Errorf("out = %q, want %q", out, tt.wantOut)
			}
		})
	}
}

// TestBuildEnvelopeMap: buildEnvelopeMap returns the same shape that
// buildEnvelope serializes, but as a map[string]any rather than bytes.
// The renderer (added in a later task) will consume the map directly.
func TestBuildEnvelopeMap(t *testing.T) {
	captured := []byte(`{"model":"claude-sonnet-4-7","system":"foo"}`)
	meta := envelopeMeta{
		ClaudeVersion: "2.1.138",
		ClaudePath:    "/usr/bin/claude",
		ClaudeArgs:    []string{"--model", "sonnet"},
		Host:          hostInfo{Platform: "linux", CWD: "/data/eidopsyche"},
		CapturedAt:    time.Date(2026, 5, 11, 17, 23, 45, 0, time.UTC),
	}
	got, err := buildEnvelopeMap(meta, captured)
	if err != nil {
		t.Fatalf("buildEnvelopeMap: %v", err)
	}
	if got["captured_at"] != "2026-05-11T17:23:45Z" {
		t.Errorf("captured_at = %v", got["captured_at"])
	}
	if got["claude_version"] != "2.1.138" {
		t.Errorf("claude_version = %v", got["claude_version"])
	}
	req, ok := got["request"].(map[string]any)
	if !ok {
		t.Fatalf("request not an object: %T", got["request"])
	}
	if req["model"] != "claude-sonnet-4-7" {
		t.Errorf("request.model = %v", req["model"])
	}
}

// TestRenderMarkdown_HappyPath: render a fixture envelope that
// exercises every section. Assert real newlines are preserved
// (not the two-char escape), section headings exist, and fenced
// blocks balance.
func TestRenderMarkdown_HappyPath(t *testing.T) {
	env := map[string]any{
		"captured_at":    "2026-05-11T17:23:45Z",
		"claude_version": "2.1.138 (Claude Code)",
		"claude_path":    "/usr/bin/claude",
		"claude_args":    []any{"--model", "sonnet", "-p", "ping"},
		"host":           map[string]any{"platform": "linux", "cwd": "/data/eidopsyche"},
		"request": map[string]any{
			"model":      "claude-sonnet-4-6",
			"max_tokens": float64(32000),
			"stream":     true,
			"system": []any{
				map[string]any{
					"type":          "text",
					"text":          "line1\nline2\nline3",
					"cache_control": map[string]any{"type": "ephemeral"},
				},
				map[string]any{
					"type": "text",
					"text": "second segment",
				},
			},
			"tools": []any{
				map[string]any{
					"name":        "Read",
					"description": "Reads a file from the local filesystem.\nFull description continues...",
				},
				map[string]any{
					"name":        "Bash",
					"description": "Runs a shell command.",
				},
			},
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "text", "text": "hello\nworld"},
					},
				},
			},
		},
	}
	out, err := renderMarkdown(env)
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}

	for _, want := range []string{
		"# promptdump capture",
		"2026-05-11T17:23:45Z",
		"2.1.138 (Claude Code)",
		"--model sonnet -p ping",
		"linux",
		"/data/eidopsyche",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in metadata section", want)
		}
	}

	if !strings.Contains(out, "claude-sonnet-4-6") {
		t.Error("model id missing from output")
	}
	if !strings.Contains(out, "## System prompt (2 segments)") {
		t.Error("system-prompt heading missing")
	}
	if !strings.Contains(out, "line1\nline2\nline3") {
		t.Error("system-segment real newlines not preserved (got JSON-escaped form?)")
	}
	if !strings.Contains(out, `cache_control: {"type":"ephemeral"}`) {
		t.Error("cache_control annotation missing from segment heading")
	}
	if !strings.Contains(out, "## Tools (2)") {
		t.Error("tools heading missing")
	}
	if !strings.Contains(out, "**`Read`**") || !strings.Contains(out, "Reads a file") {
		t.Error("Read tool not listed in bullet form")
	}
	if !strings.Contains(out, "<details><summary>Full tool schemas</summary>") {
		t.Error("collapsible tool-schemas block missing")
	}
	if !strings.Contains(out, "## First user message") {
		t.Error("user-message heading missing")
	}
	if !strings.Contains(out, "hello\nworld") {
		t.Error("user-message real newlines not preserved")
	}
	if fences := strings.Count(out, "```"); fences%2 != 0 {
		t.Errorf("unbalanced fenced blocks: %d ``` markers", fences)
	}
}

// TestRenderMarkdown_MissingFields: empty/absent fields must not panic
// and must produce sensible (possibly empty) sections.
func TestRenderMarkdown_MissingFields(t *testing.T) {
	env := map[string]any{
		"captured_at":    "2026-05-11T17:23:45Z",
		"claude_version": "2.1.138",
		"request": map[string]any{
			"model": "claude-opus-4-7",
		},
	}
	out, err := renderMarkdown(env)
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	for _, want := range []string{
		"# promptdump capture",
		"## System prompt (0 segments)",
		"## Tools (0)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in degenerate-input output", want)
		}
	}
	if strings.Contains(out, "## First user message") {
		t.Error("user-message section rendered despite no messages")
	}
}

// TestRenderMarkdown_RawBodyFallback: if the envelope contains a raw_body
// (parse failure), the renderer emits a "Raw body" section instead of
// the structured request sections.
func TestRenderMarkdown_RawBodyFallback(t *testing.T) {
	env := map[string]any{
		"captured_at":    "2026-05-11T17:23:45Z",
		"claude_version": "2.1.138",
		"raw_body":       "not json at all",
		"parse_error":    "invalid character 'n'",
	}
	out, err := renderMarkdown(env)
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	if !strings.Contains(out, "## Raw body (failed to parse as JSON)") {
		t.Error("raw-body section missing")
	}
	if !strings.Contains(out, "not json at all") {
		t.Error("raw body content missing")
	}
	if strings.Contains(out, "## Request") {
		t.Error("structured request section rendered despite parse failure")
	}
}

// TestRenderMarkdown_MarkdownInDescription: a tool description
// containing Markdown-special characters must not break the
// document structure. Real Claude Code tools have ** and ` in their
// docs.
func TestRenderMarkdown_MarkdownInDescription(t *testing.T) {
	env := map[string]any{
		"request": map[string]any{
			"tools": []any{
				map[string]any{
					"name":        "Edit",
					"description": "Performs **exact** string replacements in `files`.\n\nUsage: ...",
				},
			},
		},
	}
	out, err := renderMarkdown(env)
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	if !strings.Contains(out, "**`Edit`**") {
		t.Error("tool name bullet missing")
	}
	if !strings.Contains(out, "Performs **exact** string replacements in `files`.") {
		t.Error("tool description content missing")
	}
	if strings.Count(out, "```")%2 != 0 {
		t.Errorf("unbalanced fenced blocks: description's backticks broke structure")
	}
}

// TestDispatchOutput: covers the -o extension dispatch table.
// jsonBytes and env are dummy values; we only assert which paths
// and content kinds the dispatcher produces.
func TestDispatchOutput(t *testing.T) {
	jsonBytes := []byte(`{"captured_at":"x"}`)
	env := map[string]any{
		"captured_at": "x",
		"request":     map[string]any{},
	}

	tests := []struct {
		name      string
		outPath   string
		wantPaths []string
		wantKinds []string // "json" or "md"
	}{
		{"stdout", "", []string{""}, []string{"json"}},
		{"json only", "snap.json", []string{"snap.json"}, []string{"json"}},
		{"md only", "snap.md", []string{"snap.md"}, []string{"md"}},
		{"basename → both", "snap", []string{"snap.json", "snap.md"}, []string{"json", "md"}},
		{"other ext → both", "snap.txt", []string{"snap.txt.json", "snap.txt.md"}, []string{"json", "md"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs, err := dispatchOutput(jsonBytes, env, tt.outPath)
			if err != nil {
				t.Fatalf("dispatchOutput: %v", err)
			}
			if len(jobs) != len(tt.wantPaths) {
				t.Fatalf("got %d jobs, want %d: %+v", len(jobs), len(tt.wantPaths), jobs)
			}
			for i, j := range jobs {
				if j.path != tt.wantPaths[i] {
					t.Errorf("job[%d].path = %q, want %q", i, j.path, tt.wantPaths[i])
				}
				switch tt.wantKinds[i] {
				case "json":
					if !bytes.Equal(j.content, jsonBytes) {
						t.Errorf("job[%d] content not JSON bytes", i)
					}
				case "md":
					if !bytes.Contains(j.content, []byte("# promptdump capture")) {
						t.Errorf("job[%d] content not Markdown", i)
					}
				}
			}
		})
	}
}

// TestBuildEnvelopeMap_RoundTripsForRenderer: regression for a real
// bug — buildEnvelopeMap originally stored claude_args as []string and
// host as a hostInfo struct, but renderMarkdown only handled []any and
// map[string]any. The metadata lines silently dropped from production
// output. The map must now be JSON-normalized so all values match
// what renderMarkdown's type switches expect.
func TestBuildEnvelopeMap_RoundTripsForRenderer(t *testing.T) {
	meta := envelopeMeta{
		CapturedAt:    time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC),
		ClaudeVersion: "2.1.139",
		ClaudePath:    "/usr/bin/claude",
		ClaudeArgs:    []string{"--model", "sonnet", "-p", "ping"},
		Host:          hostInfo{Platform: "linux", CWD: "/data/eidopsyche"},
	}
	env, err := buildEnvelopeMap(meta, []byte(`{"model":"claude-sonnet-4-7"}`))
	if err != nil {
		t.Fatalf("buildEnvelopeMap: %v", err)
	}
	if _, ok := env["claude_args"].([]any); !ok {
		t.Errorf("claude_args type = %T, want []any (JSON-normalized)", env["claude_args"])
	}
	if _, ok := env["host"].(map[string]any); !ok {
		t.Errorf("host type = %T, want map[string]any (JSON-normalized)", env["host"])
	}
	md, err := renderMarkdown(env)
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	for _, want := range []string{
		"**Claude args:** `--model sonnet -p ping`",
		"**Host:** linux · cwd=`/data/eidopsyche`",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in rendered markdown — metadata line silently dropped?\n%s", want, md)
		}
	}
}

// TestRenderMarkdown_ClaudePath: claude_path must appear in the
// metadata block. Regression guard for the original Markdown
// renderer, which omitted this field entirely.
func TestRenderMarkdown_ClaudePath(t *testing.T) {
	env := map[string]any{
		"claude_path":    "/usr/bin/claude",
		"claude_version": "2.1.139",
	}
	out, err := renderMarkdown(env)
	if err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	if !strings.Contains(out, "**Claude path:** `/usr/bin/claude`") {
		t.Errorf("missing claude_path line in metadata:\n%s", out)
	}
}
