package forge

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/LucianoXu/eidopsyche/internal/transcript"
	"github.com/spf13/cobra"
)

// transcriptTailPollInterval is the poll cadence used by --follow. Var
// so tests can shrink it.
var transcriptTailPollInterval = 100 * time.Millisecond

// transcriptTailIdleTimeout: when --follow is set and the wake is no
// longer current AND the file mtime is older than this, exit cleanly
// (the wake completed and the writer is gone).
var transcriptTailIdleTimeout = 2 * time.Second

func newTranscriptTailCmd() *cobra.Command {
	var wakeArg string
	var follow bool
	cmd := &cobra.Command{
		Use:    "transcript-tail",
		Short:  "Internal: stream a wake's NDJSON to stdout (host's `forge watch` consumes this)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if wakeArg == "" {
				return errors.New("--wake is required (use 'current' or a wake id)")
			}
			s, err := transcript.NewStore(transcriptsDir)
			if err != nil {
				return err
			}
			path, kind, err := resolveWakePath(s, wakeArg)
			if err != nil {
				return err
			}
			if kind == "absent-current" {
				// No active wake — exit 0 with no output. The host
				// renderer treats this as "nothing in flight".
				return nil
			}
			return tailFile(cmd.Context().Done(), path, follow, cmd.OutOrStdout(), s, wakeArg)
		},
	}
	cmd.Flags().StringVar(&wakeArg, "wake", "", "wake id (or 'current' for the active wake)")
	cmd.Flags().BoolVar(&follow, "follow", false, "keep streaming as the wake produces new events")
	return cmd
}

// resolveWakePath maps the --wake argument to an absolute ndjson path.
// Returns kind="absent-current" with empty path when --wake=current and
// no active wake exists; this is a normal "nothing to show" signal.
func resolveWakePath(s *transcript.Store, wakeArg string) (path, kind string, err error) {
	if wakeArg == "current" {
		target, lerr := os.Readlink(s.CurrentPath())
		if lerr != nil {
			if errors.Is(lerr, fs.ErrNotExist) {
				return "", "absent-current", nil
			}
			return "", "", fmt.Errorf("read current symlink: %w", lerr)
		}
		path = filepath.Join(s.Dir, target)
		// Sanity: make sure the target exists. If it doesn't, treat as
		// absent (writer may have just cleaned up).
		if _, serr := os.Stat(path); serr != nil {
			if errors.Is(serr, fs.ErrNotExist) {
				return "", "absent-current", nil
			}
			return "", "", serr
		}
		return path, "current", nil
	}
	path = s.WakePath(wakeArg)
	if _, serr := os.Stat(path); serr != nil {
		return "", "", fmt.Errorf("wake %q not found", wakeArg)
	}
	return path, "explicit", nil
}

// tailFile writes path's bytes to w, optionally following appended bytes
// at transcriptTailPollInterval. When --wake=current, follow ends when
// the symlink no longer points here AND the file has been quiet for
// transcriptTailIdleTimeout. For an explicit wake id, --follow tails
// until idle (no growth for transcriptTailIdleTimeout) — the writer is
// gone, the file is final.
func tailFile(done <-chan struct{}, path string, follow bool, w io.Writer, s *transcript.Store, wakeArg string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(w, f); err != nil {
		return err
	}
	if !follow {
		return nil
	}

	lastGrowth := time.Now()
	for {
		select {
		case <-done:
			return nil
		case <-time.After(transcriptTailPollInterval):
		}

		n, err := io.Copy(w, f)
		if err != nil {
			return err
		}
		if n > 0 {
			lastGrowth = time.Now()
			continue
		}
		if time.Since(lastGrowth) < transcriptTailIdleTimeout {
			continue
		}
		// Idle long enough — check whether this wake is still current.
		if wakeArg == "current" {
			if target, lerr := os.Readlink(s.CurrentPath()); lerr != nil || filepath.Join(s.Dir, target) != path {
				return nil
			}
			// still current — keep waiting (writer may just be slow)
			continue
		}
		// explicit wake id, idle long enough → final.
		return nil
	}
}
