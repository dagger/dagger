package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/iancoleman/strcase"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/vito/progrock"
	"golang.org/x/term"
)

// TODO: This is highly WIP!
// - [ ] Add support for passing arguments to functions.
// - [ ] Add support for getting objects/functions by name in the API instead of having to loop.
// - [ ] Create cobra subcommands for functions.
// - [ ] Create multiple "verb" commands for calling functions with different intents.
// - [ ] Add `dagger shell` as a high priority for its debugging usefulness.
// - [ ] Add a --help flag to list available functions.
// - [ ] Maybe a list/functions command to list all functions and compatible verbs that can be used to call them.
// - [ ] Maybe compose a query instead of using `Function.call`?
// - [ ] Use `dagger run` instead of `dagger call` for generic execution.
//       Detect if running in the context of a module for backwards compatibility.
// - [ ] Improve progrock usage.
// - [ ] Support non-progrock (e.g., --progress plain) output.
// - [ ] Fix Go SDK's listing bug.
// - [ ] Add support for chaining function calls.

// outputPath is the host directory to export assets to.
// var outputPath string

var callCmd = &cobra.Command{
	Use:    "call [function]",
	Long:   `Call a module's function.`,
	RunE:   loadModCmdWrapper(RunFunction, ""),
	Hidden: true,
}

func init() {
	rootCmd.AddCommand(callCmd)

	// TODO: Use `dagger download` verb.
	// callCmd.PersistentFlags().StringVarP(&outputPath, "output", "o", "", "If the function returns a file or directory, it will be written to this path. If --output is not specified, the file or directory will be written to the module's root directory when using a module loaded from a local dir.")
	callCmd.PersistentFlags().BoolVar(&focus, "focus", true, "Only show output for focused commands.")

	callCmd.AddCommand(
		&cobra.Command{
			Use:          "list",
			Long:         `List your module's functions.`,
			SilenceUsage: true,
			RunE:         loadModCmdWrapper(ListFunctions, ""),
			Hidden:       true,
		},
	)
}

func ListFunctions(ctx context.Context, engineClient *client.Client, mod *dagger.Module, cmd *cobra.Command, cmdArgs []string) (err error) {
	if mod == nil {
		return fmt.Errorf("no module specified and no default module found in current directory")
	}

	dag := engineClient.Dagger()
	rec := progrock.FromContext(ctx)
	vtx := rec.Vertex("cmd-list-functions", "list functions", progrock.Focused())
	defer func() { vtx.Done(err) }()

	modName, err := mod.Name(ctx)
	if err != nil {
		return fmt.Errorf("failed to get module name: %w", err)
	}

	modObjs, err := mod.Objects(ctx)
	if err != nil {
		return fmt.Errorf("failed to get module objects: %w", err)
	}

	var modFuncs []dagger.Function
	for _, def := range modObjs {
		// TODO: workaround bug in codegen
		objID, err := def.ID(ctx)
		if err != nil {
			return fmt.Errorf("failed to get object id: %w", err)
		}
		def = *dag.TypeDef(
			dagger.TypeDefOpts{
				ID: objID,
			},
		)
		objDef := def.AsObject()
		objName, err := objDef.Name(ctx)
		if err != nil {
			return fmt.Errorf("failed to get object name: %w", err)
		}

		// TODO: Only list top level functions for now (i.e., type name matches module name).
		if strcase.ToLowerCamel(objName) != strcase.ToLowerCamel(modName) {
			continue
		}

		funcs, err := objDef.Functions(ctx)
		if err != nil {
			return fmt.Errorf("failed to get object functions: %w", err)
		}
		if funcs == nil {
			continue
		}
		modFuncs = append(modFuncs, funcs...)
	}

	tw := tabwriter.NewWriter(vtx.Stdout(), 0, 0, 2, ' ', 0)

	if stdoutIsTTY {
		fmt.Fprintf(tw, "%s\t%s\n", termenv.String("function name").Bold(), termenv.String("description").Bold())
	}

	printFunc := func(function *dagger.Function) error {
		name, err := function.Name(ctx)
		if err != nil {
			return fmt.Errorf("failed to get function name: %w", err)
		}
		name = strcase.ToKebab(name)

		descr, err := function.Description(ctx)
		if err != nil {
			return fmt.Errorf("failed to get function description: %w", err)
		}
		fmt.Fprintf(tw, "%s\t%s\n", name, strings.TrimSpace(descr))
		return nil
	}

	for _, function := range modFuncs {
		function := function
		// TODO: workaround bug in codegen
		funcID, err := function.ID(ctx)
		if err != nil {
			return fmt.Errorf("failed to get function id: %w", err)
		}
		function = *engineClient.Dagger().Function(funcID)
		err = printFunc(&function)
		if err != nil {
			return err
		}
	}

	return tw.Flush()
}

func RunFunction(ctx context.Context, engineClient *client.Client, mod *dagger.Module, cmd *cobra.Command, cmdArgs []string) (err error) {
	if mod == nil {
		return fmt.Errorf("no module specified and no default module found in current directory")
	}

	// TODO: Add supporting fields in the API to get module object and function
	// by name instead of looping and dealing with codegen bug.

	// TODO: Parse args and pass them to the function. Make a Directory/file
	// from a host path, for example.
	target := cmdArgs[0]
	gqlFuncName := strcase.ToLowerCamel(target)

	dag := engineClient.Dagger()
	rec := progrock.FromContext(ctx)
	vtx := rec.Vertex("cmd-call-function", fmt.Sprintf("call function: %s", target), progrock.Focused())
	defer func() { vtx.Done(err) }()

	modName, err := mod.Name(ctx)
	if err != nil {
		return fmt.Errorf("failed to get module name: %w", err)
	}

	gqlModName := strcase.ToLowerCamel(modName)

	modObjs, err := mod.Objects(ctx)
	if err != nil {
		return fmt.Errorf("failed to get module objects: %w", err)
	}

	var currFunc *dagger.Function

	for _, def := range modObjs {
		// TODO: workaround bug in codegen
		objID, err := def.ID(ctx)
		if err != nil {
			return fmt.Errorf("failed to get object id: %w", err)
		}
		def = *engineClient.Dagger().TypeDef(
			dagger.TypeDefOpts{
				ID: objID,
			},
		)
		objDef := def.AsObject()
		objName, err := objDef.Name(ctx)
		if err != nil {
			return fmt.Errorf("failed to get object name: %w", err)
		}

		// Only list top level functions (i.e., type name matches module name).
		if strcase.ToLowerCamel(objName) != gqlModName {
			continue
		}

		funcs, err := objDef.Functions(ctx)
		if err != nil {
			return fmt.Errorf("failed to get object functions: %w", err)
		}
		if funcs == nil {
			continue
		}
		for _, function := range funcs {
			function := function
			// TODO: workaround bug in codegen
			funcID, err := function.ID(ctx)
			if err != nil {
				return fmt.Errorf("failed to get function id: %w", err)
			}
			function = *dag.Function(funcID)
			if err != nil {
				return err
			}
			funcName, err := function.Name(ctx)
			if err != nil {
				return fmt.Errorf("failed to get function name: %w", err)
			}
			if strcase.ToLowerCamel(funcName) == gqlFuncName {
				currFunc = &function
				break
			}
		}
	}

	if currFunc == nil {
		return fmt.Errorf("function %q not found", target)
	}

	// TODO: Fix Function.call: Unexpected end of JSON input.
	// result, err := currFunc.Call(ctx)

	// TODO: Easier to use querybuilder but it's internal right now.
	var res struct {
		CurrMod struct {
			CurrFunc string
		}
	}

	query := fmt.Sprintf(
		`
        query {
            currMod: %s {
                currFunc: %s
            }
        }
        `,
		gqlModName,
		gqlFuncName,
	)

	err = dag.Do(ctx, &dagger.Request{
		Query:     query,
		Variables: map[string]interface{}{},
	}, &dagger.Response{
		Data: &res,
	})

	if err != nil {
		return fmt.Errorf("failed to call function: %w", err)
	}

	result := res.CurrMod.CurrFunc

	var out io.Writer
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		out = os.Stdout
	} else {
		out = vtx.Stdout()
	}

	// TODO: Figure out what makes sense based on return type. For example,
	// if a File or Directory, export to `outputPath`.
	fmt.Fprintln(out, result)

	return nil
}
