package firstcontact

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/mozillazg/go-pinyin"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// maxNameLen mirrors forgectl.ValidateName's upper bound (regex caps
// the total length at 32). Repeated here rather than imported because
// forgectl currently keeps the bound inside its regex literal.
const maxNameLen = 32

// fallbackPool is the cycle of words used when the user's input has
// nothing salvageable (empty, all-emoji, all-punctuation). Greek /
// Latin terms for mind / soul, on-theme with the project name
// (εἶδος + ψυχή). Order is the priority order.
var fallbackPool = []string{
	"eidos", "psyche", "anima", "nous", "logos",
	"pneuma", "thymos", "phren", "noema", "kardia",
}

// Derive turns a free-form summoned name into a forgectl-valid slug,
// avoiding any name in `existing`.
//
// Pipeline:
//  1. Han characters → pinyin (no tones), so 小明 → "xiao-ming".
//  2. ASCII-fold accents (NFD), lowercase, replace non-[a-z0-9] runs
//     with `-`, trim, drop leading non-letters, cap to maxNameLen.
//  3. If the result fails forgectl.ValidateName (too short, no Latin
//     content), pick the first unused word from fallbackPool.
//  4. If the result is still in `existing`, append `-2`, `-3`, ...
//     trimming the base if the suffix would exceed maxNameLen.
func Derive(summonedName string, existing []string) string {
	taken := make(map[string]struct{}, len(existing))
	for _, e := range existing {
		taken[e] = struct{}{}
	}

	s := canonicalize(transliterateHan(summonedName))
	if forgectl.ValidateName(s) != nil {
		s = pickFallback(taken)
	}
	return uniqueify(s, taken)
}

// transliterateHan replaces each Han rune with its first-reading
// pinyin (no tones), dash-padded so syllables don't fuse with
// adjacent ASCII. Non-Han runes pass through verbatim;
// canonicalize will downcase / strip them afterwards.
func transliterateHan(s string) string {
	args := pinyin.NewArgs()
	args.Style = pinyin.Normal
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			py := pinyin.SinglePinyin(r, args)
			if len(py) > 0 {
				b.WriteByte('-')
				b.WriteString(py[0])
				b.WriteByte('-')
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// canonicalize folds accents (NFD + strip combining marks),
// lowercases A-Z, replaces every other rune with `-`, collapses dash
// runs in the same pass via a prevDash flag, trims dashes from edges,
// drops any leading non-letter (forgectl requires the first char to
// be [a-z]), and caps at maxNameLen. Returns "" if nothing is left.
func canonicalize(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	folded, _, _ := transform.String(t, s)

	var b strings.Builder
	b.Grow(len(folded))
	prevDash := true // suppresses leading dashes
	for _, r := range folded {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			prevDash = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")

	for len(out) > 0 && (out[0] < 'a' || out[0] > 'z') {
		out = out[1:]
	}
	if len(out) > maxNameLen {
		out = strings.TrimRight(out[:maxNameLen], "-")
	}
	return out
}

// pickFallback returns the first pool word not in taken. If every
// pool word is already taken, returns pool[0] and lets uniqueify
// append a numeric suffix.
func pickFallback(taken map[string]struct{}) string {
	for _, w := range fallbackPool {
		if _, ok := taken[w]; !ok {
			return w
		}
	}
	return fallbackPool[0]
}

// uniqueify returns base if free, else base-2, base-3, ... trimming
// the base if the suffix would push the slug past maxNameLen.
func uniqueify(base string, taken map[string]struct{}) string {
	if _, ok := taken[base]; !ok {
		return base
	}
	for i := 2; ; i++ {
		suffix := "-" + strconv.Itoa(i)
		b := base
		if len(b)+len(suffix) > maxNameLen {
			b = strings.TrimRight(b[:maxNameLen-len(suffix)], "-")
		}
		cand := b + suffix
		if _, ok := taken[cand]; !ok {
			return cand
		}
	}
}
