package forge

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderStream_RenderedEvents(t *testing.T) {
	src := strings.NewReader(
		`{"type":"system","subtype":"init","model":"x","tools":["Read"]}` + "\n" +
			`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}` + "\n" +
			`{"type":"result","is_error":false,"duration_ms":1000,"num_turns":1}` + "\n",
	)
	var buf bytes.Buffer
	if err := renderStream(&buf, src, false, renderOpts{}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"wake started", "hi", "done", "1s", "ok"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q: %s", want, out)
		}
	}
}

func TestRenderStream_RawPassthrough(t *testing.T) {
	body := `{"type":"system","subtype":"init"}` + "\n" +
		`{"type":"result","is_error":false}` + "\n"
	src := strings.NewReader(body)
	var buf bytes.Buffer
	if err := renderStream(&buf, src, true, renderOpts{}); err != nil {
		t.Fatal(err)
	}
	if buf.String() != body {
		t.Errorf("raw passthrough modified bytes:\nwant: %q\ngot:  %q", body, buf.String())
	}
}

func TestRenderStream_TolerateMalformedLine(t *testing.T) {
	src := strings.NewReader(
		`{"type":"system","subtype":"init"}` + "\n" +
			`bad-line` + "\n" +
			`{"type":"result"}` + "\n",
	)
	var buf bytes.Buffer
	if err := renderStream(&buf, src, false, renderOpts{}); err != nil {
		t.Fatalf("malformed line should not abort: %v", err)
	}
	if !strings.Contains(buf.String(), "wake started") {
		t.Errorf("first event should still render")
	}
	if !strings.Contains(buf.String(), "done") {
		t.Errorf("trailing event after malformed should still render: %s", buf.String())
	}
}

func TestRenderStream_LargeLine(t *testing.T) {
	big := strings.Repeat("x", 2*1024*1024) // 2 MiB tool_result content
	src := strings.NewReader(
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"` + big + `"}]}}` + "\n",
	)
	var buf bytes.Buffer
	if err := renderStream(&buf, src, true, renderOpts{}); err != nil {
		t.Fatalf("renderStream large line: %v", err)
	}
	if !strings.Contains(buf.String(), big) {
		t.Errorf("raw mode should preserve large lines verbatim")
	}
}
