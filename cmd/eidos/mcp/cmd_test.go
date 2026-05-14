package mcp

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestCommand_ReturnsRoot(t *testing.T) {
	c := Command()
	if c == nil {
		t.Fatal("Command() returned nil")
	}
	if c.Use != "mcp" {
		t.Fatalf("Use = %q, want %q", c.Use, "mcp")
	}
	if _, ok := any(c).(*cobra.Command); !ok {
		t.Fatalf("Command() type = %T, want *cobra.Command", c)
	}
}
