package firstcontact

import (
	"context"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

// Phase0 displays the EIDOPSYCHE logo, asks the operator to pick a
// language, and prints the one-shot intro paragraph. It does NOT call
// claude — every string is hardcoded.
func Phase0(ctx context.Context, s *Summoning, r render.Renderer) error {
	r.Logo(ctx, 3*time.Second)
	idx, err := r.PromptChoice("Language? · 语言？", []render.ChoiceOption{
		{Label: "中文"},
		{Label: "English"},
	})
	if err != nil {
		return err
	}
	if idx == 0 {
		s.Lang = "zh"
	} else {
		s.Lang = "en"
	}
	r.Show(stringFor(s.Lang, "phase0_intro"))
	return nil
}
