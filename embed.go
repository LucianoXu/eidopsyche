// Package eidopsyche owns the module-root embed.FS bundling the canonical
// ontology template and the prefab catalogue. Internal packages
// (notably internal/ontology) re-export these for caller convenience.
//
// The file lives at the module root because Go's //go:embed directive
// cannot reach upward (no `..` segments allowed), and we want template/
// and prefab/ at the top of the repo so authors find them without
// hunting through internal/.
package eidopsyche

import "embed"

//go:embed all:template
var templateFS embed.FS

//go:embed all:prefab
var prefabFS embed.FS

// TemplateFS returns the embedded canonical ontology template FS.
// The root inside the FS is "template/".
func TemplateFS() embed.FS { return templateFS }

// PrefabFS returns the embedded prefab catalogue FS. The root inside
// the FS is "prefab/", with one subdirectory per prefab id.
func PrefabFS() embed.FS { return prefabFS }
