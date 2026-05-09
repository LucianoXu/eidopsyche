package firstcontact

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Derive turns a free-form summoned name into a forgectl-valid slug,
// avoiding any name in `existing`.
//
// Algorithm:
//
//  1. NFD-fold and strip combining marks (so `é` decomposes to `e` +
//     combining acute, and the acute is dropped).
//  2. ASCII-class pass: keep [a-z0-9], lowercase A-Z, replace anything
//     else with `-`.
//  3. Collapse runs of `-` and trim leading / trailing `-`.
//  4. Truncate to 30 chars (forgectl's max). Re-trim trailing `-`.
//  5. If empty or fails forgectl.ValidateName → fall back to "mindform".
//  6. If colliding with `existing`, append "-2", "-3", ... until free.
//     If the suffix would push the result past forgectl's limits, trim
//     the base. Worst-case fallback: "mindform-N".
func Derive(summonedName string, existing []string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, _ := transform.String(t, summonedName)

	var b strings.Builder
	for _, r := range folded {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s := collapseDashes(b.String())
	s = strings.Trim(s, "-")
	if len(s) > 30 {
		s = strings.TrimRight(s[:30], "-")
	}
	if s == "" || forgectl.ValidateName(s) != nil {
		s = "mindform"
	}

	exists := makeExistsFn(existing)
	if !exists(s) {
		return s
	}
	for i := 2; i < 1000; i++ {
		suffix := "-" + strconv.Itoa(i)
		base := s
		if len(base)+len(suffix) > 30 {
			base = strings.TrimRight(base[:30-len(suffix)], "-")
		}
		cand := base + suffix
		if forgectl.ValidateName(cand) != nil {
			continue
		}
		if !exists(cand) {
			return cand
		}
	}
	return "mindform-" + strconv.Itoa(len(existing)+1)
}

func makeExistsFn(existing []string) func(string) bool {
	set := make(map[string]struct{}, len(existing))
	for _, e := range existing {
		set[e] = struct{}{}
	}
	return func(x string) bool {
		_, ok := set[x]
		return ok
	}
}

func collapseDashes(s string) string {
	var b strings.Builder
	prev := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '-' && prev == '-' {
			continue
		}
		b.WriteByte(c)
		prev = c
	}
	return b.String()
}
