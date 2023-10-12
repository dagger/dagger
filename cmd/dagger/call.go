package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	AsObject     *objectType
	Kind         dagger.TypeDefKind
	orig         dagger.FunctionArg
}

type objectType struct {
	Name string
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
			if err = fn.loadArguments(ctx); err != nil {
				return err
			}

			// Set flags from the command line.
			for _, arg := range fn.Args {
				arg := arg

				err = arg.SetFlag(cmd.Flags())
				if err != nil {
					return fmt.Errorf("failed to set flag %q: %w", arg.FlagName, err)
				}

				// TODO: This won't work on its own because cobra checks required
				// flags before the RunE function is called. We need to manually
				// check the flags after parsing them.
				if !arg.Optional {
					cmd.MarkFlagRequired(arg.FlagName)
				}
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

				// TODO: Check required flags.

				val, err := arg.GetValue(dag, cmd.Flags())
				if err != nil {
					return fmt.Errorf("failed to get value for flag %q: %w", arg.FlagName, err)
				}

				jval, err := json.Marshal(val)
				if err != nil {
					return fmt.Errorf("failed to marshal flag value %q: %w", arg.FlagName, err)
				}

				inputs = append(inputs, dagger.FunctionCallInput{
					Name:  arg.Name,
					Value: dagger.JSON(string(jval)),
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
			// if a File or Directory, export to `outputPath`. If a Container, call
			// stdout?
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
		for _, fn := range objFuncs {
			fn := fn

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

func (fn *callFunction) loadArguments(ctx context.Context) error {
	if fn.Args != nil {
		return fmt.Errorf("function arguments already loaded")
	}

	funcArgs, err := fn.orig.Args(ctx)
	if err != nil {
		return fmt.Errorf("failed to get function args: %w", err)
	}

	for _, funcArg := range funcArgs {
		funcArg := funcArg

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

		kind, err := funcArg.TypeDef().Kind(ctx)
		if err != nil {
			return fmt.Errorf("failed to get arg type: %w", err)
		}

		optional, err := funcArg.TypeDef().Optional(ctx)
		if err != nil {
			return fmt.Errorf("failed to check if arg is optional: %w", err)
		}

		arg := callFunctionArg{
			orig:         funcArg,
			Name:         name,
			FlagName:     strcase.ToKebab(name),
			Description:  strings.TrimSpace(description),
			Kind:         kind,
			Optional:     optional,
			DefaultValue: defaultVal,
		}

		if kind == dagger.Objectkind {
			name, err := funcArg.TypeDef().AsObject().Name(ctx)
			if err != nil {
				return fmt.Errorf("failed to get object name: %w", err)
			}
			arg.AsObject = &objectType{
				Name: name,
			}
		}

		fn.Args = append(fn.Args, arg)
	}

	return nil
}

func (r *callFunctionArg) SetFlag(flags *pflag.FlagSet) error {
	switch r.Kind {
	case dagger.Stringkind:
		val, _ := r.DefaultValue.(string)
		flags.String(r.FlagName, val, r.Description)
	case dagger.Integerkind:
		val, _ := r.DefaultValue.(int)
		flags.Int(r.FlagName, val, r.Description)
	case dagger.Booleankind:
		val, _ := r.DefaultValue.(bool)
		flags.Bool(r.FlagName, val, r.Description)
	case dagger.Objectkind:
		switch r.AsObject.Name {
		case "Directory", "File", "Secret":
			flags.String(r.FlagName, "", r.Description)
		}
	default:
		return fmt.Errorf("unsupported type %q", r.Kind)
	}
	return nil
}

func (r *callFunctionArg) GetValue(dag *dagger.Client, flags *pflag.FlagSet) (any, error) {
	switch r.Kind {
	case dagger.Stringkind:
		return flags.GetString(r.FlagName)
	case dagger.Integerkind:
		return flags.GetInt(r.FlagName)
	case dagger.Booleankind:
		return flags.GetBool(r.FlagName)
	case dagger.Objectkind:
		val, err := flags.GetString(r.FlagName)
		if err != nil {
			return nil, err
		}
		switch r.AsObject.Name {
		case "Directory":
			return dag.Host().Directory(val), nil
		case "File":
			return dag.Host().File(val), nil
		case "Secret":
			hash := sha256.Sum256([]byte(val))
			name := hex.EncodeToString(hash[:])
			return dag.SetSecret(name, val), nil
		default:
			return nil, fmt.Errorf("unsupported object type %q", r.AsObject.Name)
		}
	default:
		return nil, fmt.Errorf("unsupported type %q", r.Kind)
	}
}
