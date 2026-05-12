//go:build !windows

// internal/agentloop/testmain_test.go
package agentloop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// stubClaudeBin is the absolute path to the compiled testfake/claudestub
// binary. Built once per `go test` run by TestMain.
var stubClaudeBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "eidos-agentloop-stub-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "TestMain: tempdir:", err)
		os.Exit(2)
	}
	stubClaudeBin = filepath.Join(dir, "claudestub")
	cmd := exec.Command("go", "build", "-o", stubClaudeBin,
		"github.com/LucianoXu/eidopsyche/internal/agentloop/testfake/cmd/claudestub")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "TestMain: build stub:", err)
		_ = os.RemoveAll(dir)
		os.Exit(2)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
