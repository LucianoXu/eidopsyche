package firstcontact

import (
	"context"
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/ontology"
)

func TestPhase3Prefab_PicksAndNames(t *testing.T) {
	tf, err := ontology.MetaFor("_test_fixture")
	if err != nil {
		t.Fatal(err)
	}
	cat := []ontology.Meta{tf}
	r := &fakeRenderer{
		choices: []int{0},
		prompts: []string{"Lyra"},
	}
	s := &Summoning{Lang: "en"}
	if err := Phase3Prefab(context.Background(), s, r, Phase3PrefabDeps{
		Catalogue:     cat,
		ExistingSlugs: nil,
	}); err != nil {
		t.Fatal(err)
	}
	if s.PrefabID != "_test_fixture" {
		t.Errorf("PrefabID = %q, want _test_fixture", s.PrefabID)
	}
	if s.SummonedName != "Lyra" {
		t.Errorf("SummonedName = %q, want Lyra", s.SummonedName)
	}
	if s.Slug == "" {
		t.Errorf("Slug should be derived; got empty")
	}
	if !strings.Contains(s.Displaying, "placeholder") {
		t.Errorf("Displaying should carry the prefab preview text; got %q", s.Displaying)
	}
}

func TestPhase3Prefab_EmptyCatalogue(t *testing.T) {
	r := &fakeRenderer{}
	s := &Summoning{Lang: "en"}
	err := Phase3Prefab(context.Background(), s, r, Phase3PrefabDeps{Catalogue: nil})
	if err == nil {
		t.Errorf("expected error on empty catalogue")
	}
}

func TestPhase3Prefab_ChoiceOutOfRange(t *testing.T) {
	tf, err := ontology.MetaFor("_test_fixture")
	if err != nil {
		t.Fatal(err)
	}
	cat := []ontology.Meta{tf}
	// Force an out-of-range index from the renderer.
	r := &fakeRenderer{choices: []int{5}}
	s := &Summoning{Lang: "en"}
	err = Phase3Prefab(context.Background(), s, r, Phase3PrefabDeps{Catalogue: cat})
	if err == nil {
		t.Errorf("expected out-of-range error")
	}
}

func TestPreferLang_PicksRequested(t *testing.T) {
	m := map[string]string{"zh": "防火女", "en": "Fire Keeper"}
	if got := preferLang(m, "zh"); got != "防火女" {
		t.Errorf("got %q", got)
	}
	if got := preferLang(m, "en"); got != "Fire Keeper" {
		t.Errorf("got %q", got)
	}
}

func TestPreferLang_FallsBackToEnglish(t *testing.T) {
	m := map[string]string{"en": "Fire Keeper"}
	if got := preferLang(m, "zh"); got != "Fire Keeper" {
		t.Errorf("got %q", got)
	}
}

func TestPreferLang_FallsBackToAny(t *testing.T) {
	m := map[string]string{"ja": "守り"}
	if got := preferLang(m, "zh"); got != "守り" {
		t.Errorf("got %q", got)
	}
}

func TestPreferLang_NilEmpty(t *testing.T) {
	if got := preferLang(nil, "zh"); got != "" {
		t.Errorf("got %q from nil map", got)
	}
}
