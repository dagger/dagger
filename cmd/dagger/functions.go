package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
)

var (
	// jsonOutput is true if the `-j,--json` flag is used.
	jsonOutput bool

	// outputPath is the parsed value of the `--output` flag.
	outputPath string
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
	Socket       string = "Socket"
	Terminal     string = "Terminal"
)

var (
	skippedCmdsAnnotation = "help:skippedCmds"
	skippedOptsAnnotation = "help:skippedOpts"
)

var funcGroup = &cobra.Group{
	ID:    "functions",
	Title: "Functions",
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

	// Annotations are key/value pairs that can be used to identify or
	// group commands or set special options.
	Annotations map[string]string

	// DisableModuleLoad skips adding a flag for loading a user Dagger Module.
	DisableModuleLoad bool

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

	// selectedObject is not empty if the command chain end in an object.
	selectedObject functionProvider

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
			Annotations: fc.Annotations,
			GroupID:     moduleGroup.ID,

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
				if isPrintTraceLinkEnabled(c.Annotations) {
					c.SetContext(idtui.WithPrintTraceLink(c.Context(), true))
				}

				return withEngine(c.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) (rerr error) {
					fc.c = engineClient
					fc.q = querybuilder.Query().Client(engineClient.Dagger().GraphQLClient())

					// withEngine changes the context.
					c.SetContext(ctx)

					if err := fc.execute(c, a); err != nil {
						// We've already handled printing the error in `fc.execute`
						// because we want to show the usage for the right sub-command.
						// Returning ExitError here will prevent the error from being printed
						// twice on main().

						// Return the same ExecError exit code.
						var ex *dagger.ExecError
						if errors.As(err, &ex) {
							return ExitError{Code: ex.ExitCode}
						}
						return Fail
					}

					return nil
				})
			},
		}

		if fc.cmd.Annotations == nil {
			fc.cmd.Annotations = map[string]string{}
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

		fc.cmd.PersistentFlags().StringVarP(&outputPath, "output", "o", "", "Save the result to a local file or directory")

		fc.cmd.PersistentFlags().BoolVarP(&jsonOutput, "json", "j", false, "Present result as JSON")
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

	mod, err := initializeModule(ctx, fc.c.Dagger(), !fc.DisableModuleLoad)
	if err != nil {
		return err
	}
	fc.mod = mod

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

	// No args to the parent command
	if cmd == c {
		return fc.RunE(ctx, fc.mod.MainObject.AsObject.Constructor)(cmd, flags)
	}

	return cmd.RunE(cmd, flags)
}

// initializeModule loads the module's type definitions.
func initializeModule(ctx context.Context, dag *dagger.Client, loadModule bool) (rdef *moduleDef, rerr error) {
	def := &moduleDef{}

	ctx, span := Tracer().Start(ctx, "initialize")
	defer telemetry.End(span, func() error { return rerr })

	if loadModule {
		resolveCtx, resolveSpan := Tracer().Start(ctx, "resolving module ref", telemetry.Encapsulate())
		defer telemetry.End(resolveSpan, func() error { return rerr })
		modConf, err := getDefaultModuleConfiguration(resolveCtx, dag, true, true)
		if err != nil {
			return nil, fmt.Errorf("failed to get configured module: %w", err)
		}
		if !modConf.FullyInitialized() {
			return nil, fmt.Errorf("module at source dir %q doesn't exist or is invalid", modConf.LocalRootSourcePath)
		}
		resolveSpan.End()

		def.Source = modConf.Source
		mod := modConf.Source.AsModule().Initialize()

		serveCtx, serveSpan := Tracer().Start(ctx, "installing module", telemetry.Encapsulate())
		err = mod.Serve(serveCtx)
		telemetry.End(serveSpan, func() error { return err })
		if err != nil {
			return nil, err
		}

		ctx, loadSpan := Tracer().Start(ctx, "analyzing module", telemetry.Encapsulate())
		defer telemetry.End(loadSpan, func() error { return rerr })

		name, err := mod.Name(ctx)
		if err != nil {
			return nil, fmt.Errorf("get module name: %w", err)
		}
		def.Name = name
	}

	if err := def.loadTypeDefs(ctx, dag); err != nil {
		return nil, err
	}

	if def.MainObject == nil {
		return nil, fmt.Errorf("main object not found, check that your module's name and main object match")
	}

	return def, nil
}

// loadCommand finds the leaf command to run.
func (fc *FuncCommand) loadCommand(c *cobra.Command, a []string) (rcmd *cobra.Command, rargs []string, rerr error) {
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

		// The function name can be empty if it's the mocked constructor for
		// the root type (Query). That constructor has `fn.ReturnType` set to
		// the root type itself, but empty name so we can exclude a selection
		// in the query builder here.
		if fn.Name == "" {
			return nil
		}

		// Easier to add query builder selections as we traverse the command tree.
		return fc.selectFunc(fn, c)
	}
}

// addFlagsForFunction creates the flags for a function's arguments.
func (fc *FuncCommand) addFlagsForFunction(cmd *cobra.Command, fn *modFunction) error {
	var skipped []string

	var hasArgs bool

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
		hasArgs = true
	}

	if hasArgs {
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
		RunE: fc.RunE(ctx, fn),
	}

	newCmd.Flags().SetInterspersed(false)
	newCmd.SetContext(ctx)

	return newCmd
}

// selectFunc adds the function selection to the query.
func (fc *FuncCommand) selectFunc(fn *modFunction, cmd *cobra.Command) error {
	dag := fc.c.Dagger()

	fc.q = fc.q.Select(fn.Name)

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
			obj, err := v.Get(fc.ctx, dag, fc.mod.Source, arg)
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

		fc.q = fc.q.Arg(arg.Name, val)
	}

	return nil
}

// RunE is the final command in the function chain, where the API request is made.
func (fc *FuncCommand) RunE(ctx context.Context, fn *modFunction) func(*cobra.Command, []string) error {
	typeDef := fn.ReturnType
	return func(cmd *cobra.Command, args []string) error {
		t := typeDef
		if t.AsList != nil {
			t = t.AsList.ElementTypeDef
		}

		switch t.Kind {
		case dagger.TypeDefKindObjectKind, dagger.TypeDefKindInterfaceKind:
			origSel := fc.q
			obj := t.AsFunctionProvider()

			if err := fc.SelectObjectLeaf(ctx, obj); err != nil {
				return err
			}

			// If the selection didn't change, it means SelectObjectLeaf
			// didn't handle it, probably because there's no scalars to select
			// for printing. Rather than error, just return an empty
			// response without making an API request. At minimum, the
			// object's name will be returned.
			if origSel == fc.q {
				response := map[string]any{}
				return fc.HandleResponse(cmd, typeDef, response)
			}
		}

		// Silence usage from this point on as errors don't likely come
		// from wrong CLI usage.
		fc.showUsage = false

		var response any

		if err := fc.Request(ctx, &response); err != nil {
			return err
		}

		return fc.HandleResponse(cmd, typeDef, response)
	}
}

func (fc *FuncCommand) SelectObjectLeaf(ctx context.Context, obj functionProvider) error {
	fc.selectedObject = obj
	typeName := obj.ProviderName()

	// Convenience for sub-selecting `export` when `--output` is used
	// on a core type that supports it.
	// TODO: Replace with interface when possible.
	switch typeName {
	case Container, Directory, File:
		if outputPath != "" {
			fc.q = fc.q.Select("export").Arg("path", outputPath)
			if typeName == File {
				fc.q = fc.q.Arg("allowParentDirPath", true)
			}
			return nil
		}
	}

	switch typeName {
	case Container, Terminal:
		// There's no fields in `Container` that trigger container execution so
		// we use `sync` first to evaluate, and then load the new `Container`
		// from that response before continuing.
		// TODO: Use an interface when possible.
		var id string
		fc.q = fc.q.Select("sync")
		if err := fc.Request(ctx, &id); err != nil {
			return err
		}
		fc.q = fc.q.Root().Select(fmt.Sprintf("load%sFromID", typeName)).Arg("id", id)
	}

	fns := GetLeafFunctions(obj)
	names := make([]string, 0, len(fns))
	for _, f := range fns {
		names = append(names, f.Name)
	}
	if len(names) > 0 {
		fc.q = fc.q.SelectMultiple(names...)
	}

	return nil
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

func (fc *FuncCommand) HandleResponse(cmd *cobra.Command, modType *modTypeDef, response any) error {
	if modType.Kind == dagger.TypeDefKindVoidKind {
		return nil
	}

	// Handle the `export` convenience.
	switch modType.Name() {
	case Container, Directory, File:
		if outputPath != "" {
			respPath, ok := response.(string)
			if !ok {
				return fmt.Errorf("unexpected response %T: %+v", response, response)
			}
			cmd.PrintErrf("Saved to %q.\n", respPath)
			return nil
		}
	}

	var outputFormat string

	// Command chain ended in an object.
	if fc.selectedObject != nil {
		typeName := fc.selectedObject.ProviderName()

		if typeName == "Query" {
			typeName = "Client"
		}

		// Add the object's name so we always have something to show.
		payload := make(map[string]any)
		payload["_type"] = typeName

		if response == nil {
			response = payload
		} else {
			r, err := addPayload(response, payload)
			if err != nil {
				return err
			}
			response = r
		}

		// Use yaml when printing scalars because it's more human-readable
		// and handles lists and multiline strings well.
		if stdoutIsTTY {
			outputFormat = "yaml"
		} else {
			outputFormat = "json"
		}
	}

	// The --json flag has precedence over the autodetected format above.
	if jsonOutput {
		outputFormat = "json"
	}

	buf := new(bytes.Buffer)
	switch outputFormat {
	case "json":
		// disable HTML escaping to improve readability
		encoder := json.NewEncoder(buf)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "    ")
		if err := encoder.Encode(response); err != nil {
			return err
		}
	case "yaml":
		out, err := yaml.Marshal(response)
		if err != nil {
			return err
		}
		if _, err := buf.Write(out); err != nil {
			return err
		}
	case "":
		if err := printFunctionResult(buf, response); err != nil {
			return err
		}
	default:
		return fmt.Errorf("wrong output format %q", outputFormat)
	}

	if outputPath != "" {
		if err := writeOutputFile(outputPath, buf); err != nil {
			return fmt.Errorf("couldn't write output to file: %w", err)
		}
		path, err := filepath.Abs(outputPath)
		if err != nil {
			// don't fail because at this point the output has been saved successfully
			slog.Warn("Failed to get absolute path", "error", err)
			path = outputPath
		}
		cmd.PrintErrf("Saved output to %q.\n", path)
	}

	writer := cmd.OutOrStdout()
	buf.WriteTo(writer)

	// TODO(vito) right now when stdoutIsTTY we'll be printing to a Progrock
	// vertex, which currently adds its own linebreak (as well as all the
	// other UI clutter), so there's no point doing this. consider adding
	// back when we switch to printing "clean" output on exit.
	// if stdoutIsTTY && !strings.HasSuffix(buf.String(), "\n") {
	// 	fmt.Fprintln(writer, "⏎")
	// }

	return nil
}

// addPayload merges a map into a response from getting an object's values.
func addPayload(response any, payload map[string]any) (any, error) {
	switch t := response.(type) {
	case []any:
		r := make([]any, 0, len(t))
		for _, v := range t {
			p, err := addPayload(v, payload)
			if err != nil {
				return nil, err
			}
			r = append(r, p)
		}
		return r, nil
	case map[string]any:
		if len(t) == 0 {
			return payload, nil
		}
		r := make(map[string]any, len(t)+len(payload))
		for k, v := range t {
			r[k] = v
		}
		for k, v := range payload {
			r[k] = v
		}
		return r, nil
	default:
		return nil, fmt.Errorf("unexpected response %T for object values: %+v", response, response)
	}
}

// writeOutputFile writes the buffer to a file, creating the parent directories
// if needed.
func writeOutputFile(path string, buf *bytes.Buffer) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644) //nolint: gosec
}

func printFunctionResult(w io.Writer, r any) error {
	switch t := r.(type) {
	case []any:
		for _, v := range t {
			if err := printFunctionResult(w, v); err != nil {
				return err
			}
			fmt.Fprintln(w)
		}
		return nil
	case map[string]any:
		// NB: we're only interested in values because this is where we unwrap
		// things like {"container":{"from":{"withExec":{"stdout":"foo"}}}}.
		for _, v := range t {
			if err := printFunctionResult(w, v); err != nil {
				return err
			}
		}
		return nil
	case string:
		fmt.Fprint(w, t)
	default:
		fmt.Fprintf(w, "%+v", t)
	}
	return nil
}
