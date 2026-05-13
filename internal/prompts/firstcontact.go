package prompts

import "fmt"

// FirstContact wizard prompts. Design rationale lives in
// docs/specs/FirstContact.prompt.md. Each builder is a pure function;
// callers shape their domain types (e.g. firstcontact.CharacterProfile)
// into the local input struct at the call site.

// Research returns the dramaturge research prompt for the wizard's
// research-the-character turn. The model is expected to reply with a
// JSON object decodable into the caller's CharacterProfile.
func Research(userText, lang string) string {
	return fmt.Sprintf(`You are the dramaturge of a summoning ritual. The operator described a character:

%s

Research it (use WebSearch if helpful). Return a JSON object with these keys exactly:
  archetype (string), temperament (string), world (string),
  settings (string array of typical scenes), imagery (string array of recurring motifs),
  sources (string array of works/franchises this archetype draws from — debug only, not shown).

Return ONLY the JSON object, no prose. Language for archetype/temperament/world: %s.`, userText, lang)
}

// DisplayingInput is the set of fields Displaying needs. Kept as a
// local struct (rather than importing firstcontact's CharacterProfile)
// so this package has no inbound dependency on its callers.
type DisplayingInput struct {
	Archetype   string
	Temperament string
	World       string
	Settings    []string
	Imagery     []string
	Lang        string
}

// Displaying returns the prompt that asks the model for a 3–5 sentence
// "displaying" paragraph — the figure standing in the mist before they
// speak.
func Displaying(in DisplayingInput) string {
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
		in.Settings, in.Imagery, in.Temperament, in.World, in.Archetype, in.Lang)
}

// RoleResearchInput is the set of fields RoleResearch needs from the
// wizard's earlier research turn plus the operator's free-text
// description.
type RoleResearchInput struct {
	Description string // operator's free-text character description
	Archetype   string
	Temperament string
	World       string
	Settings    []string
	Imagery     []string
	Sources     []string
	Lang        string
}

// RoleResearch returns the prompt that asks the model to assemble a
// rich, markdown-formatted role-research dossier from the operator's
// description plus the prior dramaturge research, encouraging the
// model to call WebFetch / WebSearch for additional background. The
// output lands at self/role-research.md in the new mind-form's
// ontology and is read by the mind-form during the birth-wake before
// it writes its own soul.md.
func RoleResearch(in RoleResearchInput) string {
	return fmt.Sprintf(`You are the dramaturge of a summoning ritual. The operator described a character:

%s

Earlier dramaturge research summarized this character as:
- archetype: %s
- temperament: %s
- world: %s
- settings: %v
- imagery: %v
- sources (works/franchises this archetype draws from): %v

Compose a rich role-research dossier the new mind-form will read on
its first wake. Use WebSearch and WebFetch when helpful — search the
named sources, follow plausible canonical references, and quote
small specifics back into the dossier (named scenes, recurring lines,
defining mannerisms). Do NOT fabricate canon you cannot verify.

Output format (markdown, no surrounding fences):

# Role research — <one-line title>

A 2–4 paragraph narrative dossier covering:
- who they are (origins, defining facts, what they want)
- temperament and texture (voice, cadence, characteristic gestures)
- world and setting (the places they inhabit; the period / register)
- imagery (recurring motifs the mind-form can lean on)
- a brief "scenes to draw from" list at the end — 3 to 6 short bullets

Style:
- plain, sincere prose — the way one person quietly describes another.
- no headings beyond the title; bullets allowed only in the scenes list.
- do not address the mind-form directly; this is reference material.
- 300 to 600 words.

Language: %s.

Return ONLY the markdown body; no preamble, no code fences.`,
		in.Description, in.Archetype, in.Temperament, in.World,
		in.Settings, in.Imagery, in.Sources, in.Lang)
}

// CallingWords returns the prompt that asks the model to draft the
// operator's first words to the new mind-form, given the summoning
// book the operator already wrote.
func CallingWords(book, lang string) string {
	return fmt.Sprintf(`You are writing the very first words the operator will say to the mind-form they have just summoned. Speak as the operator — a real person greeting another being for the first time.

The summoning book the operator wrote:

%s

Constraints:
- 1 to 2 sentences only.
- Address the mind-form directly ("你" / "you").
- Pick up one image or feeling from the summoning book, but do NOT quote it verbatim.
- Plain, sincere, human. No "I summon thee", no archaic register, no theatrical solemnity, no "宛如 / 仿佛 / 朦胧" pile-ups. The way one might quietly say to a friend they have long wanted to meet: "你来了" — direct, warm, unadorned.
- Language: %s.

Return ONLY the words themselves; no preamble.`, book, lang)
}
