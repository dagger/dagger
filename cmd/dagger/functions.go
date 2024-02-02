package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"github.com/dagger/dagger/dagql/ioctx"
	"github.com/dagger/dagger/engine/client"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vito/progrock"
)

const (
	Directory   string = "Directory"
	Container   string = "Container"
	File        string = "File"
	Secret      string = "Secret"
	Service     string = "Service"
	Terminal    string = "Terminal"
	PortForward string = "PortForward"
	CacheVolume string = "CacheVolume"
)

var funcGroup = &cobra.Group{
	ID:    "functions",
	Title: "Function Commands",
}

var funcCmds = FuncCommands{
	funcListCmd,
	callCmd,
}

var funcListCmd = &FuncCommand{
	Name:  "functions [flags] [FUNCTION]...",
	Short: `List available functions`,
	Long: strings.ReplaceAll(`List available functions in a module.

This is similar to ´dagger call --help´, but only focused on showing the
available functions.
`,
		"´",
		"`",
	),
	Execute: func(fc *FuncCommand, cmd *cobra.Command) error {
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)
		var o functionProvider = fc.mod.GetMainObject()
		fmt.Fprintf(tw, "%s\t%s\n",
			termenv.String("Name").Bold(),
			termenv.String("Description").Bold(),
		)
		// Walk the hypothetical function pipeline specified by the args
		for _, field := range cmd.Flags().Args() {
			// Lookup the next function in the specified pipeline
			nextFunc, err := o.GetFunction(field)
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
				o = fc.mod.GetFunctionProvider(nextType.Name())
				continue
			}
			// FIXME: handle arrays of objects
			return fmt.Errorf("function '%s' returns non-object type %v", field, nextType.Kind)
		}
		// List functions on the final object
		fns := o.GetFunctions()
		sort.Slice(fns, func(i, j int) bool {
			return fns[i].Name < fns[j].Name
		})
		for _, fn := range fns {
			desc := strings.SplitN(fn.Description, "\n", 2)[0]
			if desc == "" {
				desc = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\n",
				cliName(fn.Name),
				desc,
			)
		}
		return tw.Flush()
	},
}

type FuncCommands []*FuncCommand

func (fcs FuncCommands) AddFlagSet(flags *pflag.FlagSet) {
	for _, cmd := range fcs.All() {
		cmd.PersistentFlags().AddFlagSet(flags)
	}
}

func (fcs FuncCommands) AddParent(rootCmd *cobra.Command) {
	rootCmd.AddCommand(fcs.All()...)
}

func (fcs FuncCommands) All() []*cobra.Command {
	cmds := make([]*cobra.Command, len(fcs))
	for i, fc := range fcs {
		cmds[i] = fc.Command()
	}
	return cmds
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

// FuncCommand is a config object used to create a dynamic set of commands
// for querying a module's functions.
type FuncCommand struct {
	// The name of the command (or verb), as shown in usage.
	Name string

	// Aliases is an array of aliases that can be used instead of the first word in Use.
	Aliases []string

	// Short is the short description shown in the 'help' output.
	Short string

	// Long is the long message shown in the 'help <this-command>' output.
	Long string

	// Example is examples of how to use the command.
	Example string

	// Init is called when the command is created and initialized,
	// before execution.
	//
	// It can be useful to add persistent flags for all subcommands here.
	Init func(*cobra.Command)

	// Execute circumvents the default behavior of traversing  subcommands
	// from the arguments, but still has access to the loaded objects from
	// the module.
	Execute func(*FuncCommand, *cobra.Command) error

	// BeforeParse is called before parsing the flags for a subcommand.
	//
	// It can be useful to add any additional flags for a subcommand here.
	BeforeParse func(*FuncCommand, *cobra.Command, *modFunction) error

	// OnSelectObjectLeaf is called when a user provided command ends in a
	// object and no more sub-commands are provided.
	//
	// If set, it should make another selection on the object that results
	// return no error. Otherwise if it doesn't handle the object, it should
	// return an error.
	OnSelectObjectLeaf func(*FuncCommand, string) error

	// BeforeRequest is called before making the request with the query that
	// contains the whole chain of functions.
	//
	// It can be useful to validate the return type of the function or as a
	// last effort to select a GraphQL sub-field.
	BeforeRequest func(*FuncCommand, *cobra.Command, *modTypeDef) error

	// AfterResponse is called when the query has completed and returned a result.
	AfterResponse func(*FuncCommand, *cobra.Command, *modTypeDef, any) error

	// cmd is the parent cobra command.
	cmd *cobra.Command

	// mod is the loaded module definition.
	mod *moduleDef

	// showHelp is set in the loader vertex to flag whether to show the help
	// in the execution vertex.
	showHelp bool

	// showUsage flags whether to show a one-line usage message after error.
	showUsage bool

	q *querybuilder.Selection
	c *client.Client
}

func (fc *FuncCommand) Command() *cobra.Command {
	if fc.cmd == nil {
		fc.cmd = &cobra.Command{
			Use:     fc.Name,
			Aliases: fc.Aliases,
			Short:   fc.Short,
			Long:    fc.Long,
			Example: fc.Example,
			GroupID: moduleGroup.ID,
			Annotations: map[string]string{
				"experimental": "",
			},

			// We need to disable flag parsing because it'll act on --help
			// and validate the args before we have a chance to add the
			// subcommands.
			DisableFlagParsing:    true,
			DisableFlagsInUseLine: true,

			PreRunE: func(c *cobra.Command, a []string) error {
				// Recover what DisableFlagParsing disabled.
				// In PreRunE it's, already past the --help check and
				// args validation, but not flag validation which we want.
				c.DisableFlagParsing = false

				// Since we disabled flag parsing, we'll get all args,
				// not just flags. We want to stop parsing at the first
				// possible dynamic sub-command since they've not been
				// added yet.
				c.Flags().SetInterspersed(false)

				// Allow using flags with the name that was reported
				// by the SDK. This avoids confusion as users are editing
				// a module and trying to test its functions.
				c.SetGlobalNormalizationFunc(func(f *pflag.FlagSet, name string) pflag.NormalizedName {
					return pflag.NormalizedName(cliName(name))
				})

				// temporarily allow unknown flags so we can parse any global flags before starting
				// the engine+TUI while not erroring out on module constructor flags (which can't be
				// added until the engine has started)
				c.FParseErrWhitelist.UnknownFlags = true
				if err := c.ParseFlags(a); err != nil {
					// This gives a chance for FuncCommand implementations to
					// handle errors from parsing flags.
					return c.FlagErrorFunc()(c, err)
				}
				c.FParseErrWhitelist.UnknownFlags = false

				fc.showHelp, _ = c.Flags().GetBool("help")

				return nil
			},

			// Between PreRunE and RunE, flags are validated.
			RunE: func(c *cobra.Command, a []string) error {
				return withEngineAndTUI(c.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) (rerr error) {
					fc.c = engineClient

					// withEngineAndTUI changes the context.
					c.SetContext(ctx)

					// We need to print the errors ourselves because the root command
					// will print the command path for this one (parent), not any
					// sub-command.
					c.SilenceErrors = true

					return fc.execute(c, a)
				})
			},
		}

		if fc.Init != nil {
			fc.Init(fc.cmd)
		}
	}
	return fc.cmd
}

func (fc *FuncCommand) execute(c *cobra.Command, a []string) (rerr error) {
	ctx := c.Context()
	rec := progrock.FromContext(ctx)

	// NB: Don't print full os.Args in Vertex name because we don't know which
	// flags hold a secret value yet and don't want to risk exposing them.
	// We'll print just the command path when we have the leaf command.
	loader := rec.Vertex("cmd-func-loader", "load "+c.Name())
	setCmdOutput(c, loader)

	cmd, flags, err := fc.load(c, a, loader)
	loader.Done(err)
	if err != nil {
		return err
	}

	cmd.SetOut(ioctx.Stdout(ctx))
	cmd.SetErr(ioctx.Stderr(ctx))

	defer func() {
		if rerr != nil {
			cmd.PrintErrln("Error:", rerr.Error())

			if fc.showUsage {
				cmd.PrintErrf("Run '%v --help' for usage.\n", cmd.CommandPath())
			}
		}
	}()

	// TODO: Move help output out of progrock?
	if fc.showHelp {
		// Hide aliases for sub-commands. They just allow using the SDK's
		// casing for functions but there's no need to advertise.
		if cmd != c {
			cmd.Aliases = nil
		}
		return cmd.Help()
	}

	// There should be no args left, if there are it's an unknown command.
	if err := cobra.NoArgs(cmd, flags); err != nil {
		return err
	}

	if fc.Execute != nil {
		return fc.Execute(fc, cmd)
	}

	// No args to the parent command, default to showing help.
	if cmd == c {
		return cmd.Help()
	}

	err = cmd.RunE(cmd, flags)
	if err != nil {
		return err
	}

	return nil
}

func (fc *FuncCommand) load(c *cobra.Command, a []string, vtx *progrock.VertexRecorder) (cmd *cobra.Command, _ []string, rerr error) {
	ctx := c.Context()
	dag := fc.c.Dagger()

	// Print error in current vertex, before completing it.
	defer func() {
		if cmd == nil {
			cmd = c
		}
		if rerr != nil {
			cmd.PrintErrln("Error:", rerr.Error())

			if fc.showHelp {
				// Explicitly show the help here while still returning the error.
				// This handles the case of `dagger call --help` run on a broken module; in that case
				// we want to error out since we can't actually load the module and show all subcommands
				// and flags in the help output, but we still want to show the user *something*
				cmd.Help()
			}

			if fc.showUsage {
				cmd.PrintErrf("Run '%v --help' for usage.\n", cmd.CommandPath())
			}
		}
	}()

	load := vtx.Task("loading module")
	modConf, err := getDefaultModuleConfiguration(ctx, dag, true)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get configured module: %w", err)
	}
	if !modConf.FullyInitialized() {
		return nil, nil, fmt.Errorf("module at source dir %q doesn't exist or is invalid", modConf.LocalRootSourcePath)
	}
	mod := modConf.Source.AsModule().Initialize()
	_, err = mod.Serve(ctx)
	load.Done(err)
	if err != nil {
		return nil, nil, err
	}

	load = vtx.Task("loading objects")
	modDef, err := loadModTypeDefs(ctx, dag, mod)
	load.Done(err)
	if err != nil {
		return nil, nil, err
	}

	obj := modDef.GetMainObject()
	if obj == nil {
		return nil, nil, fmt.Errorf("main object not found")
	}

	fc.mod = modDef

	if fc.Execute != nil {
		// if `Execute` is set, there's no need for sub-commands.
		return nil, nil, nil
	}

	if obj.Constructor != nil {
		// add constructor args as top-level flags
		if err := fc.addArgsForFunction(c, a, obj.Constructor, dag); err != nil {
			return nil, nil, err
		}
		fc.selectFunc(obj.Name, obj.Constructor, c, dag)
	} else {
		fc.Select(obj.Name)
	}

	// Add main object's functions as subcommands
	fc.addSubCommands(c, dag, obj)

	if fc.showHelp {
		return nil, nil, nil
	}

	traverse := vtx.Task("traversing arguments")
	cmd, flags, err := fc.traverse(c)
	defer func() { traverse.Done(rerr) }()

	if err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			fc.showHelp = true
			return cmd, flags, nil
		}
		fc.showUsage = true
		return cmd, flags, err
	}

	return cmd, flags, nil
}

// traverse the arguments to build the command tree and return the leaf command.
func (fc *FuncCommand) traverse(c *cobra.Command) (*cobra.Command, []string, error) {
	cmd, args, err := c.Find(c.Flags().Args())
	if err != nil {
		return cmd, args, err
	}

	// Leaf command
	if cmd == c {
		return cmd, args, nil
	}

	cmd.SetContext(c.Context())
	cmd.InitDefaultHelpFlag()

	// Load and ParseFlags
	err = cmd.PreRunE(cmd, args)
	if err != nil {
		return cmd, args, err
	}

	return fc.traverse(cmd)
}

func (fc *FuncCommand) addSubCommands(cmd *cobra.Command, dag *dagger.Client, fnProvider functionProvider) {
	if fnProvider != nil {
		cmd.AddGroup(funcGroup)
		for _, fn := range fnProvider.GetFunctions() {
			subCmd := fc.makeSubCmd(dag, fn)
			cmd.AddCommand(subCmd)
		}
	}
}

func (fc *FuncCommand) makeSubCmd(dag *dagger.Client, fn *modFunction) *cobra.Command {
	newCmd := &cobra.Command{
		Use:     cliName(fn.Name),
		Short:   strings.SplitN(fn.Description, "\n", 2)[0],
		Long:    fn.Description,
		GroupID: funcGroup.ID,
		PreRunE: func(cmd *cobra.Command, args []string) (err error) {
			if err := fc.addArgsForFunction(cmd, args, fn, dag); err != nil {
				return err
			}

			fnProvider := fn.ReturnType.AsFunctionProvider()
			if fnProvider == nil && fn.ReturnType.AsList != nil {
				fnProvider = fn.ReturnType.AsList.ElementTypeDef.AsFunctionProvider()
			}
			fc.addSubCommands(cmd, dag, fnProvider)

			// Show help for first command that has the --help flag.
			help, _ := cmd.Flags().GetBool("help")
			if help {
				return pflag.ErrHelp
			}

			// Need to make the query selection before chaining off.
			return fc.selectFunc(fn.Name, fn, cmd, dag)
		},

		// This is going to be executed in the "execution" vertex, when
		// we have the final/leaf command.
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			switch fn.ReturnType.Kind {
			case dagger.ObjectKind, dagger.InterfaceKind:
				if fc.OnSelectObjectLeaf == nil {
					// there is no handling of this object and no further selections, error out
					fc.showUsage = true
					return fmt.Errorf("%q requires a sub-command", cmd.Name())
				}

				// the top-level command may handle this via OnSelectObjectLeaf
				err := fc.OnSelectObjectLeaf(fc, fn.ReturnType.Name())
				if err != nil {
					fc.showUsage = true
					return fmt.Errorf("invalid selection for command %q: %w", cmd.Name(), err)
				}

			case dagger.ListKind:
				fnProvider := fn.ReturnType.AsList.ElementTypeDef.AsFunctionProvider()
				if fnProvider != nil && len(fnProvider.GetFunctions()) > 0 {
					// we don't handle lists of objects/interfaces w/ extra functions on any commands right now
					fc.showUsage = true
					return fmt.Errorf("%q requires a sub-command", cmd.Name())
				}
			}

			if fc.BeforeRequest != nil {
				if err = fc.BeforeRequest(fc, cmd, fn.ReturnType); err != nil {
					return err
				}
			}

			ctx := cmd.Context()
			query, _ := fc.q.Build(ctx)

			rec := progrock.FromContext(ctx)
			rec.Debug("executing", progrock.Labelf("query", "%+v", query))

			var response any

			q := fc.q.Bind(&response)

			if err := q.Execute(ctx, dag.GraphQLClient()); err != nil {
				return fmt.Errorf("response from query: %w", err)
			}

			if fc.AfterResponse != nil {
				return fc.AfterResponse(fc, cmd, fn.ReturnType, response)
			}

			if fn.ReturnType.Kind != dagger.VoidKind {
				cmd.Println(response)
			}

			return nil
		},
	}

	// Allow using the function name from the SDK as an alias for the command.
	if fn.Name != newCmd.Name() {
		newCmd.Aliases = append(newCmd.Aliases, fn.Name)
	}

	newCmd.Flags().SetInterspersed(false)

	return newCmd
}

func (fc *FuncCommand) addArgsForFunction(cmd *cobra.Command, cmdArgs []string, fn *modFunction, dag *dagger.Client) error {
	fc.mod.LoadTypeDef(fn.ReturnType)

	for _, arg := range fn.Args {
		fc.mod.LoadTypeDef(arg.TypeDef)
	}

	for _, arg := range fn.Args {
		_, err := arg.AddFlag(cmd.Flags(), dag)
		if err != nil {
			return err
		}
		if !arg.TypeDef.Optional {
			cmd.MarkFlagRequired(arg.FlagName())
		}
	}

	if fc.BeforeParse != nil {
		if err := fc.BeforeParse(fc, cmd, fn); err != nil {
			return err
		}
	}

	if err := cmd.ParseFlags(cmdArgs); err != nil {
		// This gives a chance for FuncCommand implementations to
		// handle errors from parsing flags.
		return cmd.FlagErrorFunc()(cmd, err)
	}

	help, _ := cmd.Flags().GetBool("help")
	if !help {
		if err := cmd.ValidateRequiredFlags(); err != nil {
			return err
		}
		if err := cmd.ValidateFlagGroups(); err != nil {
			return err
		}
	}

	return nil
}

// selectFunc adds the function selection to the query.
// Note that the type can change if there's an extra selection for supported types.
func (fc *FuncCommand) selectFunc(selectName string, fn *modFunction, cmd *cobra.Command, dag *dagger.Client) error {
	fc.Select(selectName)

	for _, arg := range fn.Args {
		var val any

		flag := cmd.Flags().Lookup(arg.FlagName())
		if flag == nil {
			return fmt.Errorf("no flag for %q", arg.FlagName())
		}

		// Don't send optional arguments that weren't set.
		if arg.TypeDef.Optional && !flag.Changed {
			continue
		}

		val = flag.Value

		switch v := val.(type) {
		case DaggerValue:
			obj, err := v.Get(cmd.Context(), dag)
			if err != nil {
				return fmt.Errorf("failed to get value for argument %q: %w", arg.Name, err)
			}
			if obj == nil {
				return fmt.Errorf("no value for argument: %s", arg.Name)
			}
			val = obj
		case pflag.SliceValue:
			val = v.GetSlice()
		}

		fc.Arg(arg.Name, val)
	}

	return nil
}

func (fc *FuncCommand) Select(name string) {
	if fc.q == nil {
		fc.q = querybuilder.Query()
	}
	fc.q = fc.q.Select(gqlFieldName(name))
}

func (fc *FuncCommand) Arg(name string, value any) {
	fc.q = fc.q.Arg(gqlArgName(name), value)
}
