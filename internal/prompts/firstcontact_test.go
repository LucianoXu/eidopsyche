package prompts_test

import (
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/prompts"
)

func TestResearchPromptCarriesUserTextAndLang(t *testing.T) {
	got := prompts.Research("a wandering swordsman", "zh")
	if !strings.Contains(got, "a wandering swordsman") {
		t.Errorf("Research prompt missing user text; got:\n%s", got)
	}
	if !strings.Contains(got, "Language for archetype/temperament/world: zh") {
		t.Errorf("Research prompt missing lang directive; got:\n%s", got)
	}
	if !strings.Contains(got, "Return ONLY the JSON object") {
		t.Errorf("Research prompt missing JSON-only directive; got:\n%s", got)
	}
}

func TestDisplayingPromptCarriesProfileFields(t *testing.T) {
	got := prompts.Displaying(prompts.DisplayingInput{
		Archetype:   "wanderer",
		Temperament: "quiet",
		World:       "mountain road",
		Settings:    []string{"dusk", "tea-house"},
		Imagery:     []string{"lantern", "rain"},
		Lang:        "en",
	})
	for _, want := range []string{"wanderer", "quiet", "mountain road", "dusk", "lantern", "Language: en"} {
		if !strings.Contains(got, want) {
			t.Errorf("Displaying prompt missing %q; got:\n%s", want, got)
		}
	}
}

func TestCallingWordsPromptCarriesBookAndLang(t *testing.T) {
	got := prompts.CallingWords("a figure stands at the gate", "zh")
	if !strings.Contains(got, "a figure stands at the gate") {
		t.Errorf("CallingWords missing book body; got:\n%s", got)
	}
	if !strings.Contains(got, "Language: zh") {
		t.Errorf("CallingWords missing lang directive; got:\n%s", got)
	}
}

func TestRoleResearch_HasHardCanonConstraint(t *testing.T) {
	got := prompts.RoleResearch(prompts.RoleResearchInput{
		Description: "a quiet river-spirit",
		Archetype:   "river-spirit",
		Temperament: "calm",
		World:       "rural shrine",
		Settings:    []string{"a stone bridge"},
		Imagery:     []string{"moonlight on water"},
		Lang:        "en",
	})
	for _, want := range []string{
		"Do NOT name any source work",
		"original character",
		"derived adjective",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RoleResearch prompt missing constraint %q; got:\n%s", want, got)
		}
	}
	for _, banned := range []string{
		"search the named sources",
		"quote small specifics",
	} {
		if strings.Contains(got, banned) {
			t.Errorf("RoleResearch prompt still contains banned phrase %q", banned)
		}
	}
}

func TestRoleResearch_DoesNotInterpolateSources(t *testing.T) {
	got := prompts.RoleResearch(prompts.RoleResearchInput{
		Description: "x", Archetype: "y", Temperament: "z",
		World: "w", Settings: nil, Imagery: nil, Lang: "en",
	})
	if strings.Contains(got, "sources (works/franchises") {
		t.Errorf("RoleResearch prompt still references sources; got:\n%s", got)
	}
}

func TestCallingWords_HasNoCanonNamesConstraint(t *testing.T) {
	got := prompts.CallingWords("a figure at the gate", "en")
	if !strings.Contains(got, "Do not name characters, works, or fictional settings") {
		t.Errorf("CallingWords prompt missing defensive constraint; got:\n%s", got)
	}
}

func TestResearch_SourcesMarkedDebugOnly(t *testing.T) {
	got := prompts.Research("a wanderer", "en")
	if !strings.Contains(got, "dramaturge bookkeeping") {
		t.Errorf("Research prompt missing sources-is-debug-only note; got:\n%s", got)
	}
}
