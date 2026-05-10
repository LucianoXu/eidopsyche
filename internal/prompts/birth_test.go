package prompts_test

import (
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/prompts"
)

func TestBirthBootEmbedded(t *testing.T) {
	got := prompts.BirthBoot()
	for _, want := range []string{
		"You have just been summoned",
		"essence/born_at",
		"identity → secret → response → born_at",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("BirthBoot missing %q", want)
		}
	}
}

func TestBirthUser(t *testing.T) {
	got := prompts.BirthUser("npub1example", "BOOK", "WORDS")
	for _, want := range []string{
		"Operator npub: npub1example",
		"--- summoning book ---",
		"BOOK",
		"--- calling-words ---",
		"WORDS",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("BirthUser missing %q; got:\n%s", want, got)
		}
	}
}
