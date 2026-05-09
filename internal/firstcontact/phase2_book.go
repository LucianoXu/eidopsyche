package firstcontact

import (
	"context"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
	"github.com/LucianoXu/eidopsyche/internal/forgectl"
)

// Phase2Deps are the inputs Phase2 cannot derive from Summoning.
type Phase2Deps struct {
	// ExistingSlugs is the list of slugs already in use (i.e. existing
	// MindForm volumes), used by Derive to avoid collisions.
	ExistingSlugs []string
}

// Phase2 runs the four-step summoning-book core: character question
// → claude research → claude displaying paragraph (typewriter) →
// naming + slug confirmation. Mutates s in place.
func Phase2(ctx context.Context, s *Summoning, r render.Renderer, c *Claude, d Phase2Deps) error {
	// Step 1: character question (multiline).
	prompt, err := r.Prompt(stringFor(s.Lang, "phase2_character_q"), render.PromptOpts{Multiline: true})
	if err != nil {
		return err
	}
	s.CharacterPrompt = prompt

	// Step 2: research.
	st := r.Status(stringFor(s.Lang, "phase2_research_status"))
	if err := c.Call(ctx, buildResearchPrompt(s.CharacterPrompt, s.Lang), &s.Profile); err != nil {
		st.Stop()
		return fmt.Errorf("phase2 research: %w", err)
	}
	st.Update(stringFor(s.Lang, "phase2_displaying_status"))

	// Step 3: displaying paragraph.
	displaying, err := c.CallText(ctx, buildDisplayingPrompt(s.Profile, s.Lang))
	if err != nil {
		st.Stop()
		return fmt.Errorf("phase2 displaying: %w", err)
	}
	s.Displaying = displaying
	st.Stop()
	r.Typewriter(ctx, displaying)

	// Step 4: naming + slug confirmation.
	name, err := r.Prompt(stringFor(s.Lang, "phase2_naming_q"), render.PromptOpts{})
	if err != nil {
		return err
	}
	s.SummonedName = name
	derived := Derive(name, d.ExistingSlugs)
	confirmed, err := confirmSlug(r, s.Lang, derived, d.ExistingSlugs)
	if err != nil {
		return err
	}
	s.Slug = confirmed
	return nil
}

// confirmSlug shows the operator the derived slug and lets them
// override. Empty input (Enter) accepts the derived value. Invalid or
// taken slugs re-prompt with a one-line reason.
func confirmSlug(r render.Renderer, lang, derived string, existing []string) (string, error) {
	for {
		input, err := r.Prompt(
			fmt.Sprintf(stringFor(lang, "phase2_slug_q"), derived),
			render.PromptOpts{AllowEmpty: true},
		)
		if err != nil {
			return "", err
		}
		if input == "" {
			return derived, nil
		}
		if err := forgectl.ValidateName(input); err != nil {
			r.Show(stringFor(lang, "phase2_slug_invalid"))
			continue
		}
		taken := false
		for _, e := range existing {
			if e == input {
				taken = true
				break
			}
		}
		if taken {
			r.Show(stringFor(lang, "phase2_slug_taken"))
			continue
		}
		return input, nil
	}
}

func buildResearchPrompt(userText, lang string) string {
	return fmt.Sprintf(`You are the dramaturge of a summoning ritual. The operator described a character:

%s

Research it (use WebSearch if helpful). Return a JSON object with these keys exactly:
  archetype (string), temperament (string), world (string),
  settings (string array of typical scenes), imagery (string array of recurring motifs),
  sources (string array of works/franchises this archetype draws from — debug only, not shown).

Return ONLY the JSON object, no prose. Language for archetype/temperament/world: %s.`, userText, lang)
}

func buildDisplayingPrompt(p CharacterProfile, lang string) string {
	return fmt.Sprintf(`The operator is writing a summoning book and a figure is taking shape in its words. Write 3 to 5 sentences of the figure's "displaying" — the moment they appear in the book's lines, before they have arrived to speak.

Constraints (HARD):
- The figure does NOT speak.
- Do NOT name any source work or original character.
- No attribute lists, no bold, no headings.
- Imagery should evoke: %v (settings); %v (imagery).
- Temperament: %s. World: %s. Archetype: %s.

Language: %s. Tone: poetic, restrained.`,
		p.Settings, p.Imagery, p.Temperament, p.World, p.Archetype, lang)
}
