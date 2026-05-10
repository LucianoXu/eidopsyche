package forge

import (
	"os"

	"github.com/spf13/cobra"
)

// InContainer returns true when this process is running inside a mind-form
// container. Set by the container image entrypoint.
func InContainer() bool { return os.Getenv("EIDOS_IN_CONTAINER") == "1" }

// registerHost attaches host-side subcommands.
func registerHost(root *cobra.Command) {
	root.AddCommand(
		newCreateCmd(),
		newStartCmd(),
		newStopCmd(),
		newStatusCmd(),
		newListCmd(),
		newLogsCmd(),
		newExecCmd(),
		newWakeHostCmd(),
		newLoginCmd(),
		newOntologyCmd(),
		newPurgeCmd(),
		newConfigCmd(),
		newPlanHostCmd(),
	)
}

// registerInContainer attaches in-container reflection subcommands.
func registerInContainer(root *cobra.Command) {
	root.AddCommand(
		newWhoamiCmd(),
		newInboxCmd(),
		newSendCmd(),
		newMemoryCmd(),
		newOntologyStatusCmd(),
		newWakeInContainerCmd(),
		newInitVolumeCmd(),
		newPlanInContainerCmd(),
		newDreamCmd(),
		newStatusDetailCmd(),
		newRuntimeStateCmd(),
	)
}
