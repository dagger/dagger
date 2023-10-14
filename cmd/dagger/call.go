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
	"dagger.io/dagger/querybuilder"
	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/dagger/engine/client"
	"github.com/iancoleman/strcase"
	"github.com/juju/ansiterm/tabwriter"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/vito/progrock"
	"golang.org/x/term"
)

var (
	callFlags = pflag.NewFlagSet("call", pflag.ContinueOnError)
	errUsage  = fmt.Errorf("run with --help for usage")
)

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
			return fmt.Errorf("parse command flags: %w", err)
		}

		return loadModCmdWrapper(RunFunction, "")(cmd, args)
	},
}

type callObjectType struct {
	Name      string
	Functions []*callFunction
	orig      *dagger.ObjectTypeDef
}

type callFunction struct {
	Name        string
	CmdName     string
	Description string
	Args        []*callFunctionArg
	Kind        dagger.TypeDefKind
	AsObject    *callObjectType
	orig        dagger.Function
}

type callFunctionArg struct {
	Name         string
	FlagName     string
	Description  string
	Optional     bool
	DefaultValue any
	AsObject     *callObjectType
	Kind         dagger.TypeDefKind
	orig         dagger.FunctionArg
}

type callContext struct {
	c       graphql.Client
	q       *querybuilder.Selection
	modObjs []*callObjectType
	vtx     *progrock.VertexRecorder
}

func (c *callContext) Select(name string) {
	c.q = c.q.Select(gqlFieldName(name))
}

func (c *callContext) Arg(name string, value any) {
	c.q = c.q.Arg(gqlArgName(name), value)
}

func (c *callContext) AddCommand(cmd *cobra.Command) {
	// TODO: Is there a way to update the name of the vertex?
	// This is a hack to avoid showing secrets by only presenting the
	// chain of functions as they're discovered. Need a more robust way
	// of handling it though.
	c.vtx.Vertex.Name = cmd.CommandPath()
	c.vtx.Recorder.Record(&progrock.StatusUpdate{
		Vertexes: []*progrock.Vertex{
			c.vtx.Vertex,
		},
	})
}

const (
	Directory string = "Directory"
	Container string = "Container"
	File      string = "File"
	Secret    string = "Secret"
)

func ListFunctions(ctx context.Context, engineClient *client.Client, mod *dagger.Module, cmd *cobra.Command, cmdArgs []string) (err error) {
	if mod == nil {
		return fmt.Errorf("no module specified and no default module found in current directory")
	}

	dag := engineClient.Dagger()
	rec := progrock.FromContext(ctx)
	vtx := rec.Vertex("cmd-list-functions", "list functions", progrock.Focused())
	defer func() { vtx.Done(err) }()

	loadFuncs := vtx.Task("loading functions")
	_, obj, err := loadModuleContext(ctx, dag, mod, vtx)
	loadFuncs.Done(err)
	if err != nil {
		return fmt.Errorf("load module context: %w", err)
	}

	tw := tabwriter.NewWriter(vtx.Stdout(), 0, 0, 2, ' ', 0)

	if stdoutIsTTY {
		fmt.Fprintf(tw, "%s\t%s\n", termenv.String("function name").Bold(), termenv.String("description").Bold())
	}

	for _, fn := range obj.Functions {
		// TODO: Add a third column with available verbs.
		fmt.Fprintf(tw, "%s\t%s\n", fn.Name, fn.Description)
	}

	return tw.Flush()
}

func RunFunction(ctx context.Context, engineClient *client.Client, mod *dagger.Module, cmd *cobra.Command, cmdArgs []string) (err error) {
	if mod == nil {
		return fmt.Errorf("no module specified and no default module found in current directory")
	}

	dag := engineClient.Dagger()
	rec := progrock.FromContext(ctx)
	vtx := rec.Vertex("cmd-func-loader", cmd.Name(), progrock.Focused())
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

	c, obj, err := loadModuleContext(ctx, dag, mod, vtx)
	if err != nil {
		return fmt.Errorf("load module context: %w", err)
	}

	// Select constructor
	c.Select(obj.Name)

	// Add main object's functions as subcommands
	obj.AddSubCommands(ctx, dag, cmd, c)

	if help, _ := callFlags.GetBool("help"); help {
		return cmd.Help()
	}

	subCmd, subArgs, err := cmd.Find(callFlags.Args())
	if err != nil {
		return fmt.Errorf("find command: %w", err)
	}

	if subCmd.Name() == cmd.Name() {
		if len(subArgs) > 0 {
			return fmt.Errorf("unknown command %q for %q", subArgs[0], cmd.CommandPath())
		}
		return cmd.Help()
	}

	if err = subCmd.RunE(subCmd, subArgs); err != nil {
		return fmt.Errorf("execute command %q: %w", subCmd.Name(), err)
	}

	return nil
}

// loadModuleContext returns an execution context for the query builder and the module's main object
func loadModuleContext(ctx context.Context, dag *dagger.Client, mod *dagger.Module, vtx *progrock.VertexRecorder) (*callContext, *callObjectType, error) {
	// TODO: Use a GraphQL query to get all the properties we need
	// in a single request.

	objs, err := mod.Objects(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get module objects: %w", err)
	}

	c := &callContext{
		q:   querybuilder.Query(),
		c:   dag.GraphQLClient(),
		vtx: vtx,
	}

	// Load all objects into the context so we can get return type definitions
	// that include the object's functions later on.
	for _, def := range objs {
		def := def

		obj, err := newObjectType(ctx, def.AsObject())
		if err != nil {
			return nil, nil, fmt.Errorf("create object type: %w", err)
		}

		c.modObjs = append(c.modObjs, obj)
	}

	modName, err := mod.Name(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get module name: %w", err)
	}

	obj := c.GetObject(modName)
	if obj == nil {
		return nil, nil, fmt.Errorf("main object not found")
	}

	if err := obj.LoadFunctions(ctx, dag, c); err != nil {
		return nil, nil, fmt.Errorf("load main object's functions: %w", err)
	}

	return c, obj, nil
}

func (c *callContext) GetObject(name string) *callObjectType {
	for _, obj := range c.modObjs {
		// Normalize name in case an SDK uses a different convention for object names.
		if gqlFieldName(obj.Name) == gqlFieldName(name) {
			return obj
		}
	}
	return nil
}

func newObjectType(ctx context.Context, def *dagger.ObjectTypeDef) (*callObjectType, error) {
	if def == nil {
		return nil, fmt.Errorf("not an object type")
	}

	name, err := def.Name(ctx)
	if err != nil {
		return nil, fmt.Errorf("get object name: %w", err)
	}

	return &callObjectType{
		Name: name,
		orig: def,
	}, nil
}

func (o *callObjectType) LoadFunctions(ctx context.Context, dag *dagger.Client, c *callContext) error {
	// TODO: User a GraphQL query to get all the properties we need
	// in a single request.

	if o.Functions != nil {
		return nil
	}

	funcs, err := o.orig.Functions(ctx)
	if err != nil {
		return fmt.Errorf("get functions: %w", err)
	}

	if funcs == nil {
		return nil
	}

	for _, fn := range funcs {
		fn := fn

		name, err := fn.Name(ctx)
		if err != nil {
			return fmt.Errorf("get function name: %w", err)
		}

		description, err := fn.Description(ctx)
		if err != nil {
			return fmt.Errorf("get function description: %w", err)
		}

		kind, err := fn.ReturnType().Kind(ctx)
		if err != nil {
			return fmt.Errorf("get function return type: %w", err)
		}

		f := &callFunction{
			orig:        fn,
			Name:        name,
			CmdName:     cliName(name),
			Kind:        kind,
			Description: strings.TrimSpace(description),
		}

		if kind == dagger.Objectkind {
			obj, err := newObjectType(ctx, fn.ReturnType().AsObject())
			if err != nil {
				return fmt.Errorf("create object type for function: %w", err)
			}

			// If the function returns a type that's declared in this module
			// replace the type def with the one loaded in the beginning
			// because type defs from return type don't include the object's
			// functions.
			if cached := c.GetObject(obj.Name); cached != nil {
				obj = cached
			}

			f.AsObject = obj
		}

		o.Functions = append(o.Functions, f)
	}
	return nil
}

func (o *callObjectType) AddSubCommands(ctx context.Context, dag *dagger.Client, cmd *cobra.Command, c *callContext) {
	for _, fn := range o.Functions {
		subCmd := fn.MakeCmd(ctx, dag, c)
		cmd.AddCommand(subCmd)
	}
}

func (fn *callFunction) LoadArguments(ctx context.Context) error {
	// TODO: User a GraphQL query to get all the properties we need
	// in a single request.

	if fn.Args != nil {
		return nil
	}

	args, err := fn.orig.Args(ctx)
	if err != nil {
		return fmt.Errorf("get args: %w", err)
	}

	for _, arg := range args {
		arg := arg

		name, err := arg.Name(ctx)
		if err != nil {
			return fmt.Errorf("get arg name: %w", err)
		}

		description, err := arg.Description(ctx)
		if err != nil {
			return fmt.Errorf("get arg %q description: %w", name, err)
		}

		defaultJSON, err := arg.DefaultValue(ctx)
		if err != nil {
			return fmt.Errorf("get arg %q default value: %w", name, err)
		}

		var defaultVal any
		if defaultJSON != "" {
			if err = json.Unmarshal([]byte(defaultJSON), &defaultVal); err != nil {
				return fmt.Errorf("unmarshal arg %q default value: %w", name, err)
			}
		}

		kind, err := arg.TypeDef().Kind(ctx)
		if err != nil {
			return fmt.Errorf("get arg %q type: %w", name, err)
		}

		optional, err := arg.TypeDef().Optional(ctx)
		if err != nil {
			return fmt.Errorf("check if arg %q is optional: %w", name, err)
		}

		r := &callFunctionArg{
			orig:         arg,
			Name:         name,
			FlagName:     cliName(name),
			Description:  strings.TrimSpace(description),
			Kind:         kind,
			Optional:     optional,
			DefaultValue: defaultVal,
		}

		if kind == dagger.Objectkind {
			obj, err := newObjectType(ctx, arg.TypeDef().AsObject())
			if err != nil {
				return fmt.Errorf("create object type for function: %w", err)
			}
			r.AsObject = obj
		}

		fn.Args = append(fn.Args, r)
	}

	return nil
}

func (fn *callFunction) MakeCmd(ctx context.Context, dag *dagger.Client, c *callContext) *cobra.Command {
	newCmd := &cobra.Command{
		Use:   fn.CmdName,
		Short: fn.Description,
		RunE: func(cmd *cobra.Command, args []string) error {
			c.AddCommand(cmd)

			// Only load function arguments in RunE, when it's actually needed.
			if err := fn.LoadArguments(ctx); err != nil {
				return fmt.Errorf("load function %q arguments: %w", fn.Name, err)
			}

			for _, arg := range fn.Args {
				arg := arg

				err := arg.SetFlag(cmd.Flags())
				if err != nil {
					return fmt.Errorf("set flag %q: %w", arg.FlagName, err)
				}

				// TODO: This won't work on its own because cobra checks required
				// flags before the RunE function is called. We need to manually
				// check the flags after parsing them.
				if !arg.Optional {
					cmd.MarkFlagRequired(arg.FlagName)
				}
			}

			// Possibly add a few helping flags based on the function's return type.
			fn.SetReturnFlag(cmd.Flags())

			if err := cmd.ParseFlags(args); err != nil {
				return fmt.Errorf("parse sub-command flags: %w", err)
			}

			// Load functions before selection in order to detect supported objects.
			if fn.AsObject != nil {
				if err := fn.AsObject.LoadFunctions(ctx, dag, c); err != nil {
					return fmt.Errorf("load functions from %q: %w", fn.AsObject.Name, err)
				}
				fn.AsObject.AddSubCommands(ctx, dag, cmd, c)
			}

			// Need to make the query selection before chaining off.
			kind, err := fn.Select(c, dag, cmd.Flags())
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
				if err = subCmd.RunE(subCmd, subArgs); err != nil {
					return fmt.Errorf("execute sub-command %q: %w", subCmd.Name(), err)
				}
				return nil
			}

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
			rec := progrock.FromContext(ctx)
			rec.Debug("executing", progrock.Labelf("query", "%+v", query))

			var response any

			q := c.q.Bind(&response)

			if err := q.Execute(ctx, c.c); err != nil {
				return err
			}

			if fn.Kind == dagger.Voidkind {
				return nil
			}

			// TODO: Handle different return types.
			cmd.Println(response)

			return nil
		},
	}

	newCmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Show help for '%s'", fn.CmdName))
	newCmd.Flags().SetInterspersed(false)

	return newCmd
}

// SetReturnFlag adds an extra flag on supported types to select a leaf value.
func (fn *callFunction) SetReturnFlag(flags *pflag.FlagSet) {
	// TODO: Replace this with command verbs.
	// TODO: Handle more types. These are just examples to get started.
	if fn.Kind == dagger.Objectkind {
		switch fn.AsObject.Name {
		case Directory, File:
			flags.String("export-path", "", "Path to export the result to")
		case Container:
			flags.Bool("stdout", true, "Print the container's stdout")
		}
	}
}

// Select adds the function selection to the query.
// The type can change if there's an extra selection for supported types.
func (fn *callFunction) Select(c *callContext, dag *dagger.Client, flags *pflag.FlagSet) (dagger.TypeDefKind, error) {
	kind := fn.Kind
	c.Select(fn.Name)

	// TODO: Check required flags.
	for _, arg := range fn.Args {
		arg := arg

		val, err := arg.GetValue(dag, flags)
		if err != nil {
			return "", fmt.Errorf("get value for flag %q: %w", arg.FlagName, err)
		}

		c.Arg(arg.Name, val)
	}

	// TODO: Replace this with command verbs.
	// TODO: Handle more types. These are just examples to get started.
	switch kind {
	case dagger.Objectkind:
		if len(fn.AsObject.Functions) > 0 {
			return kind, nil
		}
		switch fn.AsObject.Name {
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
			return "", fmt.Errorf("unsupported object return type %q", fn.AsObject.Name)
		}
	case dagger.Listkind:
		return "", fmt.Errorf("unsupported list return type")
	}
	return kind, nil
}

func (r *callFunctionArg) SetFlag(flags *pflag.FlagSet) error {
	// TODO: Handle more types
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
		case Directory, File, Secret:
			flags.String(r.FlagName, "", r.Description)
		default:
			return fmt.Errorf("unsupported object type %q", r.AsObject.Name)
		}
	default:
		return fmt.Errorf("unsupported type %q", r.Kind)
	}
	return nil
}

func (r *callFunctionArg) GetValue(dag *dagger.Client, flags *pflag.FlagSet) (any, error) {
	// TODO: Handle more types
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
		case Directory:
			return dag.Host().Directory(val), nil
		case File:
			return dag.Host().File(val), nil
		case Secret:
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

// isLeaf returns true if a return type is a leaf, which requires execution
func isLeaf(kind dagger.TypeDefKind) bool {
	return kind != dagger.Objectkind
}

// gqlFieldName converts casing to a GraphQL object field name
func gqlFieldName(name string) string {
	return strcase.ToLowerCamel(name)
}

// gqlArgName converts casing to a GraphQL field argument name
func gqlArgName(name string) string {
	return strcase.ToLowerCamel(name)
}

// cliName converts casing to the CLI convention (kebab)
func cliName(name string) string {
	return strcase.ToKebab(name)
}
