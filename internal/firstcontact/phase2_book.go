package firstcontact

import (
	"context"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

// Phase2Deps are the inputs Phase2 cannot derive from Summoning.
type Phase2Deps struct {
	// ExistingSlugs is the list of slugs already in use (i.e. existing
	// MindForm volumes), used by Derive to avoid collisions.
	ExistingSlugs []string
}

// Phase2 runs the three-step summoning-book core: character question
// → claude research → claude displaying paragraph (typewriter) →
// naming. The system handle (slug) is auto-derived from the name so
// the ritual's high point — naming the to-be-summoned — is not
// followed by a second, prosaic prompt.
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

	// Step 4: naming. Slug is derived silently; we never ask for it.
	name, err := r.Prompt(stringFor(s.Lang, "phase2_naming_q"), render.PromptOpts{})
	if err != nil {
		return err
	}
	s.SummonedName = name
	s.Slug = Derive(name, d.ExistingSlugs)
	return nil
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
	return fmt.Sprintf(`The operator just described a character they want to summon. Write 3 to 5 sentences sketching this character — what they look like, where they are, what they happen to be doing. Write the way one person might quietly tell another what they are seeing.

Constraints (HARD):
- The figure does NOT speak yet; they have not arrived.
- Do NOT name any source work or original character.
- No attribute lists, no bold, no headings.
- Settings to draw on: %v
- Imagery to draw on: %v
- Temperament: %s. World: %s. Archetype: %s.

Language: %s.

Tone: plain and sincere, the way a real person speaks. No "宛如 / 仿佛 / 朦胧 / 缥缈" stacking, no archaic register, no elevated diction, no theatrical solemnity. Short sentences are fine. If a line sounds like it was written for a poetry recital, rewrite it. The reader should feel they could meet this person on a Tuesday afternoon, not only in a dream.`,
		p.Settings, p.Imagery, p.Temperament, p.World, p.Archetype, lang)
}
