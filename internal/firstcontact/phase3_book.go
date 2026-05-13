package firstcontact

import (
	"context"
	"fmt"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
	"github.com/LucianoXu/eidopsyche/internal/prompts"
)

// Phase3BookDeps are the inputs Phase 3 (the summoning-book core)
// cannot derive from Summoning.
type Phase3BookDeps struct {
	// ExistingSlugs is the list of slugs already in use (i.e. existing
	// MindForm volumes), used by Derive to avoid collisions.
	ExistingSlugs []string
}

// Phase3 runs the three-step summoning-book core: character question
// → claude research → claude displaying paragraph (typewriter) →
// naming. The system handle (slug) is auto-derived from the name so
// the ritual's high point — naming the to-be-summoned — is not
// followed by a second, prosaic prompt.
func Phase3(ctx context.Context, s *Summoning, r render.Renderer, c *Claude, d Phase3BookDeps) error {
	// Step 1: character question (multiline).
	prompt, err := r.Prompt(stringFor(s.Lang, "phase3_character_q"), render.PromptOpts{Multiline: true})
	if err != nil {
		return err
	}
	s.CharacterPrompt = prompt

	// Step 2: research.
	st := r.Status(stringFor(s.Lang, "phase3_research_status"))
	if err := c.Call(ctx, prompts.Research(s.CharacterPrompt, s.Lang), &s.Profile); err != nil {
		st.Stop()
		return fmt.Errorf("phase3 research: %w", err)
	}
	st.Update(stringFor(s.Lang, "phase3_displaying_status"))

	// Step 3: displaying paragraph.
	displaying, err := c.CallText(ctx, prompts.Displaying(prompts.DisplayingInput{
		Archetype:   s.Profile.Archetype,
		Temperament: s.Profile.Temperament,
		World:       s.Profile.World,
		Settings:    s.Profile.Settings,
		Imagery:     s.Profile.Imagery,
		Lang:        s.Lang,
	}))
	if err != nil {
		st.Stop()
		return fmt.Errorf("phase3 displaying: %w", err)
	}
	s.Displaying = displaying
	st.Stop()
	r.Typewriter(ctx, displaying)

	// Step 3.5: role-research. Compose a richer dossier (300-600 words,
	// markdown) that combines the operator's description, the Profile,
	// and live web research. The mind-form reads this on birth-wake
	// before writing its own soul.md. A failure here is fatal —
	// without role-research the birth handler's precondition check
	// will refuse to spawn claude. Status indicator stays up so the
	// operator sees the wait is alive.
	stRR := r.Status(stringFor(s.Lang, "phase3_role_research_status"))
	research, err := c.CallText(ctx, prompts.RoleResearch(prompts.RoleResearchInput{
		Description: s.CharacterPrompt,
		Archetype:   s.Profile.Archetype,
		Temperament: s.Profile.Temperament,
		World:       s.Profile.World,
		Settings:    s.Profile.Settings,
		Imagery:     s.Profile.Imagery,
		Sources:     s.Profile.Sources,
		Lang:        s.Lang,
	}))
	stRR.Stop()
	if err != nil {
		return fmt.Errorf("phase3 role-research: %w", err)
	}
	s.RoleResearch = research

	// Step 4: naming. Slug is derived silently; we never ask for it.
	name, err := r.Prompt(stringFor(s.Lang, "phase3_naming_q"), render.PromptOpts{})
	if err != nil {
		return err
	}
	s.SummonedName = name
	s.Slug = Derive(name, d.ExistingSlugs)
	return nil
}
