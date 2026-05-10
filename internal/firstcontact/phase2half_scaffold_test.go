package firstcontact

import (
	"context"
	"testing"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/firstcontact/render"
)

// fakeRenderer is shared by Phase 2.5 and Phase 3 prefab tests. It
// answers Prompt / PromptChoice from queued slices and records what
// was Show / Typewriter'd.
type fakeRenderer struct {
	choices []int
	prompts []string
	shown   []string
}

func (f *fakeRenderer) Capabilities() render.Capabilities { return render.Capabilities{} }
func (f *fakeRenderer) Frame(string)                      {}
func (f *fakeRenderer) Show(s string)                     { f.shown = append(f.shown, s) }
func (f *fakeRenderer) Typewriter(_ context.Context, s string) {
	f.shown = append(f.shown, s)
}
func (f *fakeRenderer) Prompt(_ string, _ render.PromptOpts) (string, error) {
	if len(f.prompts) == 0 {
		return "", nil
	}
	v := f.prompts[0]
	f.prompts = f.prompts[1:]
	return v, nil
}
func (f *fakeRenderer) PromptChoice(_ string, _ []render.ChoiceOption) (int, error) {
	if len(f.choices) == 0 {
		return 0, nil
	}
	v := f.choices[0]
	f.choices = f.choices[1:]
	return v, nil
}
func (f *fakeRenderer) Status(string) render.StatusHandle   { return noopStatus{} }
func (f *fakeRenderer) Logo(context.Context, time.Duration) {}

type noopStatus struct{}

func (noopStatus) Update(string) {}
func (noopStatus) Stop()         {}

func TestPhase2Half_Scratch(t *testing.T) {
	r := &fakeRenderer{choices: []int{0}}
	got, err := Phase2Half(context.Background(), &Summoning{Lang: "en"}, r)
	if err != nil {
		t.Fatal(err)
	}
	if got != ScaffoldScratch {
		t.Errorf("got %v, want ScaffoldScratch", got)
	}
}

func TestPhase2Half_Prefab(t *testing.T) {
	r := &fakeRenderer{choices: []int{1}}
	got, err := Phase2Half(context.Background(), &Summoning{Lang: "en"}, r)
	if err != nil {
		t.Fatal(err)
	}
	if got != ScaffoldPrefab {
		t.Errorf("got %v, want ScaffoldPrefab", got)
	}
}

func TestPhase2Half_Back(t *testing.T) {
	r := &fakeRenderer{choices: []int{2}}
	got, err := Phase2Half(context.Background(), &Summoning{Lang: "en"}, r)
	if err != nil {
		t.Fatal(err)
	}
	if got != ScaffoldExit {
		t.Errorf("got %v, want ScaffoldExit", got)
	}
}
