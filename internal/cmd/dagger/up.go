package daggercmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

const upRenamedHelp = `"dagger up" is no longer used. Use "dagger start" instead.

Run "dagger start --help" for usage.
`

// Keep the old command name as guidance only. It must fail for every
// invocation, including ones with the old flags, and must never fall through
// to a local script named "up". Flag parsing is disabled so that any flag,
// including -h and --help, reaches RunE.
var upCmd = &cobra.Command{
	Use:                "up",
	Short:              `Renamed to "dagger start"`,
	Hidden:             true,
	DisableFlagParsing: true,
	Args:               cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("%q has been renamed to %q. Run this instead:\n\n  %s",
			cmd.CommandPath(), cmd.Root().Name()+" start", startInvocation(cmd.Root().Name(), os.Args[1:], args))
	},
}

func init() {
	upCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		fmt.Fprint(cmd.OutOrStdout(), upRenamedHelp)
	})
}

// startInvocation rewrites a "dagger up" command line as "dagger start". The
// root command consumes global flags given before "up", so they are recovered
// from the full command line when it ends with "up" followed by args.
func startInvocation(root string, cmdline, args []string) string {
	words := []string{root}
	if i := len(cmdline) - len(args) - 1; i >= 0 && cmdline[i] == "up" {
		for _, word := range cmdline[:i] {
			words = append(words, shellQuote(word))
		}
	}
	words = append(words, "start")
	for _, arg := range args {
		words = append(words, shellQuote(arg))
	}
	return strings.Join(words, " ")
}
