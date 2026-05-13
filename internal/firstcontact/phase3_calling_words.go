package firstcontact

import (
	"context"
	"fmt"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
	"github.com/LucianoXu/eidopsyche/internal/ontology"
	"github.com/LucianoXu/eidopsyche/internal/prompts"
)

// Phase3CallingWords renders the default calling-words and lets the
// operator accept or edit. Sets s.CallingWords. Must run after Phase3
// (which sets s.SummonedName / s.PrefabID / s.Lang and on the scratch
// path s.Displaying) and before Phase4 (which writes s.CallingWords to
// the new mind-form's volume via WriteVolume).
//
// Scratch path: drafts via claude using the same prompt the old Phase 4
// used. Prefab path: renders prefab/<id>/self/calling-words.md.tpl
// host-side via ontology.RenderPrefabFile so the operator sees the
// same text TarStreamPrefab would have produced.
//
// Empty edit is allowed (per design): the operator can opt out of any
// calling-words. Phase 4 will write an empty file in that case, which
// is enough for supervisor's existence check.
func Phase3CallingWords(ctx context.Context, s *Summoning, r render.Renderer, c *Claude) error {
	var defaultWords string
	if s.PrefabID == "" {
		// Scratch path — claude drafts from the summoning book.
		st := r.Status(stringFor(s.Lang, "phase3_calling_words_status"))
		book := RenderSummoningBook(s)
		w, err := c.CallText(ctx, prompts.CallingWords(book, s.Lang))
		st.Stop()
		if err != nil {
			return fmt.Errorf("phase3 calling-words (scratch): %w", err)
		}
		defaultWords = w
	} else {
		// Prefab path — render the prefab's own .tpl host-side.
		params := ontology.Params{
			Label:        s.SummonedName,
			OwnerNpub:    s.MasterNpub,
			OwnerLabel:   s.MasterLabel,
			MindFormNpub: s.MindFormNpub,
			HomeRelay:    s.HomeRelay,
			CreatedDate:  time.Now().UTC().Format("2006-01-02"),
		}
		w, err := ontology.RenderPrefabFile(s.PrefabID, "self/calling-words.md.tpl", params)
		if err != nil {
			return fmt.Errorf("phase3 calling-words (prefab %s): %w", s.PrefabID, err)
		}
		defaultWords = w
	}

	edited, err := r.EditMultiline(
		stringFor(s.Lang, "phase3_calling_words_prompt"),
		defaultWords)
	if err != nil {
		return err
	}
	s.CallingWords = edited
	return nil
}
