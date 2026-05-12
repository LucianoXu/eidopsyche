package agentloop

import (
	"io"
	"os"
)

// stderrW is the destination for agent-loop's lifecycle log lines.
// Indirected so tests can capture; production points at os.Stderr.
var stderrW io.Writer = os.Stderr

func stderr() io.Writer { return stderrW }
