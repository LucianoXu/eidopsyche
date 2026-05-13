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
