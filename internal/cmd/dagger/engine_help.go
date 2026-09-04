package daggercmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dagger/dagger/engine/client/drivers"
)

// engineHelpCmd is a help topic, not a command: it has no Run, so Cobra lists
// it under "Additional help topics" instead of the command list. It holds the
// full engine selector catalog that the `--engine` usage is too small to carry.
var engineHelpCmd = &cobra.Command{
	Use:   "engine",
	Short: "How to select the engine that runs your workflows",
	Long: fmt.Sprintf(`Dagger runs your workflows on an engine. Use --engine to select one.

VALUES

%s

PRIORITY

  The CLI uses the first of these that is set:

    1. --engine
    2. %s (deprecated; any value selects Dagger Cloud)
    3. %s (deprecated)
    4. The engine that this CLI version ships with

  The two environment variables are soft-deprecated, like the --cloud flag.
  They still work as a fallback, but --engine replaces them:

    --engine=cloud  instead of %s
    --engine=URI    instead of %s

EXAMPLES

  Run on Dagger Cloud:
    dagger call --engine=cloud build

  Run on an engine container that is already running:
    dagger call --engine=container://dagger-engine build

  Run on a remote host over SSH:
    dagger call --engine=ssh://user@host build
`, drivers.SchemeHelp(), cloudEngineEnv, RunnerHostEnv, cloudEngineEnv, RunnerHostEnv),
}

func init() {
	rootCmd.AddCommand(engineHelpCmd)
}
