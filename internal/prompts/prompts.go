// Package prompts is the single home for every LLM-facing prompt string
// the eidos host binary sends to claude.
//
// Three call sites land here:
//   - cmd/eidos/supervisor on a birth-wake (BirthBoot + BirthUser)
//   - cmd/eidos/supervisor on every other wake (BuildWake)
//   - internal/firstcontact's summoning wizard, for its research /
//     displaying / calling-words turns
//
// What does NOT live here:
//   - identity / values / CLAUDE.md and the rest of the ontology tree:
//     those are file-as-essence artifacts shipped via template/ and
//     prefab/, embedded by the top-level embed.go.
//   - UI strings the wizard shows to the human operator: those live in
//     internal/firstcontact/strings.go.
package prompts

import "embed"

//go:embed assets
var assets embed.FS
