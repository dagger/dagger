package daggercmd

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

var callCoreCmd = &FuncCommand{
	Name:              "core [options]",
	Short:             "Call a core function",
	Hidden:            true,
	Deprecated:        "use 'dagger -m core api call' instead",
	DisableModuleLoad: true,
	Annotations: map[string]string{
		"experimental":       "true",
		printTraceLinkKey:    "true",
		showFinalProgressKey: "true",
	},
}

var callModCmd = &FuncCommand{
	Name:   "call [options] [function]...",
	Short:  "Call one or more functions, interconnected into a pipeline",
	Hidden: true,
	Annotations: map[string]string{
		printTraceLinkKey:    "true",
		showFinalProgressKey: "true",
	},
}

var apiCallCmd = &FuncCommand{
	Name:  "call [options] [function]...",
	Short: "Call one or more functions, interconnected into a pipeline",
	Annotations: map[string]string{
		printTraceLinkKey:    "true",
		showFinalProgressKey: "true",
	},
}

var apiFunctionsCmd = newFunctionListCmd("functions [options] [function]...", false)

// functionsAliasCmd is a hidden root-level alias for `dagger api functions`,
// mirroring how callModCmd aliases `dagger api call`.
var functionsAliasCmd = newFunctionListCmd("functions [options] [function]...", true)

func newFunctionListCmd(use string, hidden bool) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: `List available functions`,
		Long: strings.ReplaceAll(`List available functions in a module.

This is similar to ´dagger api call --help´, but only focused on showing the
available functions.

Examples:
  dagger api functions                           # List top-level functions in current workspace
  dagger api functions container                 # List functions on container
  dagger -m core api functions                   # List core functions
  dagger -W github.com/acme/ws api functions     # List top-level functions in explicit workspace
  dagger -W github.com/acme/ws api functions container from
`,
			"´",
			"`",
		),
		Hidden: hidden,
		RunE:   runFunctionList,
	}
}

func runFunctionList(cmd *cobra.Command, args []string) error {
	params := initModuleParams(args)
	return withEngine(cmd.Context(), params, func(ctx context.Context, engineClient *client.Client) (rerr error) {
		coreMode := moduleNoURL || isCoreModuleSelected()

		var mod *moduleDef
		var err error
		if coreMode {
			mod, err = initializeCore(ctx, engineClient.Dagger())
		} else {
			// -m modules are loaded at engine connect time as extra modules.
			mod, err = initializeWorkspace(ctx, engineClient.Dagger(), loadTypeDefsOpts{HideCore: true, Include: workspaceModuleScope(args)})
		}
		if err != nil {
			return err
		}
		o := mod.MainObject.AsFunctionProvider()
		// Walk the hypothetical function pipeline specified by the args
		for i, field := range args {
			// Lookup the next function in the specified pipeline
			nextFunc, err := GetSupportedFunction(mod, o, field)
			if err != nil && i == 0 {
				if sibling := findSiblingEntrypoint(mod, field); sibling != nil {
					nextFunc = sibling
					err = nil
				}
			}
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

		// Only filter core functions when listing the Query root in
		// workspace mode (multiple modules as sub-commands). When a
		// main module is set (single module or -m), or when navigating
		// into a module type, show all functions.
		filterCore := !coreMode &&
			len(args) == 0 &&
			mod.MainObject.AsObject != nil &&
			mod.MainObject.AsObject.Name == "Query"
		hideLoadFromID := len(args) == 0 &&
			mod.MainObject.AsObject != nil &&
			mod.MainObject.AsObject.Name == "Query"

		var siblingFns []*modFunction
		if len(args) == 0 &&
			mod.MainObject.AsObject != nil &&
			mod.MainObject.AsObject.Name != "Query" {
			siblingFns = mod.siblingModuleEntrypoints()
		}
		return functionListRun(o, cmd.OutOrStdout(), cmd.ErrOrStderr(), filterCore, hideLoadFromID, siblingFns)
	})
}

func findSiblingEntrypoint(mod *moduleDef, name string) *modFunction {
	for _, fn := range mod.siblingModuleEntrypoints() {
		if fn.Name == name || fn.CmdName() == name {
			mod.LoadFunctionTypeDefs(fn)
			return fn
		}
	}
	return nil
}

func functionListRun(o functionProvider, writer io.Writer, errWriter io.Writer, filterCore, hideLoadFromID bool, siblingFns []*modFunction) error {
	fns, skipped, err := GetSupportedFunctions(o)
	if err != nil {
		return err
	}

	// At the Query root, filter out core API constructors - only show module
	// constructors. Also hide loadXFromID plumbing at the Query root. When
	// navigating into a module type, show all functions.
	if filterCore || hideLoadFromID {
		filtered := make([]*modFunction, 0, len(fns))
		for _, fn := range fns {
			if filterCore && fn.SourceModuleName == "" {
				continue
			}
			if hideLoadFromID && isLoadFromIDFunction(fn) {
				continue
			}
			filtered = append(filtered, fn)
		}
		fns = filtered
		skipped = nil // don't show core "skipped" noise either
	}

	fns = append(fns, siblingFns...)
	if len(fns) == 0 {
		fmt.Fprintln(errWriter, "No functions found.")
		return nil
	}

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
		desc := fn.Short()
		// When listing module constructors at the Query root, the constructor
		// function itself usually has no description. Fall back to the return
		// type's object description (the module type's doc comment).
		if desc == "-" && fn.ReturnType != nil && fn.ReturnType.AsObject != nil {
			if objDesc := shortDescription(fn.ReturnType.AsObject.Description); objDesc != "-" {
				desc = objDesc
			}
		}
		fmt.Fprintf(tw, "%s\t%s\n",
			fn.CmdName(),
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
}

func isLoadFromIDFunction(fn *modFunction) bool {
	return strings.HasPrefix(fn.Name, "load") && strings.HasSuffix(fn.Name, "FromID")
}
