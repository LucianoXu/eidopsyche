package prompts

import "fmt"

// birthBoot is the embedded birth-wake boot prompt. Loaded once at init
// rather than each call so a corrupt embed surfaces at process start.
var birthBoot = mustLoad("assets/birth.txt")

func mustLoad(name string) string {
	b, err := assets.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("prompts: embedded asset %q missing: %v", name, err))
	}
	return string(b)
}

// BirthBoot returns the birth-wake boot prompt the supervisor appends
// after the constitution-augmented identity. The returned text is
// stable across calls.
func BirthBoot() string {
	return birthBoot
}

// BirthUser builds the user-message half of the birth-wake invocation:
// operator npub plus the two scripture fragments (summoning book and
// calling-words) the agent has been told to read in BirthBoot.
func BirthUser(operatorNpub, summoningBook, callingWords string) string {
	return fmt.Sprintf(
		"Operator npub: %s\n\n--- summoning book ---\n%s\n\n--- calling-words ---\n%s\n",
		operatorNpub, summoningBook, callingWords,
	)
}
