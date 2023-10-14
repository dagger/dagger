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

// modTypeDef is a representation of dagger.TypeDef
type modTypeDef struct {
	Kind     dagger.TypeDefKind
	Optional bool
	AsObject *modObject
}

// modObject is a representation of dagger.ObjectTypeDef
type modObject struct {
	Name      string
	Functions []*modFunction
}

// modFunction is a representation of dagger.Function
type modFunction struct {
	Name        string
	Description string
	ReturnType  modTypeDef
	Args        []*modFunctionArg
}

// modFunctionArg is a representation of dagger.FunctionArg
type modFunctionArg struct {
	Name        string
	Description string
	TypeDef     modTypeDef
	flagName    string
}

func (r *modFunctionArg) FlagName() string {
	if r.flagName == "" {
		r.flagName = strcase.ToKebab(r.Name)
	}
	return r.flagName
}

// callContext holds the execution context for a function call.
type callContext struct {
	c       graphql.Client
	q       *querybuilder.Selection
	modObjs []*modObject
	vtx     *progrock.VertexRecorder
}

func (c *callContext) Select(name string) {
	c.q = c.q.Select(gqlFieldName(name))
}

func (c *callContext) Arg(name string, value any) {
	c.q = c.q.Arg(gqlArgName(name), value)
}

func (c *callContext) StartCommand(cmd *cobra.Command) {
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
	obj.AddSubCommands(ctx, dag, c, cmd)

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

// loadModuleContext returns an execution context for the query builder and the module's main object.
func loadModuleContext(ctx context.Context, dag *dagger.Client, mod *dagger.Module, vtx *progrock.VertexRecorder) (*callContext, *modObject, error) {
	var res struct {
		Module struct {
			Name    string
			Objects []*modTypeDef
		}
	}

	err := dag.Do(ctx, &dagger.Request{
		Query: `
            query Objects($module: ModuleID!) {
                module: loadModuleFromID(id: $module) {
                    name
                    objects {
                        asObject {
                            name
                            functions {
                                name
                                description
                                returnType {
                                    kind
                                    asObject {
                                        name
                                    }
                                }
                                args {
                                    name
                                    description
                                    typeDef {
                                        kind
                                        optional
                                        asObject {
                                            name
                                        }
                                    }
                                }
                            }
                        }
                    }
                }

            }
        `,
		Variables: map[string]interface{}{
			"module": mod,
		},
	}, &dagger.Response{
		Data: &res,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("query module objects: %w", err)
	}

	c := &callContext{
		q:   querybuilder.Query(),
		c:   dag.GraphQLClient(),
		vtx: vtx,
	}

	// Function return types as objects don't include their functions so load
	// all module objects with included functions here in order to reuse later.
	for _, typeDef := range res.Module.Objects {
		if obj := typeDef.AsObject; obj != nil {
			c.modObjs = append(c.modObjs, obj)
		}
	}

	obj := c.GetObject(res.Module.Name)
	if obj == nil {
		return nil, nil, fmt.Errorf("main object not found")
	}

	return c, obj, nil
}

// GetObject retrieves a saved object type definition from the module.
func (c *callContext) GetObject(name string) *modObject {
	for _, obj := range c.modObjs {
		// Normalize name in case an SDK uses a different convention for object names.
		// Otherwise we could just convert `name` to upper camel case.
		if gqlFieldName(obj.Name) == gqlFieldName(name) {
			return obj
		}
	}
	return nil
}

// AddSubCommands creates and adds sub commands for all functions returned on an object type.
func (o *modObject) AddSubCommands(ctx context.Context, dag *dagger.Client, c *callContext, cmd *cobra.Command) {
	if o != nil {
		for _, fn := range o.Functions {
			subCmd := fn.MakeCmd(ctx, dag, c)
			cmd.AddCommand(subCmd)
		}
	}
}

// Load attempts to replace an object's type definition with the one initially
// loaded in the call context if present. This is necessary to recover missing
// function definitions when chaining functions.
func (t *modTypeDef) Load(c *callContext) {
	if t.AsObject != nil && t.AsObject.Functions == nil {
		obj := c.GetObject(t.AsObject.Name)
		if obj != nil {
			t.AsObject = obj
		}
	}
}

// LoadObjects loads return and argument object types.
func (fn *modFunction) LoadObjects(c *callContext) {
	fn.ReturnType.Load(c)
	for _, arg := range fn.Args {
		arg.TypeDef.Load(c)
	}
}

// MakeCmd creates a Cobra command for the function.
func (fn *modFunction) MakeCmd(ctx context.Context, dag *dagger.Client, c *callContext) *cobra.Command {
	return &cobra.Command{
		Use:   cliName(fn.Name),
		Short: fn.Description,
		RunE: func(cmd *cobra.Command, args []string) error {
			c.StartCommand(cmd)

			fn.LoadObjects(c)
			fn.SetFlags(cmd)

			if err := cmd.ParseFlags(args); err != nil {
				return fmt.Errorf("parse sub-command flags: %w", err)
			}

			fn.ReturnType.AsObject.AddSubCommands(ctx, dag, c, cmd)

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

			if kind == dagger.Voidkind {
				return nil
			}

			// TODO: Handle different return types.
			cmd.Println(response)

			return nil
		},
	}
}

// SetFlags adds flags to the Cobra sub-command corresponding to this function..
func (fn *modFunction) SetFlags(cmd *cobra.Command) error {
	// Every command gets a --help flag.
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Show help for '%s'", cmd.Name()))
	cmd.Flags().SetInterspersed(false)

	for _, arg := range fn.Args {
		err := arg.SetFlag(cmd.Flags())
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

// Select adds the function selection to the query.
// The type can change if there's an extra selection for supported types.
func (fn *modFunction) Select(c *callContext, dag *dagger.Client, flags *pflag.FlagSet) (dagger.TypeDefKind, error) {
	kind := fn.ReturnType.Kind
	c.Select(fn.Name)

	// TODO: Check required flags.
	for _, arg := range fn.Args {
		arg := arg

		val, err := arg.GetValue(dag, flags)
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

func (r *modFunctionArg) SetFlag(flags *pflag.FlagSet) error {
	// TODO: Handle more types
	switch r.TypeDef.Kind {
	case dagger.Stringkind:
		flags.String(r.FlagName(), "", r.Description)
	case dagger.Integerkind:
		flags.Int(r.FlagName(), 0, r.Description)
	case dagger.Booleankind:
		flags.Bool(r.FlagName(), false, r.Description)
	case dagger.Objectkind:
		switch r.TypeDef.AsObject.Name {
		case Directory, File, Secret:
			flags.String(r.FlagName(), "", r.Description)
		default:
			return fmt.Errorf("unsupported object type %q", r.TypeDef.AsObject.Name)
		}
	default:
		return fmt.Errorf("unsupported type %q", r.TypeDef.Kind)
	}
	return nil
}

func (r *modFunctionArg) GetValue(dag *dagger.Client, flags *pflag.FlagSet) (any, error) {
	// TODO: Handle more types
	switch r.TypeDef.Kind {
	case dagger.Stringkind:
		return flags.GetString(r.FlagName())
	case dagger.Integerkind:
		return flags.GetInt(r.FlagName())
	case dagger.Booleankind:
		return flags.GetBool(r.FlagName())
	case dagger.Objectkind:
		val, err := flags.GetString(r.FlagName())
		if err != nil {
			return nil, err
		}
		switch r.TypeDef.AsObject.Name {
		case Directory:
			return dag.Host().Directory(val), nil
		case File:
			return dag.Host().File(val), nil
		case Secret:
			hash := sha256.Sum256([]byte(val))
			name := hex.EncodeToString(hash[:])
			return dag.SetSecret(name, val), nil
		default:
			return nil, fmt.Errorf("unsupported object type %q", r.TypeDef.AsObject.Name)
		}
	default:
		return nil, fmt.Errorf("unsupported type %q", r.TypeDef.Kind)
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
