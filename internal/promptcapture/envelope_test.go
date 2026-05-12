package promptcapture

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildEnvelopeMapAttachesCapturedFrom(t *testing.T) {
	meta := EnvelopeMeta{
		CapturedAt:    time.Unix(0, 0).UTC(),
		ClaudeVersion: "test",
		ClaudePath:    "/x/claude",
		ClaudeArgs:    []string{"-p", "ping"},
		Host:          HostInfo{Platform: "linux", CWD: "/eidos/ontology"},
		CapturedFrom: CapturedFrom{
			Mindform:     "alice",
			OntologyRoot: "/eidos/ontology",
			Model:        "sonnet",
			IdentityPath: "self/identity.md",
			Bare:         false,
		},
	}
	body := []byte(`{"system":[{"text":"hi"}]}`)
	m, err := BuildEnvelopeMap(meta, body)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	cf, ok := m["captured_from"].(map[string]any)
	if !ok {
		t.Fatalf("captured_from missing or wrong type: %v", m["captured_from"])
	}
	if cf["mindform"] != "alice" || cf["model"] != "sonnet" || cf["bare"] != false {
		t.Fatalf("captured_from values: %v", cf)
	}
	req, _ := m["request"].(map[string]any)
	if req == nil {
		t.Fatalf("request missing")
	}
}

func TestRenderMarkdownIncludesCapturedFromHeader(t *testing.T) {
	meta := EnvelopeMeta{
		CapturedAt: time.Unix(0, 0).UTC(),
		CapturedFrom: CapturedFrom{
			Mindform:     "alice",
			Model:        "sonnet",
			IdentityPath: "self/identity.md",
		},
	}
	m, err := BuildEnvelopeMap(meta, []byte(`{"system":[]}`))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	md, err := RenderMarkdown(m)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"Captured from", "alice", "sonnet", "self/identity.md"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestBuildEnvelopeMapFallsBackToRawBody(t *testing.T) {
	meta := EnvelopeMeta{CapturedAt: time.Unix(0, 0).UTC()}
	m, err := BuildEnvelopeMap(meta, []byte(`not-json`))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, ok := m["request"]; ok {
		t.Fatalf("request should be absent for unparseable body")
	}
	if m["raw_body"] != "not-json" {
		t.Fatalf("raw_body=%v", m["raw_body"])
	}
	if _, ok := m["parse_error"].(string); !ok {
		t.Fatalf("parse_error missing: %v", m)
	}
}

func TestRoundTripJSON(t *testing.T) {
	meta := EnvelopeMeta{CapturedAt: time.Unix(0, 0).UTC()}
	body := []byte(`{"system":[],"tools":[]}`)
	js, err := BuildEnvelope(meta, body)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(js, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_ = back
}
