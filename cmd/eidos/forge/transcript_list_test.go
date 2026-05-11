package forge

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
)

// transcriptsFixture redirects transcriptsDir to a temp location and
// restores it on cleanup.
func transcriptsFixture(t *testing.T) string {
	t.Helper()
	prev := transcriptsDir
	transcriptsDir = t.TempDir()
	t.Cleanup(func() { transcriptsDir = prev })
	return transcriptsDir
}

func TestTranscriptList_EmptyHuman(t *testing.T) {
	transcriptsFixture(t)
	cmd := newTranscriptListCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "no wakes") {
		t.Errorf("expected empty hint, got %q", buf.String())
	}
}

func TestTranscriptList_JSONShape(t *testing.T) {
	dir := transcriptsFixture(t)
	s, _ := transcript.NewStore(dir)
	cost := 0.012
	if err := s.WriteIndex(transcript.Index{V: 1, Wakes: []transcript.Entry{
		{ID: "abc", Reason: "mindgate", StartedAt: 1700000000, EndedAt: 1700000042, OK: true, CostUSD: &cost},
	}}); err != nil {
		t.Fatal(err)
	}
	cmd := newTranscriptListCmd()
	cmd.SetArgs([]string{"--json"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var idx transcript.Index
	if err := json.Unmarshal(buf.Bytes(), &idx); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, buf.String())
	}
	if len(idx.Wakes) != 1 || idx.Wakes[0].ID != "abc" {
		t.Errorf("index roundtrip: %+v", idx)
	}
}

func TestTranscriptList_HumanTable(t *testing.T) {
	dir := transcriptsFixture(t)
	s, _ := transcript.NewStore(dir)
	cost := 0.05
	s.WriteIndex(transcript.Index{V: 1, Wakes: []transcript.Entry{
		{ID: "abcdef1234", Reason: "mindgate", StartedAt: 1700000000, EndedAt: 1700000010, OK: true, CostUSD: &cost},
		{ID: "9876fedcba", Reason: "planned", StartedAt: 1699000000, EndedAt: 1699000000, OK: false, ExitCode: -1},
	}})
	cmd := newTranscriptListCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "ID         SESSION    REASON") {
		t.Errorf("header missing: %q", out)
	}
	if !strings.Contains(out, "abcdef12") {
		t.Errorf("wake id (truncated to 8) missing: %q", out)
	}
	if !strings.Contains(out, "$0.0500") {
		t.Errorf("cost missing: %q", out)
	}
	if !strings.Contains(out, "crashed") {
		t.Errorf("crashed status missing: %q", out)
	}
}

func TestTranscriptList_FailKind(t *testing.T) {
	dir := transcriptsFixture(t)
	s, _ := transcript.NewStore(dir)
	s.WriteIndex(transcript.Index{V: 1, Wakes: []transcript.Entry{
		{ID: "abc12345", Reason: "heartbeat", StartedAt: 1700000000, EndedAt: 1700000001, OK: false, ExitCode: 1, FailKind: "auth"},
		{ID: "def67890", Reason: "heartbeat", StartedAt: 1700000002, EndedAt: 1700000003, OK: false, ExitCode: 1}, // no FailKind → fallback
	}})
	cmd := newTranscriptListCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "failed(auth)") {
		t.Errorf("expected failed(auth), got:\n%s", out)
	}
	if !strings.Contains(out, "failed(1)") {
		t.Errorf("expected failed(1) fallback for entry without FailKind, got:\n%s", out)
	}
}

func TestTranscriptList_Limit(t *testing.T) {
	dir := transcriptsFixture(t)
	s, _ := transcript.NewStore(dir)
	wakes := make([]transcript.Entry, 5)
	for i := range wakes {
		wakes[i] = transcript.Entry{ID: string(rune('a' + i)), StartedAt: int64(i)}
	}
	s.WriteIndex(transcript.Index{V: 1, Wakes: wakes})

	cmd := newTranscriptListCmd()
	cmd.SetArgs([]string{"--json", "--limit", "2"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var idx transcript.Index
	json.Unmarshal(buf.Bytes(), &idx)
	if len(idx.Wakes) != 2 {
		t.Errorf("limit=2 not respected: got %d wakes", len(idx.Wakes))
	}
}
