package prompts

import (
	"fmt"
	"strings"
)

// ParseFrontmatter extracts a YAML-style key: value frontmatter block
// from the start of body, returning the key/value map, the remainder
// of body (with the frontmatter block removed), and any error.
//
// Frontmatter is delimited by lines containing only "---" — the first
// such line must be on the very first line of body. If the input does
// not begin with "---", ParseFrontmatter returns an empty map, the
// original body, and nil error (a file with no frontmatter is valid).
//
// Values may be optionally wrapped in single or double quotes; quotes
// are stripped. No escape handling. All values are strings — callers
// coerce if they need other types.
func ParseFrontmatter(body string) (map[string]string, string, error) {
	out := map[string]string{}
	if !strings.HasPrefix(body, "---\n") && !strings.HasPrefix(body, "---\r\n") {
		return out, body, nil
	}
	rest := strings.TrimPrefix(body, "---\n")
	rest = strings.TrimPrefix(rest, "---\r\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, "", fmt.Errorf("frontmatter: opening --- without closing ---")
	}
	block := rest[:end]
	after := rest[end:]
	after = strings.TrimPrefix(after, "\n---")
	after = strings.TrimPrefix(after, "\n")
	after = strings.TrimPrefix(after, "\r\n")

	for lineNum, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		i := strings.Index(s, ":")
		if i < 0 {
			return nil, "", fmt.Errorf("frontmatter line %d: missing ':' in %q", lineNum+1, s)
		}
		key := strings.TrimSpace(s[:i])
		val := strings.TrimSpace(s[i+1:])
		val = unquote(val)
		out[key] = val
	}
	return out, after, nil
}

func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
