// Package ontology owns the on-disk shape of a mind-form's essence and the
// scaffolding that produces it.
package ontology

import "embed"

//go:embed all:template
var templateFS embed.FS

// TemplateFS exposes the embedded template root for callers that need to
// walk it directly. The root is "template/".
func TemplateFS() embed.FS { return templateFS }
