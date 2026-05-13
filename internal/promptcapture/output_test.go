package promptcapture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteOutputs_JSONExtension(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.json")
	meta := EnvelopeMeta{CapturedAt: time.Unix(0, 0).UTC()}
	m, _ := BuildEnvelopeMap(meta, []byte(`{"system":[]}`))
	wrote, err := WriteOutputs(m, out)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(wrote) != 1 || wrote[0] != out {
		t.Fatalf("wrote=%v", wrote)
	}
	b, _ := os.ReadFile(out)
	if !strings.Contains(string(b), `"captured_at"`) {
		t.Fatalf("json content unexpected: %s", b)
	}
}

func TestWriteOutputs_MDExtension(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "snap.md")
	meta := EnvelopeMeta{CapturedAt: time.Unix(0, 0).UTC()}
	m, _ := BuildEnvelopeMap(meta, []byte(`{"system":[]}`))
	wrote, err := WriteOutputs(m, out)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(wrote) != 1 || wrote[0] != out {
		t.Fatalf("wrote=%v", wrote)
	}
	b, _ := os.ReadFile(out)
	if !strings.Contains(string(b), "# promptdump capture") {
		t.Fatalf("md content unexpected: %s", b)
	}
}

func TestWriteOutputs_BareBasenameWritesBoth(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "snap")
	meta := EnvelopeMeta{CapturedAt: time.Unix(0, 0).UTC()}
	m, _ := BuildEnvelopeMap(meta, []byte(`{"system":[]}`))
	wrote, err := WriteOutputs(m, base)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(wrote) != 2 {
		t.Fatalf("wrote=%v", wrote)
	}
	if _, err := os.Stat(base + ".json"); err != nil {
		t.Fatalf("missing json: %v", err)
	}
	if _, err := os.Stat(base + ".md"); err != nil {
		t.Fatalf("missing md: %v", err)
	}
}
