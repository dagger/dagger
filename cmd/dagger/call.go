package main

import (
	"context"
	"encoding/json"
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
	"github.com/spf13/pflag"
	"github.com/vito/progrock"
	"golang.org/x/term"
)

var callFlags = pflag.NewFlagSet("call", pflag.ContinueOnError)

var funcsCmd = &cobra.Command{
	Use:    "functions",
	Long:   `List all functions in a module.`,
	Hidden: true,
	RunE:   loadModCmdWrapper(ListFunctions, ""),
}

var callCmd = &cobra.Command{
	Use:                "call",
	Short:              `Call a module's function.`,
	DisableFlagParsing: true,
	Hidden:             true,
	RunE: func(cmd *cobra.Command, args []string) error {
		callFlags.SetInterspersed(false)
		callFlags.AddFlagSet(cmd.Flags())
		callFlags.AddFlagSet(cmd.PersistentFlags())

		err := callFlags.Parse(args)
		if err != nil {
			return fmt.Errorf("failed to parse top-level flags: %w", err)
		}

		return loadModCmdWrapper(RunFunction, "")(cmd, args)
	},
}

type callFunction struct {
	Name        string
	CmdName     string
	Description string
	Args        []callFunctionArg
	orig        dagger.Function
}

type callFunctionArg struct {
	Name         string
	FlagName     string
	Description  string
	Optional     bool
	DefaultValue any
	orig         dagger.FunctionArg
}

func ListFunctions(ctx context.Context, engineClient *client.Client, mod *dagger.Module, cmd *cobra.Command, cmdArgs []string) (err error) {
	if mod == nil {
		return fmt.Errorf("no module specified and no default module found in current directory")
	}

	dag := engineClient.Dagger()
	rec := progrock.FromContext(ctx)
	vtx := rec.Vertex("cmd-list-functions", "list functions", progrock.Focused())
	defer func() { vtx.Done(err) }()

	loadFuncs := vtx.Task("loading functions")
	funcs, err := loadFunctions(ctx, dag, mod)
	loadFuncs.Done(err)
	if err != nil {
		return fmt.Errorf("failed to load functions: %w", err)
	}

	tw := tabwriter.NewWriter(vtx.Stdout(), 0, 0, 2, ' ', 0)

	if stdoutIsTTY {
		fmt.Fprintf(tw, "%s\t%s\n", termenv.String("function name").Bold(), termenv.String("description").Bold())
	}

	for _, function := range funcs {
		// TODO: Add a third column with available verbs.
		fmt.Fprintf(tw, "%s\t%s\n", function.Name, function.Description)
	}

	return tw.Flush()
}

func RunFunction(ctx context.Context, engineClient *client.Client, mod *dagger.Module, cmd *cobra.Command, cmdArgs []string) (err error) {
	if mod == nil {
		return fmt.Errorf("no module specified and no default module found in current directory")
	}

	dag := engineClient.Dagger()
	rec := progrock.FromContext(ctx)
	vtx := rec.Vertex("cmd-func-loader", strings.Join(append([]string{cmd.Name()}, callFlags.Args()...), " "), progrock.Focused())
	defer func() { vtx.Done(err) }()

	var stdout io.Writer
	var stderr io.Writer
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		stdout = os.Stdout
		stderr = os.Stderr
	} else {
		stdout = vtx.Stdout()
		stderr = vtx.Stderr()
	}

	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	funcs, err := loadFunctions(ctx, dag, mod)
	if err != nil {
		return fmt.Errorf("failed to load functions: %w", err)
	}

	for _, fn := range funcs {
		subCmd, err := addCmd(ctx, dag, mod, fn, vtx)
		if err != nil {
			return fmt.Errorf("failed to add cmd: %w", err)
		}
		cmd.AddCommand(subCmd)
	}

	subCmd, subArgs, err := cmd.Find(callFlags.Args())
	if err != nil {
		return fmt.Errorf("failed to find command: %w", err)
	}

	if help, _ := callFlags.GetBool("help"); help {
		return cmd.Help()
	}

	if subCmd.Name() == cmd.Name() {
		if len(subArgs) > 0 {
			return fmt.Errorf("unknown command %q for %q", subArgs[0], cmd.CommandPath())
		}
		return cmd.Help()
	}

	err = subCmd.RunE(subCmd, subArgs)
	if err != nil {
		return fmt.Errorf("failed to execute command: %w", err)
	}

	return nil
}

func addCmd(ctx context.Context, dag *dagger.Client, mod *dagger.Module, fn *callFunction, vtx *progrock.VertexRecorder) (sub *cobra.Command, err error) {
	subCmd := &cobra.Command{
		Use:   strcase.ToKebab(fn.Name),
		Short: fn.Description,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Only load function arguments when it's actually needed.
			if err = loadArguments(ctx, dag, fn); err != nil {
				return err
			}

			for _, arg := range fn.Args {
				arg := arg

				// TODO: handle more types
				/*
				   switch kind {
				   case dagger.Stringkind:
				       subCmd.Flags().String(flagName, "", argDescription)
				   default:
				       // TODO: Handle more types.
				       continue
				   }
				*/

				// TODO: handle default value properly

				defaultVal, ok := arg.DefaultValue.(string)
				if !ok {
					defaultVal = ""
				}

				cmd.Flags().String(arg.FlagName, defaultVal, arg.Description)

				/*
					// TODO: This won't work on its own because cobra checks required
					// flags before the RunE function is called. We need to manually
					// check the flags after parsing them.
					if !arg.Optional && defaultVal != "" {
						cmd.MarkFlagRequired(arg.FlagName)
					}
				*/
			}

			if err = cmd.ParseFlags(args); err != nil {
				return fmt.Errorf("failed to parse subcommand flags: %w", err)
			}

			if help, _ := cmd.Flags().GetBool("help"); help {
				return cmd.Help()
			}

			var inputs []dagger.FunctionCallInput

			for _, arg := range fn.Args {
				arg := arg

				// TODO: Handle various types.
				flagVal, err := cmd.Flags().GetString(arg.FlagName)
				if err != nil {
					return fmt.Errorf("failed to get flag %q: %w", arg.FlagName, err)
				}

				jsonVal, err := json.Marshal(flagVal)
				if err != nil {
					return fmt.Errorf("failed to marshal flag value %q: %w", arg.FlagName, err)
				}

				inputs = append(inputs, dagger.FunctionCallInput{
					Name:  arg.Name,
					Value: dagger.JSON(string(jsonVal)),
				})
			}

			// TODO: Use query builder for chainable calls, but need to make it public.
			res, err := fn.orig.Call(ctx, dagger.FunctionCallOpts{
				Input: inputs,
			})

			if err != nil {
				return fmt.Errorf("failed to call function: %w", err)
			}

			var result interface{}

			if err = json.Unmarshal([]byte(res), &result); err != nil {
				return fmt.Errorf("failed to unmarshal result: %w", err)
			}

			// TODO: Figure out what makes sense based on return type. For example,
			// if a File or Directory, export to `outputPath`.
			cmd.Println(result)

			return nil
		},
	}

	subCmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Show help for '%s'", fn.CmdName))

	return subCmd, nil
}

func loadFunctions(ctx context.Context, dag *dagger.Client, mod *dagger.Module) (callFuncs []*callFunction, err error) {
	modName, err := mod.Name(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module name: %w", err)
	}

	// Normalize module name like a GraphQL field name for comparison later on.
	gqlFieldName := strcase.ToLowerCamel(modName)

	modObjs, err := mod.Objects(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get module objects: %w", err)
	}

	for _, def := range modObjs {
		def := def

		// TODO: workaround bug in codegen
		objID, err := def.ID(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get object id: %w", err)
		}
		def = *dag.TypeDef(
			dagger.TypeDefOpts{
				ID: objID,
			},
		)

		objDef := def.AsObject()
		objName, err := objDef.Name(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get object name: %w", err)
		}

		// Only list top level functions (i.e., type name matches module name).
		if strcase.ToLowerCamel(objName) != gqlFieldName {
			continue
		}

		objFuncs, err := objDef.Functions(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get object functions: %w", err)
		}
		if objFuncs == nil {
			continue
		}
		for _, objFunc := range objFuncs {
			objFunc := objFunc

			// TODO: workaround bug in codegen
			funcID, err := objFunc.ID(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get function id: %w", err)
			}
			fn := *dag.Function(funcID)
			if err != nil {
				return nil, err
			}

			name, err := fn.Name(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get function name: %w", err)
			}

			description, err := fn.Description(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to get function description: %w", err)
			}

			// Some descriptions have a trailing newline, which is not desirable.
			description = strings.TrimSpace(description)

			callFuncs = append(callFuncs, &callFunction{
				orig:        fn,
				Name:        name,
				CmdName:     strcase.ToKebab(name),
				Description: description,
			})
		}
	}

	return callFuncs, nil
}

func loadArguments(ctx context.Context, _ *dagger.Client, fn *callFunction) error {
	if fn.Args != nil {
		return fmt.Errorf("function arguments already loaded")
	}

	funcArgs, err := fn.orig.Args(ctx)
	if err != nil {
		return fmt.Errorf("failed to get function args: %w", err)
	}

	for _, funcArg := range funcArgs {
		funcArg := funcArg

		// TODO: workaround bug in codegen
		// TODO: Need constructor for dagger.FunctionArg
		/*
		   argID, err := funcArg.ID(ctx)
		   if err != nil {
		       return nil, fmt.Errorf("failed to get arg id: %w", err)
		   }
		*/

		name, err := funcArg.Name(ctx)
		if err != nil {
			return fmt.Errorf("failed to get arg name: %w", err)
		}

		description, err := funcArg.Description(ctx)
		if err != nil {
			return fmt.Errorf("failed to get arg description: %w", err)
		}

		defaultJSON, err := funcArg.DefaultValue(ctx)
		if err != nil {
			return fmt.Errorf("failed to get arg default value: %w", err)
		}

		var defaultVal any
		if defaultJSON != "" {
			if err = json.Unmarshal([]byte(defaultJSON), &defaultVal); err != nil {
				return fmt.Errorf("failed to unmarshal arg default value (%v): %w", defaultJSON, err)
			}
		}

		// TODO: Can't get Kind with go codegen bug. Need to make FunctionArg
		// an idable type.

		/*
		   kind, err := funcArg.TypeDef().Kind(ctx)
		   if err != nil {
		       return nil, fmt.Errorf("failed to get arg type: %w", err)
		   }

		   optional, err := funcArg.TypeDef().Optional(ctx)
		   if err != nil {
		       return nil, fmt.Errorf("failed to check if arg is optional: %w", err)
		   }
		*/

		fn.Args = append(fn.Args, callFunctionArg{
			orig:         funcArg,
			Name:         name,
			FlagName:     strcase.ToKebab(name),
			Description:  strings.TrimSpace(description),
			DefaultValue: defaultVal,
		})
	}

	return nil
}
