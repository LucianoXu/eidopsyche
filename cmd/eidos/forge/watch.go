package forge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/forgectl"
	"github.com/LucianoXu/eidopsyche/internal/transcript"
	"github.com/spf13/cobra"
)

func newWatchCmd() *cobra.Command {
	var (
		wakeArg      string
		listOnly     bool
		limit        int
		showThinking bool
		noFollow     bool
		raw          bool
	)
	cmd := &cobra.Command{
		Use:   "watch <name>",
		Short: "Stream a mind-form's headless reasoning chain (claude thinking + tool calls + results)",
		Long: `Render the structured stream-json transcript captured by agent-runner.

Defaults to following the current wake. With --wake <id> renders a past
wake (use --list to see ids). With --raw the host renders nothing and
just passes the in-container NDJSON through stdout — pipe to jq to
script around it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := forgectl.ValidateName(name); err != nil {
				return err
			}
			c, err := forgectl.New()
			if err != nil {
				return err
			}
			cont := forgectl.ContainerName(name)
			if listOnly {
				return runWatchList(cmd, c, cont, limit)
			}
			opts := renderOpts{ShowThinking: showThinking}
			return runWatchTail(cmd.Context(), cmd.OutOrStdout(), c, cont, wakeArg, !noFollow, raw, opts)
		},
	}
	cmd.Flags().StringVar(&wakeArg, "wake", "current", "wake id to render (default: current); 'current' follows the active wake")
	cmd.Flags().BoolVar(&listOnly, "list", false, "list recent wakes instead of streaming a transcript")
	cmd.Flags().IntVar(&limit, "limit", 0, "with --list, show at most N most recent wakes (0 = all)")
	cmd.Flags().BoolVar(&showThinking, "thinking", false, "expand assistant.thinking blocks instead of collapsing them")
	cmd.Flags().BoolVar(&noFollow, "no-follow", false, "stop at end of file instead of waiting for new events")
	cmd.Flags().BoolVar(&raw, "raw", false, "passthrough the NDJSON event stream without rendering")
	return cmd
}

// runWatchList shows the table of recent wakes by execing
// `transcript-list --json` in the container and rendering the index.
func runWatchList(cmd *cobra.Command, c forgectl.Client, cont string, limit int) error {
	args := []string{"eidos", "forge", "transcript-list", "--json"}
	res, err := c.ContainerExec(cmd.Context(), cont, args)
	if err != nil {
		return fmt.Errorf("transcript-list: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("transcript-list exited %d: %s", res.ExitCode, string(res.Stderr))
	}
	var idx transcript.Index
	if err := json.Unmarshal(res.Stdout, &idx); err != nil {
		return fmt.Errorf("parse index: %w", err)
	}
	out := cmd.OutOrStdout()
	for _, line := range renderListTableForWatch(idx, limit) {
		fmt.Fprintln(out, line)
	}
	return nil
}

// runWatchTail streams the transcript (raw or rendered).
//
// Default behaviour (--wake current) follows the active wake; when no
// wake is active, the most recent finalised wake is dumped first as a
// starter, then the loop waits for a new wake to land. With --no-follow
// the function dumps once and exits.
//
// An explicit --wake <id> resolves the id (or unique prefix) against
// the index and renders that single wake.
func runWatchTail(ctx context.Context, out io.Writer, c forgectl.Client, cont, wakeArg string, follow, raw bool, opts renderOpts) error {
	if wakeArg != "current" {
		resolved, err := resolveWakeID(ctx, c, cont, wakeArg)
		if err != nil {
			return err
		}
		wakeArg = resolved
	}

	dumpedLatest := false
	prevSessionID := ""
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		args := []string{"eidos", "forge", "transcript-tail", "--wake", wakeArg}
		if follow {
			args = append(args, "--follow")
		}
		wctx := buildWakeRenderCtx(ctx, c, cont, wakeArg)
		if shouldRenderBoundary(prevSessionID, wctx.SessionID) {
			for _, l := range renderSessionBoundary(wctx.SessionID) {
				fmt.Fprintln(out, l)
			}
		}
		if wctx.SessionID != "" {
			prevSessionID = wctx.SessionID
		}
		emitted, streamErr := streamTranscript(ctx, out, c, cont, args, raw, opts, wctx)
		if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
			return streamErr
		}
		if !follow {
			return nil
		}
		// Follow mode reached this point: --wake current --follow either
		// (a) returned immediately because no wake is active (emitted=0)
		// or (b) returned after the current wake completed (emitted>0).
		// Case (a): show the most recent historical wake once before
		// looping, so an operator opening the command on a sleeping
		// mind-form sees the latest reasoning rather than an empty
		// terminal. Case (b): no need to re-dump — the user just
		// watched the wake live.
		if wakeArg == "current" && !emitted && !dumpedLatest {
			if latest := latestWakeID(ctx, c, cont); latest != "" {
				latestArgs := []string{"eidos", "forge", "transcript-tail", "--wake", latest}
				latestCtx := buildWakeRenderCtx(ctx, c, cont, latest)
				if shouldRenderBoundary(prevSessionID, latestCtx.SessionID) {
					for _, l := range renderSessionBoundary(latestCtx.SessionID) {
						fmt.Fprintln(out, l)
					}
				}
				if latestCtx.SessionID != "" {
					prevSessionID = latestCtx.SessionID
				}
				if _, err := streamTranscript(ctx, out, c, cont, latestArgs, raw, opts, latestCtx); err != nil && !errors.Is(err, context.Canceled) {
					return err
				}
			}
		}
		dumpedLatest = true

		if wakeArg == "current" {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(watchPollInterval):
			}
			continue
		}
		// An explicit --wake <id> is one-shot in spirit; --follow is a
		// no-op once the file is final.
		return nil
	}
}

// buildWakeRenderCtx fetches index + runtime-state to build the per-wake
// header context. Returns zero value when the lookups fail (the renderer
// then falls back to the legacy header). The "current" wake uses
// runtime-state's session info (the active session's UUID + an ordinal
// of WakesInSession+1, matching what IncrementWake will record at the
// end of the wake).
func buildWakeRenderCtx(ctx context.Context, c forgectl.Client, cont, wakeArg string) wakeRenderCtx {
	if wakeArg == "current" {
		res, err := c.ContainerExec(ctx, cont, []string{"eidos", "forge", "runtime-state"})
		if err != nil || res.ExitCode != 0 {
			return wakeRenderCtx{}
		}
		var rs RuntimeState
		if err := json.Unmarshal(res.Stdout, &rs); err != nil || rs.SessionID == "" {
			return wakeRenderCtx{}
		}
		short := rs.SessionID
		if len(short) > 8 {
			short = short[:8]
		}
		wakeShort := rs.ActiveWakeID
		if len(wakeShort) > 8 {
			wakeShort = wakeShort[:8]
		}
		return wakeRenderCtx{
			WakeID:    wakeShort,
			SessionID: short,
			Ordinal:   rs.WakesInSession + 1,
		}
	}
	res, err := c.ContainerExec(ctx, cont, []string{"eidos", "forge", "transcript-list", "--json"})
	if err != nil || res.ExitCode != 0 {
		return wakeRenderCtx{}
	}
	var idx transcript.Index
	if err := json.Unmarshal(res.Stdout, &idx); err != nil {
		return wakeRenderCtx{}
	}
	for _, e := range idx.Wakes {
		if e.ID != wakeArg || e.SessionID == "" {
			continue
		}
		short := e.SessionID
		if len(short) > 8 {
			short = short[:8]
		}
		wakeShort := e.ID
		if len(wakeShort) > 8 {
			wakeShort = wakeShort[:8]
		}
		return wakeRenderCtx{
			WakeID:    wakeShort,
			SessionID: short,
			Ordinal:   computeOrdinal(idx, e.SessionID, e.ID),
		}
	}
	return wakeRenderCtx{}
}

// resolveWakeID expands a possibly-truncated wake id against the
// in-container index. Exact match wins; otherwise the input is treated
// as a prefix and must match a single wake. Returns the input verbatim
// when the index is unreadable so the caller can still attempt the
// raw id (helps when the index is missing for a fresh mind-form).
func resolveWakeID(ctx context.Context, c forgectl.Client, cont, want string) (string, error) {
	res, err := c.ContainerExec(ctx, cont, []string{"eidos", "forge", "transcript-list", "--json"})
	if err != nil || res.ExitCode != 0 {
		return want, nil
	}
	var idx transcript.Index
	if jerr := json.Unmarshal(res.Stdout, &idx); jerr != nil {
		return want, nil
	}
	var prefixes []string
	for _, w := range idx.Wakes {
		if w.ID == want {
			return want, nil
		}
		if len(want) > 0 && len(w.ID) > len(want) && w.ID[:len(want)] == want {
			prefixes = append(prefixes, w.ID)
		}
	}
	if len(prefixes) == 1 {
		return prefixes[0], nil
	}
	if len(prefixes) > 1 {
		return "", fmt.Errorf("wake id prefix %q is ambiguous (matches %d wakes: %v)", want, len(prefixes), prefixes)
	}
	// Pass through unchanged — let transcript-tail return its own
	// not-found error so the host doesn't second-guess the operator.
	return want, nil
}

// latestWakeID returns the id of the most recently started finalised
// wake, or "" if the index is empty / unreadable.
func latestWakeID(ctx context.Context, c forgectl.Client, cont string) string {
	res, err := c.ContainerExec(ctx, cont, []string{"eidos", "forge", "transcript-list", "--json", "--limit", "1"})
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	var idx transcript.Index
	if jerr := json.Unmarshal(res.Stdout, &idx); jerr != nil {
		return ""
	}
	if len(idx.Wakes) == 0 {
		return ""
	}
	return idx.Wakes[0].ID
}

// watchPollInterval is the gap between transcript-tail invocations
// when there's no current wake. var so tests can shrink it.
var watchPollInterval = 1 * time.Second

// streamTranscript runs transcript-tail in the container, parses the
// resulting NDJSON, and either passes it through (--raw) or renders it.
//
// Returns whether any bytes were written to out. The caller uses this
// to distinguish "ran but produced nothing" (no current wake, idle
// follow) from "ran and rendered events".
//
// Implementation note: forgectl.Client.ContainerExec returns the buffered
// stdout once exec completes — fine for one-shot reads, not for follow.
// For --follow we need a streaming exec; we shell out to `docker exec`
// directly to get a continuous pipe.
func streamTranscript(ctx context.Context, out io.Writer, c forgectl.Client, cont string, args []string, raw bool, opts renderOpts, wctx wakeRenderCtx) (emitted bool, err error) {
	dockerArgs := append([]string{"exec", cont}, args...)
	cmd := exec.CommandContext(ctx, "docker", dockerArgs...) //nolint:gosec
	cmd.Stderr = os.Stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return false, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("docker exec: %w", err)
	}

	cw := &countingWriter{w: out}
	rerr := renderStream(cw, pipe, raw, opts, wctx)
	werr := cmd.Wait()
	if rerr != nil && !errors.Is(rerr, io.EOF) {
		return cw.n > 0, rerr
	}
	if werr != nil {
		var exitErr *exec.ExitError
		if errors.As(werr, &exitErr) && exitErr.ExitCode() != 0 {
			return cw.n > 0, fmt.Errorf("transcript-tail in %s: %w", cont, werr)
		}
	}
	return cw.n > 0, nil
}

// countingWriter counts bytes written through it. Used by
// streamTranscript to report whether the in-container subcommand
// produced any output.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func renderStream(out io.Writer, src io.Reader, raw bool, opts renderOpts, wctx wakeRenderCtx) error {
	r := bufio.NewReaderSize(src, 1<<20)
	for {
		line, err := readLineUnbounded(r)
		if len(line) > 0 {
			if raw {
				if _, werr := out.Write(line); werr != nil {
					return werr
				}
			} else {
				ev, perr := transcript.ParseEvent(line)
				if perr != nil {
					// Unknown line — surface to stderr so the user can
					// choose to investigate; renderer keeps going.
					fmt.Fprintf(os.Stderr, "watch: parse: %v\n", perr)
				} else {
					for _, l := range renderEvent(ev, opts, wctx) {
						fmt.Fprintln(out, l)
					}
				}
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// readLineUnbounded matches the helper in supervisor/agent_runner.go:
// reads up to and including the next '\n' from r, joining as many
// ReadSlice chunks as needed. A trailing partial line at EOF is
// returned with (line, io.EOF).
func readLineUnbounded(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return buf, err
	}
}
