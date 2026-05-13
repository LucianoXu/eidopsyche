package prompts

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

// buildData is the rendering context for assets/system1-instructions.txt.
// Keep field names stable — the template references them by name.
type buildData struct {
	Facts  IdentityFacts
	CLAUDE string // contents of <ontology>/CLAUDE.md
	Soul   string // contents of <ontology>/self/soul.md
	Today  string // current date, YYYY-MM-DD
}

// Build assembles the full --system-prompt payload for a mind-form's
// next claude spawn. It reads CLAUDE.md and self/soul.md from
// ontologyDir; missing files render as empty strings (a fresh ontology
// may have an empty soul). Returns the rendered prompt or an error if
// the template parse / execute fails (which would indicate a bug, not
// a runtime state issue).
//
// self/identity.toml is consumed by FromOntology (callers populate
// `facts` ahead of Build); its values surface through the Info block
// of the rendered template rather than being inlined verbatim.
//
// mood.md is intentionally not read here — it is volatile working
// state the mind-form consults on demand via the Read tool.
func Build(ctx context.Context, facts IdentityFacts, ontologyDir string) (string, error) {
	if facts.OntologyDir == "" {
		facts.OntologyDir = ontologyDir
	}
	data := buildData{
		Facts: facts,
		Today: time.Now().UTC().Format("2006-01-02"),
	}

	var err error
	data.CLAUDE, err = readOptional(filepath.Join(ontologyDir, "CLAUDE.md"))
	if err != nil {
		return "", fmt.Errorf("read CLAUDE.md: %w", err)
	}
	data.Soul, err = readOptional(filepath.Join(ontologyDir, "self", "soul.md"))
	if err != nil {
		return "", fmt.Errorf("read self/soul.md: %w", err)
	}

	raw, err := assets.ReadFile("assets/system1-instructions.txt")
	if err != nil {
		return "", fmt.Errorf("read embedded template: %w", err)
	}

	tmpl, err := template.New("system1").Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("parse system1 template: %w", err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("render system1 template: %w", err)
	}
	return sb.String(), nil
}

func readOptional(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}
