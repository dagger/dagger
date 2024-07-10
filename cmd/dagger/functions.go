package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
)

const (
	Directory    string = "Directory"
	Container    string = "Container"
	File         string = "File"
	Secret       string = "Secret"
	Service      string = "Service"
	PortForward  string = "PortForward"
	CacheVolume  string = "CacheVolume"
	ModuleSource string = "ModuleSource"
	Module       string = "Module"
	Platform     string = "Platform"
)

var (
	skippedCmdsAnnotation = "help:skippedCmds"
	skippedOptsAnnotation = "help:skippedOpts"
)

var funcGroup = &cobra.Group{
	ID:    "functions",
	Title: "Functions",
}

var funcCmds = FuncCommands{
	funcListCmd,
	callCmd,
}

var funcListCmd = &FuncCommand{
	Name:  "functions [options] [function]...",
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
		var o functionProvider = fc.mod.MainObject.AsFunctionProvider()
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
				o = fc.mod.GetFunctionProvider(nextType.Name())
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

	// OnSelectObjectLeaf is called when a user provided command ends in a
	// object and no more sub-commands are provided.
	//
	// If set, it should make another selection on the object that results
	// return no error. Otherwise if it doesn't handle the object, it should
	// return an error.
	OnSelectObjectLeaf func(context.Context, *FuncCommand, functionProvider) error

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

	// needsHelp is set in the loader vertex to flag whether to show the help
	// in the execution vertex.
	needsHelp bool

	// showUsage flags whether to show a one-line usage message after error.
	showUsage bool

	// warnSkipped flags whether to show a warning for skipped functions and
	// arguments rather than a debug level log.
	warnSkipped bool

	q   *querybuilder.Selection
	c   *client.Client
	ctx context.Context
}

func (fc *FuncCommand) Command() *cobra.Command {
	if fc.cmd == nil {
		fc.cmd = &cobra.Command{
			Use:         fc.Name,
			Aliases:     fc.Aliases,
			Short:       fc.Short,
			Long:        fc.Long,
			Example:     fc.Example,
			GroupID:     moduleGroup.ID,
			Annotations: map[string]string{},

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

				// Do a first pass with interspersed=true to look for any
				// --help flag in the arguments. This is needed to skip
				// some validations while building the command tree, before
				// parsing the command where the --help flag is.
				help := pflag.NewFlagSet("help", pflag.ContinueOnError)
				help.AddFlag(c.Flags().Lookup("help"))

				help.ParseErrorsWhitelist.UnknownFlags = true
				help.ParseAll(a, func(flag *pflag.Flag, value string) error {
					fc.needsHelp = value == flag.NoOptDefVal
					return nil
				})

				// Stop parsing at the first possible dynamic sub-command
				// since they've not been added yet.
				c.Flags().SetInterspersed(false)

				// Global flags that affect the engine+TUI have already been
				// parsed by the root command, but there's module specific
				// flags (-m) that need to be parsed before initializing the
				// module.
				// Temporarily allow unknown flags so we can parse without
				// erroring on flags that haven't been able to load yet.
				c.FParseErrWhitelist.UnknownFlags = true
				if err := c.ParseFlags(a); err != nil {
					return c.FlagErrorFunc()(c, err)
				}
				c.FParseErrWhitelist.UnknownFlags = false

				return nil
			},

			// Between PreRunE and RunE, flags are validated.
			RunE: func(c *cobra.Command, a []string) error {
				return withEngine(c.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) (rerr error) {
					fc.c = engineClient

					// withEngine changes the context.
					c.SetContext(ctx)

					if err := fc.execute(c, a); err != nil {
						// We've already handled printing the error in `fc.execute`
						// because we want to show the usage for the right sub-command.
						// Returning Fail here will prevent the error from being printed
						// twice on main().
						return Fail
					}

					return nil
				})
			},
		}

		// Allow using flags with the name that was reported by the SDK.
		// This avoids confusion as users are editing a module and trying
		// to test its functions. For example, if a function argument is
		// `dockerConfig` in code, the user can type `--dockerConfig` or even
		// `--DockerConfig` as this normalization function rewrites to the
		// equivalent `--docker-config` in kebab-case.
		fc.cmd.SetGlobalNormalizationFunc(func(f *pflag.FlagSet, name string) pflag.NormalizedName {
			return pflag.NormalizedName(cliName(name))
		})

		if fc.Init != nil {
			fc.Init(fc.cmd)
		}
	}
	return fc.cmd
}

func (fc *FuncCommand) Help(cmd *cobra.Command) error {
	var args []any
	// We need to store these in annotations because during traversal all
	// sub-commands are created, not just the one selected. At the end of
	// traversal we'll get the final command, but without the associated
	// function or object type definitions.
	if skipped, ok := cmd.Annotations[skippedOptsAnnotation]; ok {
		args = append(args, "arguments", strings.Split(skipped, ", "))
	}
	if skipped, ok := cmd.Annotations[skippedCmdsAnnotation]; ok {
		args = append(args, "functions", strings.Split(skipped, ", "))
	}
	if len(args) > 0 {
		msg := "Skipped unsupported types"
		if fc.warnSkipped {
			slog.Warn(msg, args...)
		} else {
			slog.Debug(msg, args...)
		}
	}
	return cmd.Help()
}

// execute runs the main logic for the top level command's RunE function.
func (fc *FuncCommand) execute(c *cobra.Command, a []string) (rerr error) {
	ctx := c.Context()

	var cmd *cobra.Command
	defer func() {
		if cmd == nil { // errored during loading
			cmd = c
		}
		if ctx.Err() != nil {
			cmd.PrintErrln("Canceled.")
		} else if rerr != nil {
			cmd.PrintErrln(cmd.ErrPrefix(), rerr.Error())

			if fc.needsHelp {
				cmd.Println()
				// Explicitly show the help here while still returning the error.
				// This handles the case of `dagger call --help` run on a broken module; in that case
				// we want to error out since we can't actually load the module and show all subcommands
				// and flags in the help output, but we still want to show the user *something*
				fc.Help(cmd)
				return
			}

			if fc.showUsage {
				cmd.PrintErrf("Run '%v --help' for usage.\n", cmd.CommandPath())
			}
		}
	}()

	if err := fc.initializeModule(ctx); err != nil {
		return err
	}

	// Now that the module is loaded, show usage by default since errors
	// are more likely to be from wrong CLI usage.
	fc.showUsage = true

	cmd, flags, err := fc.loadCommand(c, a)
	if err != nil {
		return err
	}

	if fc.needsHelp {
		return fc.Help(cmd)
	}

	if fc.Execute != nil {
		return fc.Execute(fc, cmd)
	}

	// No args to the parent command
	if cmd == c {
		return fc.RunE(ctx, fc.mod.MainObject)(cmd, flags)
	}

	return cmd.RunE(cmd, flags)
}

// initializeModule loads the module's type definitions.
func (fc *FuncCommand) initializeModule(ctx context.Context) (rerr error) {
	dag := fc.c.Dagger()

	ctx, span := Tracer().Start(ctx, "initialize", telemetry.Encapsulate())
	defer telemetry.End(span, func() error { return rerr })

	modConf, err := getDefaultModuleConfiguration(ctx, dag, true, true)
	if err != nil {
		return fmt.Errorf("failed to get configured module: %w", err)
	}
	if !modConf.FullyInitialized() {
		return fmt.Errorf("module at source dir %q doesn't exist or is invalid", modConf.LocalRootSourcePath)
	}
	mod := modConf.Source.AsModule().Initialize()
	_, err = mod.Serve(ctx)
	if err != nil {
		return err
	}

	name, err := mod.Name(ctx)
	if err != nil {
		return fmt.Errorf("get module name: %w", err)
	}

	fc.mod = &moduleDef{
		Name:   name,
		Source: modConf.Source,
	}

	if err := fc.mod.loadTypeDefs(ctx, dag); err != nil {
		return err
	}

	if fc.mod.MainObject == nil {
		return fmt.Errorf("main object not found")
	}

	return nil
}

// loadCommand finds the leaf command to run.
func (fc *FuncCommand) loadCommand(c *cobra.Command, a []string) (rcmd *cobra.Command, rargs []string, rerr error) {
	// If a command implements Execute, it doesn't need to build and
	// traverse the command tree.
	if fc.Execute != nil {
		return c, nil, nil
	}

	ctx := c.Context()

	spanCtx, span := Tracer().Start(ctx, "prepare", telemetry.Encapsulate())
	defer telemetry.End(span, func() error { return rerr })
	fc.ctx = spanCtx

	builder := fc.cobraBuilder(ctx, fc.mod.MainObject.AsObject.Constructor)

	cmd, args, err := fc.traverse(c, a, builder)
	if err != nil {
		return cmd, args, err
	}

	// There should be no args left, if there are it's an unknown command.
	if err := cobra.NoArgs(cmd, args); err != nil {
		return cmd, args, err
	}

	return cmd, args, nil
}

// traverse recursively builds the command tree, until the leaf command is found.
func (fc *FuncCommand) traverse(c *cobra.Command, a []string, build func(*cobra.Command, []string) error) (*cobra.Command, []string, error) {
	// Build the flags and subcommands
	err := build(c, a)
	if err != nil {
		return c, a, err
	}

	cmd, args, err := c.Find(c.Flags().Args())
	if err != nil {
		return cmd, args, err
	}

	// Leaf command
	if cmd == c {
		return cmd, args, nil
	}

	return fc.traverse(cmd, args, cmd.PreRunE)
}

// cobraBuilder returns a PreRunE compatible function to add the next set of
// flags and sub-commands to the command tree, based on a function definition.
func (fc *FuncCommand) cobraBuilder(ctx context.Context, fn *modFunction) func(*cobra.Command, []string) error {
	return func(c *cobra.Command, a []string) error {
		if err := fc.addFlagsForFunction(c, fn); err != nil {
			return err
		}

		// Even if just for --help, parsing flags is needed to clean up the
		// args while traversing sub-commands.
		if err := c.ParseFlags(a); err != nil {
			return c.FlagErrorFunc()(c, err)
		}

		fc.addSubCommands(ctx, c, fn.ReturnType)

		if fc.needsHelp {
			// May be too noisy to always show a warning for skipped functions
			// and arguments when they're from the core API. In modules, however,
			// users can do something about it. Even if it's a reusable module
			// from someone else, hopefully the author notices the warning first.
			fc.warnSkipped = !fn.ReturnsCoreObject()
			return nil
		}

		// Validate before accessing values for select.
		if err := c.ValidateRequiredFlags(); err != nil {
			return err
		}
		if err := c.ValidateFlagGroups(); err != nil {
			return err
		}

		// Easier to add query builder selections as we traverse the command tree.
		return fc.selectFunc(fn, c)
	}
}

// addFlagsForFunction creates the flags for a function's arguments.
func (fc *FuncCommand) addFlagsForFunction(cmd *cobra.Command, fn *modFunction) error {
	var skipped []string

	for _, arg := range fn.Args {
		fc.mod.LoadTypeDef(arg.TypeDef)

		if err := arg.AddFlag(cmd.Flags()); err != nil {
			var e *UnsupportedFlagError
			if errors.As(err, &e) {
				skipped = append(skipped, arg.FlagName())
				continue
			}
		}
		if arg.IsRequired() {
			cmd.MarkFlagRequired(arg.FlagName())
		}
		cmd.Flags().SetAnnotation(
			arg.FlagName(),
			"help:group",
			[]string{"Arguments"},
		)
	}

	if cmd.HasAvailableLocalFlags() {
		cmd.Use += " [arguments]"
	}

	if len(skipped) > 0 {
		cmd.Annotations[skippedOptsAnnotation] = strings.Join(skipped, ", ")
	}

	return nil
}

// addSubCommands creates sub-commands for the functions in an object or
// interface type definition.
func (fc *FuncCommand) addSubCommands(ctx context.Context, cmd *cobra.Command, typeDef *modTypeDef) {
	fc.mod.LoadTypeDef(typeDef)

	fnProvider := typeDef.AsFunctionProvider()
	if fnProvider == nil && typeDef.AsList != nil {
		fnProvider = typeDef.AsList.ElementTypeDef.AsFunctionProvider()
	}

	if fnProvider == nil {
		return
	}

	cmd.AddGroup(funcGroup)

	skipped := make([]string, 0)

	for _, fn := range fnProvider.GetFunctions() {
		if fn.IsUnsupported() {
			skipped = append(skipped, cliName(fn.Name))
			continue
		}
		subCmd := fc.makeSubCmd(ctx, fn)
		cmd.AddCommand(subCmd)
	}

	if cmd.HasAvailableSubCommands() {
		cmd.Use += " <function>"
	}

	if len(skipped) > 0 {
		cmd.Annotations[skippedCmdsAnnotation] = strings.Join(skipped, ", ")
	}
}

// makeSubCmd creates a sub-command for a function definition.
func (fc *FuncCommand) makeSubCmd(ctx context.Context, fn *modFunction) *cobra.Command {
	newCmd := &cobra.Command{
		Use:                   cliName(fn.Name),
		Short:                 strings.SplitN(fn.Description, "\n", 2)[0],
		Long:                  fn.Description,
		GroupID:               funcGroup.ID,
		DisableFlagsInUseLine: true,
		// FIXME: Persistent flags should be marked as hidden for sub-commands
		// but it's not working, so setting an annotation to circumvent it.
		Annotations: map[string]string{
			"help:hideInherited": "true",
		},
		// Using PreRunE to build the next set of flags and sub-commands.
		// This allows us to attach a function definition to a cobra.Command,
		// which simplifies the command tree traversal and building process.
		PreRunE: fc.cobraBuilder(ctx, fn),
		// This is going to be executed in the "execution" vertex, when
		// we have the final/leaf command.
		RunE: fc.RunE(ctx, fn.ReturnType),
	}

	newCmd.Flags().SetInterspersed(false)
	newCmd.SetContext(ctx)

	return newCmd
}

// selectFunc adds the function selection to the query.
func (fc *FuncCommand) selectFunc(fn *modFunction, cmd *cobra.Command) error {
	dag := fc.c.Dagger()

	fc.Select(fn.Name)

	for _, arg := range fn.SupportedArgs() {
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
			obj, err := v.Get(fc.ctx, dag, fc.mod.Source)
			if err != nil {
				return fmt.Errorf("failed to get value for argument %q: %w", arg.FlagName(), err)
			}
			if obj == nil {
				return fmt.Errorf("no value for argument: %s", arg.FlagName())
			}
			val = obj
		case pflag.SliceValue:
			val = v.GetSlice()
		}

		fc.Arg(arg.Name, val)
	}

	return nil
}

func (fc *FuncCommand) Select(name ...string) {
	if fc.q == nil {
		fc.q = querybuilder.Query().Client(fc.c.Dagger().GraphQLClient())
	}
	fc.q = fc.q.Select(name...)
}

func (fc *FuncCommand) Arg(name string, value any) {
	fc.q = fc.q.Arg(name, value)
}

func (fc *FuncCommand) RunE(ctx context.Context, typeDef *modTypeDef) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		t := typeDef
		if t.AsList != nil {
			t = t.AsList.ElementTypeDef
		}

		switch t.Kind {
		case dagger.ObjectKind, dagger.InterfaceKind:
			origSel := fc.q
			obj := t.AsFunctionProvider()

			if fc.OnSelectObjectLeaf != nil {
				if err := fc.OnSelectObjectLeaf(ctx, fc, obj); err != nil {
					return fmt.Errorf("invalid selection for command %q: %w", cmd.Name(), err)
				}
			}

			// If the selection didn't change, it means OnSelectObjectLeaf
			// didn't handle it. Rather than error, just simulate an empty
			// response for AfterResponse.
			if origSel == fc.q {
				if fc.AfterResponse == nil {
					return fmt.Errorf("%q requires a sub-command", cmd.Name())
				}
				response := map[string]any{}
				return fc.AfterResponse(fc, cmd, typeDef, response)
			}
		}

		// Silence usage from this point on as errors don't likely come
		// from wrong CLI usage.
		fc.showUsage = false

		if fc.BeforeRequest != nil {
			if err := fc.BeforeRequest(fc, cmd, typeDef); err != nil {
				return err
			}
		}

		var response any

		if err := fc.Request(ctx, &response); err != nil {
			return err
		}

		if typeDef.Kind == dagger.VoidKind {
			return nil
		}

		if fc.AfterResponse != nil {
			return fc.AfterResponse(fc, cmd, typeDef, response)
		}

		cmd.Println(response)

		return nil
	}
}

func (fc *FuncCommand) Request(ctx context.Context, response any) error {
	query, _ := fc.q.Build(ctx)

	slog.Debug("executing query", "query", query)

	q := fc.q.Bind(&response)

	if err := q.Execute(ctx); err != nil {
		return fmt.Errorf("response from query: %w", err)
	}

	return nil
}
