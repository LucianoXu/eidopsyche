package render

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// NewCLI returns a CLI Renderer that reads from in and writes to out.
// Pass os.Stdin / os.Stdout for production use. cps is the typewriter
// rate (chars per second) — pass 0 to fast-path to instant rendering.
func NewCLI(in io.Reader, out io.Writer, cps int) Renderer {
	caps := Capabilities{}
	if f, ok := out.(*os.File); ok {
		if term.IsTerminal(int(f.Fd())) {
			caps.IsTTY = true
			caps.ANSI = os.Getenv("TERM") != "dumb"
			caps.Color = caps.ANSI
		}
	}
	return &cliRenderer{
		in:   bufio.NewReader(in),
		out:  out,
		caps: caps,
		cps:  cps,
	}
}

type cliRenderer struct {
	in   *bufio.Reader
	out  io.Writer
	caps Capabilities
	cps  int
}

func (r *cliRenderer) Capabilities() Capabilities { return r.caps }

func (r *cliRenderer) Frame(content string) {
	if r.caps.ANSI {
		fmt.Fprint(r.out, "\x1b[2J\x1b[H")
	} else {
		fmt.Fprintln(r.out)
		fmt.Fprintln(r.out)
	}
	fmt.Fprintln(r.out, content)
}

func (r *cliRenderer) Show(text string) {
	fmt.Fprintln(r.out, text)
}

func (r *cliRenderer) Typewriter(ctx context.Context, text string) {
	if !r.caps.IsTTY || r.cps <= 0 {
		fmt.Fprintln(r.out, text)
		return
	}
	delay := time.Second / time.Duration(r.cps)
	for _, ch := range text {
		select {
		case <-ctx.Done():
			fmt.Fprintln(r.out)
			return
		default:
		}
		fmt.Fprintf(r.out, "%c", ch)
		if f, ok := r.out.(*os.File); ok {
			_ = f.Sync()
		}
		time.Sleep(delay)
	}
	fmt.Fprintln(r.out)
}

func (r *cliRenderer) Prompt(question string, opts PromptOpts) (string, error) {
	for {
		fmt.Fprintln(r.out, question)
		if opts.HelpText != "" {
			fmt.Fprintln(r.out, "  ("+opts.HelpText+")")
		}
		fmt.Fprint(r.out, "> ")
		var text string
		if opts.Multiline {
			var sb strings.Builder
			for {
				line, err := r.in.ReadString('\n')
				if err != nil && line == "" {
					return "", err
				}
				if strings.TrimRight(line, "\r\n") == "" && sb.Len() > 0 {
					break
				}
				if strings.TrimRight(line, "\r\n") == "" {
					if err == io.EOF {
						break
					}
					continue
				}
				sb.WriteString(line)
				if err == io.EOF {
					break
				}
			}
			text = strings.TrimSpace(sb.String())
		} else {
			line, err := r.in.ReadString('\n')
			if err != nil && line == "" {
				return "", err
			}
			text = strings.TrimSpace(line)
		}
		if text == "" && !opts.AllowEmpty {
			fmt.Fprintln(r.out, "  (blank input — please answer)")
			continue
		}
		return text, nil
	}
}

func (r *cliRenderer) PromptChoice(question string, options []ChoiceOption) (int, error) {
	for {
		fmt.Fprintln(r.out, question)
		for i, o := range options {
			fmt.Fprintf(r.out, "  [%d] %s", i+1, o.Label)
			if o.Hint != "" {
				fmt.Fprintf(r.out, "    (%s)", o.Hint)
			}
			fmt.Fprintln(r.out)
		}
		fmt.Fprint(r.out, "> ")
		line, err := r.in.ReadString('\n')
		if err != nil && line == "" {
			return 0, err
		}
		s := strings.TrimSpace(line)
		var n int
		if _, perr := fmt.Sscanf(s, "%d", &n); perr == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		fmt.Fprintln(r.out, "  (please enter a number from the list)")
	}
}

type cliStatus struct {
	out  io.Writer
	caps Capabilities
	mu   sync.Mutex
	done bool
}

func (s *cliStatus) Update(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	if s.caps.ANSI {
		fmt.Fprintf(s.out, "\r\x1b[K · %s", message)
	} else {
		fmt.Fprintf(s.out, " · %s\n", message)
	}
}

func (s *cliStatus) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	s.done = true
	if s.caps.ANSI {
		fmt.Fprintln(s.out)
	}
}

func (r *cliRenderer) Status(message string) StatusHandle {
	s := &cliStatus{out: r.out, caps: r.caps}
	if r.caps.ANSI {
		fmt.Fprintf(r.out, " · %s", message)
	} else {
		fmt.Fprintln(r.out, " · "+message)
	}
	return s
}

// Logo renders the EIDOPSYCHE letter circle. ANSI-capable terminals
// see a brief flicker effect; piped / dumb terminals get a single
// static print. A true rotating-glyph implementation is deferred until
// we have a terminal-render test harness.
func (r *cliRenderer) Logo(ctx context.Context, d time.Duration) {
	const static = `
       E   I
     D       O
    P    *    P
     S       Y
       C   H
         E
`
	if !r.caps.ANSI {
		fmt.Fprint(r.out, static)
		return
	}
	frames := int(d.Milliseconds() / 200)
	for i := 0; i < frames; i++ {
		select {
		case <-ctx.Done():
			fmt.Fprint(r.out, "\x1b[2J\x1b[H")
			fmt.Fprint(r.out, static)
			return
		default:
		}
		fmt.Fprint(r.out, "\x1b[2J\x1b[H")
		if i%2 == 0 {
			fmt.Fprint(r.out, static)
		} else {
			fmt.Fprint(r.out, strings.ReplaceAll(static, "*", "·"))
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Fprint(r.out, "\x1b[2J\x1b[H")
	fmt.Fprint(r.out, static)
}
