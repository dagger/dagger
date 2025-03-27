package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dagger/dagger/engine/client"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
)

const (
	printTraceLinkKey = "printTraceLink"
)

func isPrintTraceLinkEnabled(annotations map[string]string) bool {
	if val, ok := annotations[printTraceLinkKey]; ok && val == "true" {
		return true
	}

	return false
}

var funcCmds = []*FuncCommand{
	callModCmd,
	callCoreCmd,
}

var callCoreCmd = &FuncCommand{
	Name:              "core [options]",
	Short:             "Call a core function",
	DisableModuleLoad: true,
	Annotations: map[string]string{
		"experimental":    "true",
		printTraceLinkKey: "true",
	},
}

var callModCmd = &FuncCommand{
	Name:  "call [options]",
	Short: "Call one or more functions, interconnected into a pipeline",
	Annotations: map[string]string{
		printTraceLinkKey: "true",
	},
}

var funcListCmd = &cobra.Command{
	Use:   "functions [options] [function]...",
	Short: `List available functions`,
	Long: strings.ReplaceAll(`List available functions in a module.

This is similar to ´dagger call --help´, but only focused on showing the
available functions.
`,
		"´",
		"`",
	),
	GroupID: moduleGroup.ID,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) (rerr error) {
			mod, err := initializeDefaultModule(ctx, engineClient.Dagger())
			if err != nil {
				return err
			}
			if mod.MainObject == nil {
				return functionListRun(cmd.OutOrStdout(), nil, nil)
			}
			o := mod.MainObject.AsFunctionProvider()
			// Walk the hypothetical function pipeline specified by the args
			for _, field := range cmd.Flags().Args() {
				// Lookup the next function in the specified pipeline
				nextFunc, err := GetSupportedFunction(mod, o, field)
				if err != nil {
					return err
				}
				nextType := nextFunc.ReturnType
				if nextType.AsFunctionProvider() != nil {
					// sipsma explains why 'nextType.AsObject' is not enough:
					// > when we're returning the hierarchies of TypeDefs from the API,
					// > and an object shows up as an output/input type to a function,
					// > we just return a TypeDef with a name of the object rather than the full object definition.
					// > You can get the full object definition only from the "top-level" returned object on the api call.
					//
					// > The reason is that if we repeated the full object definition every time,
					// > you'd at best be using O(n^2) space in the result,
					// > and at worst cause json serialization errors due to cyclic references
					// > (i.e. with* functions on an object that return the object itself).
					o = mod.GetFunctionProvider(nextType.Name())
					continue
				}
				return fmt.Errorf("function %q returns type %q with no further functions available", field, nextType.Kind)
			}

			fns, skipped := GetSupportedFunctions(o)
			return functionListRun(cmd.OutOrStdout(), fns, skipped)
		})
	},
}

func functionListRun(writer io.Writer, fns []*modFunction, skipped []string) error {
	tw := tabwriter.NewWriter(writer, 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
	fmt.Fprintf(tw, "%s\t%s\n",
		termenv.String("Name").Bold(),
		termenv.String("Description").Bold(),
	)
	// List functions on the final object
	sort.Slice(fns, func(i, j int) bool {
		return fns[i].Name < fns[j].Name
	})
	for _, fn := range fns {
		fmt.Fprintf(tw, "%s\t%s\n",
			fn.CmdName(),
			fn.Short(),
		)
	}
	if len(skipped) > 0 {
		msg := fmt.Sprintf("Skipped %d function(s) with unsupported types: %s", len(skipped), strings.Join(skipped, ", "))
		fmt.Fprintf(tw, "\n%s\n",
			termenv.String(msg).Faint().String(),
		)
	}
	return tw.Flush()
}
