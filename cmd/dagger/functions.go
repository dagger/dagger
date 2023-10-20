package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/engine/client"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
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
	funcListCmd,
	callCmd,
	shellCmd,
	downloadCmd,
}

var funcListCmd = &FuncCommand{
	Name:  "functions",
	Short: `List all functions in a module`,
	Execute: func(c *callContext, cmd *cobra.Command) error {
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', tabwriter.DiscardEmptyColumns)

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			termenv.String("object name").Bold(),
			termenv.String("function name").Bold(),
			termenv.String("description").Bold(),
			termenv.String("return type").Bold(),
		)

		for _, o := range c.mod.Objects {
			if o.AsObject != nil {
				for _, fn := range o.AsObject.Fields {
					objName := o.AsObject.Name
					if gqlObjectName(objName) == gqlObjectName(c.mod.Name) {
						objName = "*" + objName
					}

					// TODO: Add another column with available verbs.
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
						objName,
						fn.Name,
						fn.Description,
						printReturnType(fn.TypeDef),
					)
				}
				for _, fn := range o.AsObject.Functions {
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

// callContext holds the execution context for a function call.
type callContext struct {
	q      *querybuilder.Selection
	e      *client.Client
	c      graphql.Client
	mod    *moduleDef
	loader *progrock.VertexRecorder
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

	// Execute circumvents the default behavior of adding subcommands and
	// executing them.
	Execute func(*callContext, *cobra.Command) error

	// OnInit is called when the command is created and initialized, before execution.
	OnInit func(*cobra.Command)

	// OnSetFlags is called when the flags have finished being set for a subcommand.
	OnSetFlags func(*cobra.Command, *modTypeDef) error

	// OnSelectObject is called when building the query on a function that
	// returns a "leaf" object (e.g., Container). This is useful to add an
	// additional field selection to the object. If the new selection changes
	// the return type of the function, return the new type definition.
	OnSelectObject func(*callContext, string) (*modTypeDef, error)

	// OnSelectObjectList is called when building the query on a function that
	// returns a list of objects. This is useful to add an additional field
	// selection to the objects. If the new selection changes the return type
	// of the function, return the new type definition.
	OnSelectObjectList func(*callContext, *modObject) (*modTypeDef, error)

	// OnSelectFunc is called after making a selection on a function to build
	// the query. This can be useful to validate the return type or make
	// any last minute changes to the query. If the new selection changes the
	// return type of the function, return the new type definition.
	OnSelectFunc func(*callContext, *modTypeDef) (*modTypeDef, error)

	// CheckReturnType is called before the query is built, to validate
	// the return type of the function. Can also be used as a last effort
	// to select a sub-field.
	CheckReturnType func(*callContext, *modTypeDef) error

	// AfterResponse is called when the query has completed and returned a result.
	AfterResponse func(*callContext, *cobra.Command, modTypeDef, any) error
	cmd           *cobra.Command
}

func (fc *FuncCommand) Command() *cobra.Command {
	if fc.cmd == nil {
		fc.cmd = &cobra.Command{
			Use:                   fmt.Sprintf("%s [flags] [command [flags]]...", fc.Name),
			Short:                 fc.Short,
			Long:                  fc.Long,
			Example:               fc.Example,
			DisableFlagsInUseLine: true,
			DisableFlagParsing:    true,
			Hidden:                true, // for now, remove once we're ready for primetime
			GroupID:               funcGroup.ID,
			RunE:                  fc.execute,
		}

		fc.cmd.Flags().SetInterspersed(false) // stop parsing at first possible dynamic subcommand

		if fc.OnInit != nil {
			fc.OnInit(fc.cmd)
		}
	}
	return fc.cmd
}

func (fc *FuncCommand) execute(cmd *cobra.Command, args []string) error {
	// Need to parse flags that affect the TUI and module loading before all else.
	err := cmd.Flags().Parse(args)
	if err != nil {
		return fmt.Errorf("parse command flags: %w", err)
	}

	return withEngineAndTUI(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) (err error) {
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
			q:      querybuilder.Query(),
			c:      dag.GraphQLClient(),
			e:      engineClient,
			mod:    modDef,
			loader: vtx,
		}

		help, _ := cmd.Flags().GetBool("help")

		if fc.Execute != nil {
			if help {
				return cmd.Help()
			}
			return fc.Execute(c, cmd)
		}

		// Select constructor
		c.Select(obj.Name)

		// Add main object's functions as subcommands
		fc.addSubCommands(ctx, dag, obj, c, cmd)

		if help {
			return cmd.Help()
		}

		subCmd, subArgs, err := cmd.Find(cmd.Flags().Args())
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
}

func (*FuncCommand) run(cmd *cobra.Command, args []string) error {
	err := cmd.RunE(cmd, args)
	if err != nil {
		return err
	}
	return nil
}

func (fc *FuncCommand) addSubCommands(ctx context.Context, dag *dagger.Client, obj *modObject, c *callContext, cmd *cobra.Command) {
	if obj != nil {
		for _, fn := range obj.Functions {
			subCmd := fc.makeSubCmd(ctx, dag, fn, c)
			cmd.AddCommand(subCmd)
		}
		for _, field := range obj.Fields {
			subCmd := fc.makeSubCmd(ctx, dag, &modFunction{Name: field.Name, Description: field.Description, ReturnType: field.TypeDef}, c)
			cmd.AddCommand(subCmd)
		}
	}
}

func (fc *FuncCommand) makeSubCmd(ctx context.Context, dag *dagger.Client, fn *modFunction, c *callContext) *cobra.Command {
	newCmd := &cobra.Command{
		Use:   cliName(fn.Name),
		Short: fn.Description,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			cmd.SetContext(ctx)
			rec := progrock.FromContext(ctx)

			c.mod.LoadObject(fn.ReturnType)

			for _, arg := range fn.Args {
				c.mod.LoadObject(arg.TypeDef)
			}

			for _, arg := range fn.Args {
				_, err := arg.AddFlag(cmd.Flags(), dag)
				if err != nil {
					return fmt.Errorf("add flag %q: %w", arg.FlagName(), err)
				}
				if !arg.TypeDef.Optional {
					cmd.MarkFlagRequired(arg.FlagName())
				}
			}

			if fc.OnSetFlags != nil {
				return fc.OnSetFlags(cmd, fn.ReturnType)
			}

			if err := cmd.ParseFlags(args); err != nil {
				return fmt.Errorf("parse flags: %w", err)
			}

			help, _ := cmd.Flags().GetBool("help")
			if !help {
				if err := cmd.ValidateRequiredFlags(); err != nil {
					return err
				}
			}

			fc.addSubCommands(ctx, dag, fn.ReturnType.AsObject, c, cmd)

			// Need to make the query selection before chaining off.
			returnType, err := fc.selectFunc(fn, c, cmd, dag)
			if err != nil {
				return fmt.Errorf("query selection: %w", err)
			}

			cmdArgs := cmd.Flags().Args()

			subCmd, subArgs, err := cmd.Find(cmdArgs)
			if err != nil {
				return fmt.Errorf("find sub-command: %w", err)
			}

			// If a subcommand was found, run it instead.
			if subCmd != cmd {
				return fc.run(subCmd, subArgs)
			}

			c.loader.Complete()

			vtx := rec.Vertex(
				"cmd-func-exec",
				fmt.Sprintf("running %q", cmd.CommandPath()),
				progrock.Focused(),
			)
			defer func() { vtx.Done(err) }()

			setCmdOutput(cmd, vtx)

			// Make sure --help is running on the leaf command (subCmd == cmd),
			// otherwise the first command in the chain swallows it.
			if help {
				return cmd.Help()
			}

			if fc.CheckReturnType != nil {
				if err = fc.CheckReturnType(c, returnType); err != nil {
					return err
				}
			}

			query, _ := c.q.Build(ctx)
			rec.Debug("executing", progrock.Labelf("query", "%+v", query))

			var response any

			q := c.q.Bind(&response)

			if err := q.Execute(ctx, c.c); err != nil {
				return err
			}

			if fc.AfterResponse != nil {
				return fc.AfterResponse(c, cmd, *returnType, response)
			}

			if returnType.Kind != dagger.Voidkind {
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

// selectFunc adds the function selection to the query.
// Note that the type can change if there's an extra selection for supported types.
func (fc *FuncCommand) selectFunc(fn *modFunction, c *callContext, cmd *cobra.Command, dag *dagger.Client) (*modTypeDef, error) {
	c.Select(fn.Name)

	// TODO: Check required flags.

	for _, arg := range fn.Args {
		var val any

		flag := cmd.Flags().Lookup(arg.FlagName())
		if flag == nil {
			return nil, fmt.Errorf("no flag for %q", arg.FlagName())
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
				return nil, fmt.Errorf("no value for argument: %s", arg.Name)
			}
			val = obj
		}
		c.Arg(arg.Name, val)
	}

	ret := fn.ReturnType

	switch ret.Kind {
	case dagger.Objectkind:
		// Possible to continue chaining.
		if len(ret.AsObject.Functions) > 0 || len(ret.AsObject.Fields) > 0 {
			break
		}
		// Otherwise this is a leaf.
		if fc.OnSelectObject != nil {
			newRet, err := fc.OnSelectObject(c, ret.AsObject.Name)
			if err != nil {
				return nil, err
			}
			if newRet != nil {
				return newRet, nil
			}
		}
	case dagger.Listkind:
		if fc.OnSelectObjectList != nil && ret.AsList.ElementTypeDef.AsObject != nil {
			newRet, err := fc.OnSelectObjectList(c, ret.AsList.ElementTypeDef.AsObject)
			if err != nil {
				return nil, err
			}
			if newRet != nil {
				return newRet, nil
			}
		}
	}

	if fc.OnSelectFunc != nil {
		newRet, err := fc.OnSelectFunc(c, ret)
		if err != nil {
			return nil, err
		}
		if newRet != nil {
			return newRet, nil
		}
	}

	return ret, nil
}
