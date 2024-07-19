package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/dagger/dagger/engine/client"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
)

var funcCmds = []*FuncCommand{
	callModCmd,
	callCoreCmd,
}

var callCoreCmd = &FuncCommand{
	Name:              "core [options]",
	Short:             "Call a core function",
	DisableModuleLoad: true,
}

var callModCmd = &FuncCommand{
	Name:  "call [options]",
	Short: "Call one or more functions, interconnected into a pipeline",
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
			mod, err := initializeModule(ctx, engineClient.Dagger(), true)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
			var o functionProvider = mod.MainObject.AsFunctionProvider()
			fmt.Fprintf(tw, "%s\t%s\n",
				termenv.String("Name").Bold(),
				termenv.String("Description").Bold(),
			)
			// Walk the hypothetical function pipeline specified by the args
			for _, field := range cmd.Flags().Args() {
				// Lookup the next function in the specified pipeline
				nextFunc, err := GetFunction(o, field)
				if err != nil {
					return err
				}
				nextType := nextFunc.ReturnType
				if nextType.AsList != nil {
					nextType = nextType.AsList.ElementTypeDef
				}
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

			// List functions on the final object
			fns := o.GetFunctions()
			sort.Slice(fns, func(i, j int) bool {
				return fns[i].Name < fns[j].Name
			})
			skipped := make([]string, 0)
			for _, fn := range fns {
				if fn.IsUnsupported() {
					skipped = append(skipped, cliName(fn.Name))
					continue
				}
				desc := strings.SplitN(fn.Description, "\n", 2)[0]
				if desc == "" {
					desc = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\n",
					cliName(fn.Name),
					desc,
				)
			}
			if len(skipped) > 0 {
				msg := fmt.Sprintf("Skipped %d function(s) with unsupported types: %s", len(skipped), strings.Join(skipped, ", "))
				fmt.Fprintf(tw, "\n%s\n",
					termenv.String(msg).Faint().String(),
				)
			}
			return tw.Flush()
		})
	},
}
