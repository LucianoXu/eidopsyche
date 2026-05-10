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
// Default behaviour follows --wake current; if there is no current
// wake, dumps the most recent wake from the index, then waits for a
// new one. With --no-follow it dumps once and exits.
func runWatchTail(ctx context.Context, out io.Writer, c forgectl.Client, cont, wakeArg string, follow, raw bool, opts renderOpts) error {
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
		streamErr := streamTranscript(ctx, out, c, cont, args, raw, opts)
		if !follow {
			return streamErr
		}
		if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
			return streamErr
		}
		// Follow mode: when transcript-tail returns (wake finished or
		// no current wake), wait briefly and retry. This gives the
		// user the natural `tail -f` UX: keep watching.
		if wakeArg == "current" {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(watchPollInterval):
			}
			continue
		}
		// An explicit --wake <id> is a one-shot in spirit; --follow is
		// a no-op once the file is final.
		return nil
	}
}

// watchPollInterval is the gap between transcript-tail invocations
// when there's no current wake. var so tests can shrink it.
var watchPollInterval = 1 * time.Second

// streamTranscript runs transcript-tail in the container, parses the
// resulting NDJSON, and either passes it through (--raw) or renders it.
//
// Implementation note: forgectl.Client.ContainerExec returns the buffered
// stdout once exec completes — fine for one-shot reads, not for follow.
// For --follow we need a streaming exec; we shell out to `docker exec`
// directly to get a continuous pipe.
func streamTranscript(ctx context.Context, out io.Writer, c forgectl.Client, cont string, args []string, raw bool, opts renderOpts) error {
	// Build the docker exec command. We bypass forgectl.ContainerExec
	// because that buffers stdout to completion; we want streaming.
	dockerArgs := append([]string{"exec", cont}, args...)
	cmd := exec.CommandContext(ctx, "docker", dockerArgs...) //nolint:gosec
	cmd.Stderr = os.Stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("docker exec: %w", err)
	}

	rerr := renderStream(out, pipe, raw, opts)
	werr := cmd.Wait()
	if rerr != nil && !errors.Is(rerr, io.EOF) {
		return rerr
	}
	if werr != nil {
		// docker exec returns the in-container exit code. transcript-tail
		// exits 1 only on hard errors; a clean "no current wake" exit is
		// 0 with no output, which we render as a quiet stream.
		var exitErr *exec.ExitError
		if errors.As(werr, &exitErr) && exitErr.ExitCode() != 0 {
			return fmt.Errorf("transcript-tail in %s: %w", cont, werr)
		}
	}
	return nil
}

func renderStream(out io.Writer, src io.Reader, raw bool, opts renderOpts) error {
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
					for _, l := range renderEvent(ev, opts) {
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
