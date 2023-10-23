package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"github.com/dagger/dagger/engine/client"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vito/progrock"
)

const (
	Directory string = "Directory"
	Container string = "Container"
	File      string = "File"
	Secret    string = "Secret"
	Service   string = "Service"
)

var funcGroup = &cobra.Group{
	ID:    "functions",
	Title: "Functions",
}

var funcCmds = FuncCommands{
	funcListCmd,
	callCmd,
	shellCmd,
	downloadCmd,
	upCmd,
}

var funcListCmd = &FuncCommand{
	Name:  "functions",
	Short: `List all functions in a module`,
	Execute: func(c *FuncCommand, cmd *cobra.Command) error {
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			termenv.String("object name").Bold(),
			termenv.String("function name").Bold(),
			termenv.String("description").Bold(),
			termenv.String("return type").Bold(),
		)

		for _, o := range c.mod.Objects {
			if o.AsObject != nil {
				for _, fn := range o.AsObject.GetFunctions() {
					objName := o.AsObject.Name
					if gqlObjectName(objName) == gqlObjectName(c.mod.Name) {
						objName = "*" + objName
					}

					// TODO: Add another column with available verbs.
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
						objName,
						fn.Name,
						fn.Description,
						printReturnType(fn.ReturnType),
					)
				}
			}
		}

		return tw.Flush()
	},
}

func printReturnType(returnType *modTypeDef) (n string) {
	defer func() {
		if !returnType.Optional {
			n += "!"
		}
	}()
	switch returnType.Kind {
	case dagger.Stringkind:
		return "String"
	case dagger.Integerkind:
		return "Int"
	case dagger.Booleankind:
		return "Boolean"
	case dagger.Objectkind:
		return returnType.AsObject.Name
	case dagger.Listkind:
		return fmt.Sprintf("[%s]", printReturnType(returnType.AsList.ElementTypeDef))
	default:
		return ""
	}
}

type FuncCommands []*FuncCommand

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

	// OnSelectObjectLeaf is called when adding a query selection on a
	// function that returns an object with no subsequent chainable
	// functions (e.g., Container).
	//
	// It can be useful to add an additional field selection for the object.
	OnSelectObjectLeaf func(*FuncCommand, string) error

	// OnSelectObjectList is called when adding a query selection on a
	// function that returns a list of objects.
	//
	// It can be useful to add an additional field selection for each object.
	OnSelectObjectList func(*FuncCommand, *modObject) error

	// BeforeRequest is called before making the request with the query that
	// contains the whole chain of functions.
	//
	// It can be useful to validate the return type of the function or as a
	// last effort to select a GraphQL sub-field.
	BeforeRequest func(*FuncCommand, *modTypeDef) error

	// AfterResponse is called when the query has completed and returned a result.
	AfterResponse func(*FuncCommand, *cobra.Command, *modTypeDef, any) error

	// cmd is the parent cobra command.
	cmd *cobra.Command

	// mod is the loaded module definition.
	mod *moduleDef

	q *querybuilder.Selection
	c *client.Client
}

func (fc *FuncCommand) Command() *cobra.Command {
	if fc.cmd == nil {
		fc.cmd = &cobra.Command{
			Use:     fmt.Sprintf("%s [flags] [command [flags]]...", fc.Name),
			Short:   fc.Short,
			Long:    fc.Long,
			Example: fc.Example,
			RunE:    fc.execute,
			GroupID: funcGroup.ID,

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
				c.Flags().SetInterspersed(false) // stop parsing at first possible dynamic subcommand

				// Allow using flags with the the name that was reported
				// by the SDK. This avoids confusion as users are editing
				// a module and trying to test its functions.
				c.SetGlobalNormalizationFunc(func(f *pflag.FlagSet, name string) pflag.NormalizedName {
					return pflag.NormalizedName(cliName(name))
				})

				err := c.ParseFlags(a)
				if err != nil {
					return c.FlagErrorFunc()(c, err)
				}

				help, _ := c.Flags().GetBool("help")
				if help {
					return pflag.ErrHelp
				}

				return nil
			},
			Hidden: true, // for now, remove once we're ready for primetime
		}

		if fc.Init != nil {
			fc.Init(fc.cmd)
		}
	}
	return fc.cmd
}

func (fc *FuncCommand) execute(cmd *cobra.Command, allArgs []string) error {
	return withEngineAndTUI(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) (rerr error) {
		cmd.SetContext(ctx)

		fc.c = engineClient
		dag := engineClient.Dagger()
		rec := progrock.FromContext(ctx)

		// Can't print full args because we don't know which ones are secrets
		// yet and don't want to risk exposing them.
		loader := rec.Vertex("cmd-func-loader", "load "+cmd.Name(), progrock.Focused())
		defer func() { loader.Done(rerr) }()
		setCmdOutput(cmd, loader)

		load := loader.Task("loading module")
		mod, err := loadMod(ctx, dag)
		load.Done(err)
		if err != nil {
			return err
		}
		if mod == nil {
			return fmt.Errorf("no module specified and no default module found in current directory")
		}

		load = loader.Task("loading objects")
		modDef, err := loadModObjects(ctx, dag, mod)
		load.Done(err)
		if err != nil {
			return err
		}

		obj := modDef.GetMainObject()
		if obj == nil {
			return fmt.Errorf("main object not found")
		}

		fc.mod = modDef

		// TODO: Move help output out of the loader vertex, which is unfocused
		// and has tasks. Could be outside the TUI altogether, by capturing
		// outside of `withEngineAndTUI`.
		help, _ := cmd.Flags().GetBool("help")

		if fc.Execute != nil {
			if help {
				return cmd.Help()
			}
			return fc.Execute(fc, cmd)
		}

		// Select constructor
		fc.Select(obj.Name)

		// Add main object's functions as subcommands
		fc.addSubCommands(cmd, dag, obj)

		if help {
			return cmd.Help()
		}

		// We need to print the errors ourselves because the root command
		// will print the command path for this one (parent), not any
		// sub-command.
		cmd.SilenceErrors = true

		traverse := loader.Task("traversing arguments")
		subCmd, flags, err := fc.traverse(cmd)
		traverse.Done(err)
		if err != nil {
			if subCmd != nil {
				cmd = subCmd
			}
			if errors.Is(err, pflag.ErrHelp) {
				return cmd.Help()
			}
			cmd.PrintErrln("Error:", err.Error())
			cmd.PrintErrf("Run '%v --help' for usage.\n", cmd.CommandPath())
			return err
		}

		loader.Complete()

		vtx := rec.Vertex("cmd-func-exec", cmd.CommandPath(), progrock.Focused())
		defer func() { vtx.Done(rerr) }()
		setCmdOutput(cmd, vtx)

		// There should be no args left, if there are it's an unknown command.
		if err := cobra.NoArgs(subCmd, flags); err != nil {
			subCmd.PrintErrln("Error:", err.Error())
			return err
		}

		// No args to the parent command, default to showing help.
		if subCmd == cmd {
			return cmd.Help()
		}

		err = subCmd.RunE(subCmd, flags)
		if err != nil {
			subCmd.PrintErrln("Error:", err.Error())
			return err
		}

		return nil
	})
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

func (fc *FuncCommand) addSubCommands(cmd *cobra.Command, dag *dagger.Client, obj *modObject) {
	if obj != nil {
		for _, fn := range obj.GetFunctions() {
			subCmd := fc.makeSubCmd(dag, fn)
			cmd.AddCommand(subCmd)
		}
	}
}

func (fc *FuncCommand) makeSubCmd(dag *dagger.Client, fn *modFunction) *cobra.Command {
	newCmd := &cobra.Command{
		Use:   cliName(fn.Name),
		Short: fn.Description,
		PreRunE: func(cmd *cobra.Command, args []string) (err error) {
			fc.mod.LoadObject(fn.ReturnType)

			for _, arg := range fn.Args {
				fc.mod.LoadObject(arg.TypeDef)
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

			if err := cmd.ParseFlags(args); err != nil {
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

			fc.addSubCommands(cmd, dag, fn.ReturnType.AsObject)

			// Show help for first command that has the --help flag.
			if help {
				return pflag.ErrHelp
			}

			// Need to make the query selection before chaining off.
			return fc.selectFunc(fn, cmd, dag)
		},
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			if fc.BeforeRequest != nil {
				if err = fc.BeforeRequest(fc, fn.ReturnType); err != nil {
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

			if fn.ReturnType.Kind != dagger.Voidkind {
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

// selectFunc adds the function selection to the query.
// Note that the type can change if there's an extra selection for supported types.
func (fc *FuncCommand) selectFunc(fn *modFunction, cmd *cobra.Command, dag *dagger.Client) error {
	fc.Select(fn.Name)

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

		dv, ok := val.(DaggerValue)
		if ok {
			obj := dv.Get(dag)
			if obj == nil {
				return fmt.Errorf("no value for argument: %s", arg.Name)
			}
			val = obj
		}

		fc.Arg(arg.Name, val)
	}

	ret := fn.ReturnType

	switch ret.Kind {
	case dagger.Objectkind:
		// Possible to continue chaining.
		if len(ret.AsObject.GetFunctions()) > 0 {
			break
		}
		// Otherwise this is a leaf.
		if fc.OnSelectObjectLeaf != nil {
			err := fc.OnSelectObjectLeaf(fc, ret.AsObject.Name)
			if err != nil {
				return err
			}
		}
	case dagger.Listkind:
		if fc.OnSelectObjectList != nil && ret.AsList.ElementTypeDef.AsObject != nil {
			err := fc.OnSelectObjectList(fc, ret.AsList.ElementTypeDef.AsObject)
			if err != nil {
				return err
			}
		}
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
