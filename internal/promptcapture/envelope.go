package promptcapture

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// CapturedFrom records which mind-form context produced the envelope.
// All fields are optional — utils/promptdump (no mind-form) leaves the
// struct zero-valued; the in-container forge worker fills it in.
type CapturedFrom struct {
	Mindform     string `json:"mindform"`
	OntologyRoot string `json:"ontology_root"`
	Model        string `json:"model"`
	IdentityPath string `json:"identity_path"`
	Bare         bool   `json:"bare"`
}

// EnvelopeMeta is the wrapper metadata captured alongside the request body.
type EnvelopeMeta struct {
	CapturedAt    time.Time
	ClaudeVersion string
	ClaudePath    string
	ClaudeArgs    []string
	Host          HostInfo
	CapturedFrom  CapturedFrom // zero-valued for utils/promptdump callers
}

// HostInfo captures the host-side context that influences dynamic
// segments of the system prompt (cwd, platform).
type HostInfo struct {
	Platform string `json:"platform"`
	CWD      string `json:"cwd"`
}

// BuildEnvelopeMap assembles the wrapper map from meta + body.
// Valid JSON body lands under "request"; malformed body falls back to
// "raw_body" + "parse_error" so output is always usable.
//
// All values are JSON-normalized: ClaudeArgs becomes []any, Host becomes
// map[string]any, etc. This keeps RenderMarkdown's type assertions simple
// and uniform regardless of whether the map was hand-built (in tests) or
// produced from a real capture.
func BuildEnvelopeMap(meta EnvelopeMeta, body []byte) (map[string]any, error) {
	raw := map[string]any{
		"captured_at":    meta.CapturedAt.UTC().Format(time.RFC3339),
		"claude_version": meta.ClaudeVersion,
		"claude_path":    meta.ClaudePath,
		"claude_args":    meta.ClaudeArgs,
		"host":           meta.Host,
	}
	if meta.CapturedFrom != (CapturedFrom{}) {
		raw["captured_from"] = meta.CapturedFrom
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err == nil {
		raw["request"] = parsed
	} else {
		raw["raw_body"] = string(body)
		raw["parse_error"] = err.Error()
	}
	normalized, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(normalized, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// BuildEnvelope serializes meta + body into the final pretty-printed JSON.
func BuildEnvelope(meta EnvelopeMeta, body []byte) ([]byte, error) {
	m, err := BuildEnvelopeMap(meta, body)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(m, "", "  ")
}

// RenderMarkdown formats a parsed envelope as a human-readable Markdown
// document. Companion to BuildEnvelope (which produces the canonical JSON).
// Sections degrade gracefully when fields are absent.
func RenderMarkdown(env map[string]any) (string, error) {
	var b strings.Builder

	b.WriteString("# promptdump capture\n\n")
	if v, ok := env["captured_at"].(string); ok {
		fmt.Fprintf(&b, "**Captured at:** %s\n", v)
	}
	if v, ok := env["claude_version"].(string); ok {
		fmt.Fprintf(&b, "**Claude version:** %s\n", v)
	}
	if v, ok := env["claude_path"].(string); ok && v != "" {
		fmt.Fprintf(&b, "**Claude path:** `%s`\n", v)
	}
	if v, ok := env["claude_args"].([]any); ok {
		fmt.Fprintf(&b, "**Claude args:** `%s`\n", joinArgs(v))
	}
	if h, ok := env["host"].(map[string]any); ok {
		platform, _ := h["platform"].(string)
		cwd, _ := h["cwd"].(string)
		fmt.Fprintf(&b, "**Host:** %s · cwd=`%s`\n", platform, cwd)
	}
	if cf, ok := env["captured_from"].(map[string]any); ok {
		b.WriteString("**Captured from:** ")
		var parts []string
		if v, _ := cf["mindform"].(string); v != "" {
			parts = append(parts, "mindform="+v)
		}
		if v, _ := cf["model"].(string); v != "" {
			parts = append(parts, "model="+v)
		}
		if v, _ := cf["identity_path"].(string); v != "" {
			parts = append(parts, "identity="+v)
		}
		if v, _ := cf["bare"].(bool); v {
			parts = append(parts, "bare=true")
		}
		if v, _ := cf["ontology_root"].(string); v != "" {
			parts = append(parts, "ontology_root="+v)
		}
		b.WriteString(strings.Join(parts, " · "))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	req, _ := env["request"].(map[string]any)
	if req == nil {
		if raw, ok := env["raw_body"].(string); ok {
			b.WriteString("## Raw body (failed to parse as JSON)\n\n```\n")
			b.WriteString(raw)
			b.WriteString("\n```\n")
		}
		return b.String(), nil
	}

	b.WriteString("## Request\n\n")
	if v, ok := req["model"].(string); ok {
		fmt.Fprintf(&b, "- **Model:** `%s`\n", v)
	}
	if v, ok := req["max_tokens"].(float64); ok {
		fmt.Fprintf(&b, "- **Max tokens:** %d\n", int(v))
	}
	if v, ok := req["stream"].(bool); ok {
		fmt.Fprintf(&b, "- **Stream:** %v\n", v)
	}
	b.WriteString("\n")

	renderOtherRequestFields(&b, req)

	sys, _ := req["system"].([]any)
	fmt.Fprintf(&b, "## System prompt (%d segments)\n\n", len(sys))
	for i, seg := range sys {
		segMap, _ := seg.(map[string]any)
		header := fmt.Sprintf("### Segment %d", i+1)
		if cc, ok := segMap["cache_control"]; ok {
			ccBytes, _ := json.Marshal(cc)
			header += fmt.Sprintf(" — `cache_control: %s`", string(ccBytes))
		}
		b.WriteString(header + "\n\n")
		text, _ := segMap["text"].(string)
		b.WriteString("```\n")
		b.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
	}

	tools, _ := req["tools"].([]any)
	fmt.Fprintf(&b, "## Tools (%d)\n\n", len(tools))
	for _, t := range tools {
		tm, _ := t.(map[string]any)
		name, _ := tm["name"].(string)
		desc, _ := tm["description"].(string)
		fmt.Fprintf(&b, "- **`%s`** — %s\n", name, firstNonEmptyLine(desc))
	}
	if len(tools) > 0 {
		b.WriteString("\n")
	}
	for _, t := range tools {
		tm, _ := t.(map[string]any)
		renderToolDetail(&b, tm)
	}

	messages, _ := req["messages"].([]any)
	if len(messages) > 0 {
		b.WriteString("## First user message\n\n")
		msg, _ := messages[0].(map[string]any)
		switch content := msg["content"].(type) {
		case string:
			b.WriteString("```\n")
			b.WriteString(content)
			if !strings.HasSuffix(content, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```\n")
		case []any:
			for _, part := range content {
				pm, _ := part.(map[string]any)
				typ, _ := pm["type"].(string)
				switch typ {
				case "text":
					txt, _ := pm["text"].(string)
					b.WriteString("```\n")
					b.WriteString(txt)
					if !strings.HasSuffix(txt, "\n") {
						b.WriteString("\n")
					}
					b.WriteString("```\n")
				default:
					mediaType, _ := pm["media_type"].(string)
					fmt.Fprintf(&b, "_[%s%s]_\n", typ, formatMediaType(mediaType))
				}
			}
		}
	}

	return b.String(), nil
}

func joinArgs(args []any) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if s, ok := a.(string); ok {
			parts = append(parts, s)
		} else {
			parts = append(parts, fmt.Sprintf("%v", a))
		}
	}
	return strings.Join(parts, " ")
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			return t
		}
	}
	return ""
}

// renderOtherRequestFields appends an "### Other request fields"
// subsection enumerating every key in req that is not already covered
// by a dedicated section. Skipped when no such key exists. Keys are
// sorted alphabetically for deterministic output.
func renderOtherRequestFields(b *strings.Builder, req map[string]any) {
	covered := map[string]struct{}{
		"system": {}, "tools": {}, "messages": {},
		"model": {}, "max_tokens": {}, "stream": {},
	}
	var keys []string
	for k := range req {
		if _, ok := covered[k]; ok {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return
	}
	sort.Strings(keys)

	b.WriteString("### Other request fields\n\n")
	for _, k := range keys {
		v := req[k]
		switch vv := v.(type) {
		case string:
			fmt.Fprintf(b, "- **%s:** `%s`\n", k, vv)
		case bool, float64, nil:
			fmt.Fprintf(b, "- **%s:** %v\n", k, vv)
		default:
			fmt.Fprintf(b, "- **%s:**\n  ```json\n", k)
			j, _ := json.MarshalIndent(vv, "  ", "  ")
			b.Write(j)
			b.WriteString("\n  ```\n")
		}
	}
	b.WriteString("\n")
}

// renderToolDetail appends a "### `<name>`" subsection for one tool:
// its full description in a fenced block, and the input_schema in a
// collapsible <details> block.
func renderToolDetail(b *strings.Builder, tool map[string]any) {
	name, _ := tool["name"].(string)
	desc, _ := tool["description"].(string)
	if name == "" && desc == "" {
		return
	}
	fmt.Fprintf(b, "### `%s`\n\n", name)
	if desc != "" {
		b.WriteString("```\n")
		b.WriteString(desc)
		if !strings.HasSuffix(desc, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
	}
	if schema, ok := tool["input_schema"]; ok {
		b.WriteString("<details><summary>input_schema</summary>\n\n")
		b.WriteString("```json\n")
		schemaJSON, _ := json.MarshalIndent(schema, "", "  ")
		b.Write(schemaJSON)
		b.WriteString("\n```\n\n</details>\n\n")
	}
}

// formatMediaType returns the media-type prefixed with a single space,
// or empty if s is empty.
func formatMediaType(s string) string {
	if s == "" {
		return ""
	}
	return " " + s
}
