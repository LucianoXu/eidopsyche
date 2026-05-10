package firstcontact

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
	"github.com/LucianoXu/eidopsyche/internal/ontology"
)

// Phase3PrefabDeps are the inputs Phase 3's prefab branch cannot
// derive from Summoning.
type Phase3PrefabDeps struct {
	Catalogue     []ontology.Meta
	ExistingSlugs []string
}

// Phase3Prefab renders the prefab catalogue, prompts the operator to
// pick one and to name the instance, and populates Summoning fields
// the seal/orchestrate stages rely on (PrefabID, SummonedName, Slug,
// Displaying).
//
// Empty catalogue is an error: the wizard should have offered the
// scratch path instead. Callers wire ScaffoldChoice so this is only
// reached when the operator explicitly picked prefab.
func Phase3Prefab(ctx context.Context, s *Summoning, r render.Renderer, d Phase3PrefabDeps) error {
	if len(d.Catalogue) == 0 {
		r.Show(stringFor(s.Lang, "phase3_prefab_no_prefabs"))
		return errors.New("prefab catalogue is empty")
	}
	choices := make([]render.ChoiceOption, len(d.Catalogue))
	for i, m := range d.Catalogue {
		choices[i] = render.ChoiceOption{Label: prefabMenuLine(m, s.Lang)}
	}
	idx, err := r.PromptChoice(stringFor(s.Lang, "phase3_prefab_q"), choices)
	if err != nil {
		return err
	}
	if idx < 0 || idx >= len(d.Catalogue) {
		return fmt.Errorf("phase3 prefab: choice index %d out of range", idx)
	}
	chosen := d.Catalogue[idx]

	preview := preferLang(chosen.Preview, s.Lang)
	r.Typewriter(ctx, preview)

	name, err := r.Prompt(stringFor(s.Lang, "phase3_prefab_naming_q"), render.PromptOpts{})
	if err != nil {
		return err
	}
	s.PrefabID = chosen.ID
	s.SummonedName = name
	s.Slug = Derive(name, d.ExistingSlugs)
	s.Displaying = preview
	return nil
}

// prefabMenuLine formats one row of the prefab menu in the operator's
// preferred language: "<display> · <kind-glyph> · <tagline>".
func prefabMenuLine(m ontology.Meta, lang string) string {
	parts := []string{}
	if disp := preferLang(m.Display, lang); disp != "" {
		parts = append(parts, disp)
	}
	if kind := kindGlyph(m.Kind, lang); kind != "" {
		parts = append(parts, kind)
	}
	if tag := preferLang(m.Tagline, lang); tag != "" {
		parts = append(parts, tag)
	}
	return strings.Join(parts, " · ")
}

// preferLang picks a value from a lang-keyed map: requested lang first,
// then "en", then any non-empty value, else the empty string. Used for
// display name / tagline / preview where prefab.toml carries multiple
// translations.
func preferLang(m map[string]string, lang string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[lang]; ok && v != "" {
		return v
	}
	if v, ok := m["en"]; ok && v != "" {
		return v
	}
	for _, v := range m {
		if v != "" {
			return v
		}
	}
	return ""
}

func kindGlyph(kind, lang string) string {
	if lang == "zh" {
		switch kind {
		case "m":
			return "男"
		case "f":
			return "女"
		case "spirit":
			return "灵"
		}
		return ""
	}
	switch kind {
	case "m":
		return "M"
	case "f":
		return "F"
	case "spirit":
		return "Spirit"
	}
	return ""
}
