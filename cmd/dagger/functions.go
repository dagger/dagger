package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/engine/client"
	"github.com/opencontainers/go-digest"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vito/progrock"
)

const (
	Directory string = "Directory"
	Container string = "Container"
	File      string = "File"
	Secret    string = "Secret"
)

var funcGroup = &cobra.Group{
	ID:    "functions",
	Title: "Functions",
}

var funcCmds = FuncCommands{
	callCmd,
}

type FuncCommands []*FuncCommand

// TODO: Replace with something more native
var errUsage = fmt.Errorf("run with --help for usage")

// TODO: Implement as another verb
/*
var listCmd = &cobra.Command{
	Use:    "functions",
	Long:   `List all functions in a module.`,
	Hidden: true,
	   RunE:   loadFuncsCmdWrapper(func(_ context.Context, _ *dagger.Client, c *callContext, cmd *cobra.Command, _ []string) (err error) {
	       tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)

	       if stdoutIsTTY {
	           fmt.Fprintf(tw, "%s\t%s\t%s\n",
	               termenv.String("object name").Bold(),
	               termenv.String("function name").Bold(),
	               termenv.String("description").Bold(),
	           )
	       }

	       for _, o := range c.m.Objects {
	           for _, fn := range o.Functions {
	               // TODO: Add another column with available verbs.
	               fmt.Fprintf(tw, "%s\t%s\t%s\n", o.Name, fn.Name, fn.Description)
	           }
	       }

	       return tw.Flush()
	   })
}
*/

func (fcs FuncCommands) AddFlagSet(flags *pflag.FlagSet) {
	for _, cmd := range fcs.All() {
		cmd.PersistentFlags().AddFlagSet(flags)
	}
}

func (fcs FuncCommands) AddParent(rootCmd *cobra.Command) {
	rootCmd.AddGroup(funcGroup)
	rootCmd.AddCommand(fcs.All()...)
}

func (fcs FuncCommands) All() []*cobra.Command {
	cmds := make([]*cobra.Command, len(fcs))
	for i, fc := range fcs {
		cmds[i] = fc.Command()
	}
	return cmds
}

// callContext holds the execution context for a function call.
type callContext struct {
	m *moduleDef
	c graphql.Client
	q *querybuilder.Selection
}

func (c *callContext) Select(name string) {
	c.q = c.q.Select(gqlFieldName(name))
}

func (c *callContext) Arg(name string, value any) {
	c.q = c.q.Arg(gqlArgName(name), value)
}

func setCmdOutput(cmd *cobra.Command, vtx *progrock.VertexRecorder) {
	if silent {
		return
	}

	var stdout io.Writer
	var stderr io.Writer

	if stdoutIsTTY {
		stdout = vtx.Stdout()
	} else {
		stdout = os.Stdout
	}

	if stderrIsTTY {
		stderr = vtx.Stderr()
	} else {
		stderr = os.Stderr
	}

	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
}

type FuncCommand struct {
	Name    string
	Short   string
	Example string
	cmd     *cobra.Command
}

func (fc *FuncCommand) Command() *cobra.Command {
	if fc.cmd == nil {
		fc.cmd = &cobra.Command{
			Use:                   fmt.Sprintf("%s [flags] [command [flags]]...", fc.Name),
			Short:                 fc.Short,
			Example:               fc.Example,
			DisableFlagsInUseLine: true,
			DisableFlagParsing:    true,
			Hidden:                true, // for now, remove once we're ready for primetime
			GroupID:               funcGroup.ID,
			RunE:                  fc.execute,
		}

		fc.cmd.Flags().SetInterspersed(false) // stop parsing at first possible dynamic subcommand
	}
	return fc.cmd
}

func (fc *FuncCommand) execute(cmd *cobra.Command, args []string) error {
	// Need to parse flags that affect the TUI and module loading before all else.
	err := cmd.Flags().Parse(args)
	if err != nil {
		return fmt.Errorf("parse command flags: %w", err)
	}

	err = withEngineAndTUI(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
		dag := engineClient.Dagger()
		rec := progrock.FromContext(ctx)

		// TODO: Make this vertex unfocused to hide the progress after its done,
		// but need to handle --help so it's not hidden.

		// Can't print full args because we don't know which ones are secrets
		// yet and don't want to risk exposing them.
		vtx := rec.Vertex(
			digest.Digest(fmt.Sprintf("cmd-func-%s-loader", cmd.Name())),
			fmt.Sprintf("build %q", cmd.CommandPath()),
			progrock.Focused(),
		)
		defer func() { vtx.Done(err) }()

		setCmdOutput(cmd, vtx)

		load := vtx.Task("loading module")
		mod, err := loadMod(ctx, dag)
		load.Done(err)
		if err != nil {
			return err
		}
		if mod == nil {
			return fmt.Errorf("no module specified and no default module found in current directory")
		}

		load = vtx.Task("loading objects")
		modDef, err := loadModObjects(ctx, dag, mod)
		load.Done(err)
		if err != nil {
			return err
		}

		obj := modDef.GetMainObject()
		if obj == nil {
			return fmt.Errorf("main object not found")
		}

		// TODO: Build all the commands first, based on the arguments.
		// Don't build from the subcommands. Then re-enable cobra parsing
		// and handoff to Execute.
		/*
		   build := vtx.Task("building commands from arguments")
		   build.Done(fc.Parse(ctx, dag, obj, cmd, args))
		*/

		c := &callContext{
			m: modDef,
			q: querybuilder.Query(),
			c: dag.GraphQLClient(),
		}

		// Select constructor
		c.Select(obj.Name)

		// Add main object's functions as subcommands
		fc.addSubCommands(ctx, dag, obj, c, cmd)

		// Re-parse
		err = cmd.Flags().Parse(args)
		if err != nil {
			return fmt.Errorf("parse command flags: %w", err)
		}

		if help, _ := cmd.Flags().GetBool("help"); help {
			return cmd.Help()
		}

		subCmd, subArgs, err := cmd.Traverse(cmd.Flags().Args())
		if err != nil {
			return fmt.Errorf("find command: %w", err)
		}

		if subCmd.Name() == cmd.Name() {
			if len(subArgs) > 0 {
				return fmt.Errorf("unknown command %q for %q", subArgs[0], cmd.CommandPath())
			}
			return cmd.Help()
		}

		return fc.run(subCmd, subArgs)
	})
	if err != nil {
		return err
	}

	cmd.DebugFlags()

	return nil
}

func (*FuncCommand) run(cmd *cobra.Command, args []string) error {
	err := cmd.RunE(cmd, args)
	if err != nil {
		return fmt.Errorf("execute command %q: %w", cmd.Name(), err)
	}
	return nil
}

func (fc *FuncCommand) addSubCommands(ctx context.Context, dag *dagger.Client, obj *modObject, c *callContext, cmd *cobra.Command) {
	if obj != nil {
		for _, fn := range obj.Functions {
			subCmd := fc.makeSubCmd(ctx, dag, fn, c)
			cmd.AddCommand(subCmd)
		}
	}
}

func (fc *FuncCommand) makeSubCmd(ctx context.Context, dag *dagger.Client, fn *modFunction, c *callContext) *cobra.Command {
	newCmd := &cobra.Command{
		Use:   cliName(fn.Name),
		Short: fn.Description,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			rec := progrock.FromContext(ctx)

			c.m.LoadObject(fn.ReturnType)

			for _, arg := range fn.Args {
				c.m.LoadObject(arg.TypeDef)
			}

			fc.setFlags(cmd, fn)

			if err := cmd.ParseFlags(args); err != nil {
				return fmt.Errorf("parse sub-command flags: %w", err)
			}

			fc.addSubCommands(ctx, dag, fn.ReturnType.AsObject, c, cmd)

			// Need to make the query selection before chaining off.
			kind, err := fc.selectFunc(fn, c, cmd.Flags(), dag)
			if err != nil {
				return fmt.Errorf("query selection: %w", err)
			}

			leaf := isLeaf(kind)
			help, _ := cmd.Flags().GetBool("help")
			cmdArgs := cmd.Flags().Args()

			// Easy check, but could have invalid arguments.
			if !leaf && !help && len(cmdArgs) == 0 {
				return fmt.Errorf("missing argument: %v", errUsage)
			}

			subCmd, subArgs, err := cmd.Find(cmdArgs)
			if err != nil {
				return fmt.Errorf("find sub-command: %w", err)
			}

			// If a subcommand was found, run it instead.
			if subCmd != cmd {
				return fc.run(subCmd, subArgs)
			}

			vtx := rec.Vertex("cmd-func-exec", cmd.CommandPath(), progrock.Focused())
			defer func() { vtx.Done(err) }()

			setCmdOutput(cmd, vtx)

			// Make sure --help is running on the leaf command (subCmd == cmd),
			// otherwise the first command in the chain swallows it.
			if help {
				return cmd.Help()
			}

			// At this point there should be invalid arguments if not a leaf.
			if !leaf {
				// to be safe!
				if len(subArgs) > 0 {
					return fmt.Errorf("invalid argument %q: %w", subArgs[0], errUsage)
				}
				return fmt.Errorf("missing argument: %w", errUsage)
			}

			query, _ := c.q.Build(ctx)
			rec.Debug("executing", progrock.Labelf("query", "%+v", query))

			var response any

			q := c.q.Bind(&response)

			if err := q.Execute(ctx, c.c); err != nil {
				return err
			}

			if kind != dagger.Voidkind {
				// TODO: Handle different return types.
				cmd.Println(response)
			}

			return nil
		},
	}

	if fn.Name != newCmd.Name() {
		newCmd.Aliases = append(newCmd.Aliases, fn.Name)
	}

	newCmd.InitDefaultHelpFlag()
	newCmd.Flags().SetInterspersed(false)

	return newCmd
}

// setFlags adds flags to the Cobra sub-command corresponding to this function..
func (fc *FuncCommand) setFlags(cmd *cobra.Command, fn *modFunction) error {
	for _, arg := range fn.Args {
		err := fc.setFlag(cmd, arg)
		if err != nil {
			return fmt.Errorf("set flag %q: %w", arg.FlagName(), err)
		}

		// TODO: This won't work on its own because cobra checks required
		// flags before the RunE function is called. We need to manually
		// check the flags after parsing them.
		if !arg.TypeDef.Optional {
			cmd.MarkFlagRequired(arg.FlagName())
		}
	}

	// TODO: Replace this with command verbs.
	// TODO: Handle more types. These are just examples to get started.
	if fn.ReturnType.Kind == dagger.Objectkind {
		switch fn.ReturnType.AsObject.Name {
		case Directory, File:
			cmd.Flags().String("export-path", "", "Path to export the result to")
		case Container:
			cmd.Flags().Bool("stdout", true, "Print the container's stdout")
		}
	}

	return nil
}

// selectFunc adds the function selection to the query.
// Note that the type can change if there's an extra selection for supported types.
func (fc *FuncCommand) selectFunc(fn *modFunction, c *callContext, flags *pflag.FlagSet, dag *dagger.Client) (dagger.TypeDefKind, error) {
	kind := fn.ReturnType.Kind
	c.Select(fn.Name)

	// TODO: Check required flags.
	for _, arg := range fn.Args {
		arg := arg

		val, err := fc.getValue(flags, arg, dag)
		if err != nil {
			return "", fmt.Errorf("get value for flag %q: %w", arg.FlagName(), err)
		}

		c.Arg(arg.Name, val)
	}

	// TODO: Replace this with command verbs.
	// TODO: Handle more types. These are just examples to get started.
	switch kind {
	case dagger.Objectkind:
		if len(fn.ReturnType.AsObject.Functions) > 0 {
			return kind, nil
		}
		switch fn.ReturnType.AsObject.Name {
		case Directory, File:
			val, err := flags.GetString("export-path")
			if err != nil {
				return "", err
			}
			if val != "" {
				c.Select("export")
				c.Arg("path", val)
				kind = dagger.Booleankind
			}
		case Container:
			val, err := flags.GetBool("stdout")
			if err != nil {
				return "", err
			}
			if val {
				c.Select("stdout")
				kind = dagger.Stringkind
			}
		default:
			return "", fmt.Errorf("unsupported object return type %q", fn.ReturnType.AsObject.Name)
		}
	case dagger.Listkind:
		return "", fmt.Errorf("unsupported list return type")
	}
	return kind, nil
}

func (*FuncCommand) setFlag(cmd *cobra.Command, arg *modFunctionArg) error {
	flags := cmd.Flags()

	// TODO: Handle more types
	switch arg.TypeDef.Kind {
	case dagger.Stringkind:
		flags.String(arg.FlagName(), "", arg.Description)
	case dagger.Integerkind:
		flags.Int(arg.FlagName(), 0, arg.Description)
	case dagger.Booleankind:
		flags.Bool(arg.FlagName(), false, arg.Description)
	case dagger.Objectkind:
		switch arg.TypeDef.AsObject.Name {
		case Directory, File, Secret:
			flags.String(arg.FlagName(), "", arg.Description)
			switch arg.TypeDef.AsObject.Name {
			case Directory:
				return cmd.MarkFlagDirname(arg.flagName)
			case File:
				return cmd.MarkFlagFilename(arg.flagName)
			}
		default:
			return fmt.Errorf("unsupported object type %q", arg.TypeDef.AsObject.Name)
		}
	default:
		return fmt.Errorf("unsupported type %q", arg.TypeDef.Kind)
	}
	return nil
}

func (*FuncCommand) getValue(flags *pflag.FlagSet, arg *modFunctionArg, dag *dagger.Client) (any, error) {
	// TODO: Handle more types
	switch arg.TypeDef.Kind {
	case dagger.Stringkind:
		return flags.GetString(arg.FlagName())
	case dagger.Integerkind:
		return flags.GetInt(arg.FlagName())
	case dagger.Booleankind:
		return flags.GetBool(arg.FlagName())
	case dagger.Objectkind:
		val, err := flags.GetString(arg.FlagName())
		if err != nil {
			return nil, err
		}
		switch arg.TypeDef.AsObject.Name {
		case Directory:
			return dag.Host().Directory(val), nil
		case File:
			return dag.Host().File(val), nil
		case Secret:
			hash := sha256.Sum256([]byte(val))
			name := hex.EncodeToString(hash[:])
			return dag.SetSecret(name, val), nil
		default:
			return nil, fmt.Errorf("unsupported object type %q", arg.TypeDef.AsObject.Name)
		}
	default:
		return nil, fmt.Errorf("unsupported type %q", arg.TypeDef.Kind)
	}
}

// isLeaf returns true if a return type is a leaf, which requires execution
func isLeaf(kind dagger.TypeDefKind) bool {
	return kind != dagger.Objectkind
}
