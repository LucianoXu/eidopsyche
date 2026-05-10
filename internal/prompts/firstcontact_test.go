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
