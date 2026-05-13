package prompts_test

import (
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/prompts"
)

func TestBirthUser(t *testing.T) {
	got, err := prompts.BirthUser("Bob")
	if err != nil {
		t.Fatalf("BirthUser: %v", err)
	}
	for _, want := range []string{
		"summoned by Bob.",
		"chest/summoning-book.md",
		"self/calling-words.md",
		"self/born_at",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("BirthUser missing %q; got:\n%s", want, got)
		}
	}
}

func TestBirthUser_NewBirthFlow(t *testing.T) {
	out, err := prompts.BirthUser("Bob")
	if err != nil {
		t.Fatalf("BirthUser: %v", err)
	}
	for _, want := range []string{
		"role-research.md",
		"summoning-book.md",
		"calling-words.md",
		"self/soul.md",
		"self/secret.md",
		"chest/first-message.md",
		"eidos gate send",
		"creator_npub",
		"self/born_at",
		"Vibe / 气质",
		"Personality / 性格",
		"Speech / 表达方式",
		"Self-image / 自我形象",
		"Treasures and Tensions / 珍视的与介意的",
		"Bob",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("BirthUser output missing %q", want)
		}
	}
	// Must NOT contain stale references.
	for _, banned := range []string{
		"first-words.md",
		"identity.md",
	} {
		if strings.Contains(out, banned) {
			t.Errorf("BirthUser output should no longer contain %q", banned)
		}
	}
}
