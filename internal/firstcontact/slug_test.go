package firstcontact_test

import (
	"strings"
	"testing"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

func TestDerive_TableCases(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		existing []string
		want     string
	}{
		{"latin-simple", "Alice", nil, "alice"},
		{"latin-spaces", "Alice the Wise", nil, "alice-the-wise"},
		{"trim-leading-trailing", "  -alice-  ", nil, "alice"},
		{"diacritics-stripped", "Café", nil, "cafe"},
		{"length-cap-30",
			"abcdefghijklmnopqrstuvwxyzabcdefghijklmnop",
			nil,
			"abcdefghijklmnopqrstuvwxyzabcd"},
		{"collision-2", "alice", []string{"alice"}, "alice-2"},
		{"collision-3", "alice", []string{"alice", "alice-2"}, "alice-3"},
		{"all-cjk-falls-back", "雨", nil, "mindform"},
		{"empty-falls-back", "", nil, "mindform"},
		{"emoji-falls-back", "🌸", nil, "mindform"},
		{"mixed-keeps-latin", "Alice 雨", nil, "alice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := firstcontact.Derive(c.input, c.existing)
			if got != c.want {
				t.Errorf("Derive(%q, %v) = %q, want %q", c.input, c.existing, got, c.want)
			}
		})
	}
}

// TestDerive_AlwaysValidatesAgainstForgectl pins that the output of
// Derive always passes forgectl.ValidateName — that's the contract the
// wizard's phase 2 step 4 relies on.
func TestDerive_AlwaysValidatesAgainstForgectl(t *testing.T) {
	for _, in := range []string{
		"???###",
		strings.Repeat("a", 200),
		"123-leading-digit",
		"-a",
		"a-",
		"",
		"\t\n",
	} {
		got := firstcontact.Derive(in, nil)
		if got == "" {
			t.Errorf("Derive(%q) returned empty", in)
		}
		if err := forgectl.ValidateName(got); err != nil {
			t.Errorf("Derive(%q) = %q which fails ValidateName: %v", in, got, err)
		}
	}
}
