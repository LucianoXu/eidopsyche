package prompts

import (
	"fmt"
	"strings"
	"text/template"
)

// birthBootTemplate is the embedded birth-wake user prompt template,
// loaded once at init rather than each call so a corrupt embed surfaces
// at process start. Rendered with {{.OwnerLabel}} via BirthUser.
var birthBootTemplate = mustLoad("assets/birth.txt")

func mustLoad(name string) string {
	b, err := assets.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("prompts: embedded asset %q missing: %v", name, err))
	}
	return string(b)
}

// BirthUser renders assets/birth.txt as the user-message half of the
// birth-wake invocation. The system prompt half is assembled by Build.
// The agent reads the summoning book and calling-words from disk via
// the Read tool — they are not inlined into this prompt.
func BirthUser(ownerLabel string) (string, error) {
	tmpl, err := template.New("birthuser").Option("missingkey=error").Parse(birthBootTemplate)
	if err != nil {
		return "", fmt.Errorf("parse birth.txt: %w", err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, map[string]string{"OwnerLabel": ownerLabel}); err != nil {
		return "", fmt.Errorf("render birth.txt: %w", err)
	}
	return sb.String(), nil
}
