// Package ontology owns the on-disk shape of a mind-form's essence and
// the scaffolding that produces it.
package ontology

import (
	"embed"

	root "github.com/LucianoXu/eidopsyche"
)

// templateFS returns the embedded canonical ontology template, sourced
// from the module root's embed.FS. The root inside the FS is
// "template/".
func templateFS() embed.FS { return root.TemplateFS() }

// prefabFS returns the embedded prefab catalogue. The root inside the
// FS is "prefab/", with one subdirectory per prefab id.
func prefabFS() embed.FS { return root.PrefabFS() }

// TemplateFS exposes the embedded template root for callers that need
// to walk it directly. Re-exported from the module-root package so
// existing call sites do not have to change import paths.
func TemplateFS() embed.FS { return root.TemplateFS() }
