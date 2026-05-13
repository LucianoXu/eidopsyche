package render

// askKind discriminates the renderer method that produced an askMsg.
type askKind int

const (
	kindPrompt        askKind = iota // single-line text input
	kindMultiline                    // textarea (Esc-then-Enter to submit)
	kindPromptChoice                 // arrow-key list
	kindShow                         // print one-shot text
	kindFrame                        // print full-screen frame (transcript-style: just inserts content)
	kindTypewriter                   // body added to the transcript (markdown if opts.HelpText=="markdown")
	kindLogo                         // logo render
	kindEditMultiline                // textarea pre-filled with body (Ctrl+D submit / Esc cancel)
)

// askMsg is the worker → TUI message. The Model's Update switches on
// kind to decide which widget to activate.
type askMsg struct {
	kind     askKind
	question string
	opts     PromptOpts
	choices  []ChoiceOption
	body     string
}

// replyMsg is the TUI → worker message sent through the renderer's
// reply channel after the user submits input or after a fire-and-act
// op (Show/Frame/Typewriter/Logo) finishes rendering.
type replyMsg struct {
	text string // for Prompt: the typed text; for PromptChoice: the index as a string
	err  error
}

// startStatusMsg / updateStatusMsg / stopStatusMsg drive a long-running
// status line that ticks while a claude call or boot-wake runs.
type startStatusMsg struct {
	id      int
	message string
}

type updateStatusMsg struct {
	id      int
	message string
}

type stopStatusMsg struct {
	id int
}

// workerDoneMsg signals that the phase function returned. The TUI
// renders the final state and quits on receipt.
type workerDoneMsg struct {
	err error
}
