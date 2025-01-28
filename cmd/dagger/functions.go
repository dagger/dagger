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
							tty := !silent && (hasTTY && progress == "auto" || progress == "tty")
							// Only the pretty frontend prints the stderr of
							// the exec error in the final render
							if !tty && ex.Stdout != "" {
								c.Println("Stdout:")
								c.Println(ex.Stdout)
							}
							if !tty && ex.Stderr != "" {
								c.PrintErrln("Stderr:")
								c.PrintErrln(ex.Stderr)
							}
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

	var mod *moduleDef
	var err error
	if fc.DisableModuleLoad {
		mod, err = initializeCore(ctx, fc.c.Dagger())
	} else {
		mod, err = initializeDefaultModule(ctx, fc.c.Dagger())
	}
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

// loadCommand finds the leaf command to run.
func (fc *FuncCommand) loadCommand(c *cobra.Command, a []string) (rcmd *cobra.Command, rargs []string, rerr error) {
	ctx := c.Context()

	spanCtx, span := Tracer().Start(ctx, "parsing command line arguments", telemetry.Encapsulate())
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
	if fnProvider == nil {
		return
	}

	cmd.AddGroup(funcGroup)

	fns, skipped := GetSupportedFunctions(fnProvider)

	for _, fn := range fns {
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
		Short:                 fn.Short(),
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
	fc.q = fc.q.Select(fn.Name)

	missingFlags := []string{}
	for _, a := range fn.SupportedArgs() {
		flag, err := a.GetFlag(cmd.Flags())
		if err != nil {
			return err
		}

		if !flag.Changed {
			if a.IsRequired() {
				missingFlags = append(missingFlags, a.FlagName())
			}
			// don't send optional arguments that weren't set
			continue
		}

		v, err := a.GetFlagValue(fc.ctx, flag, fc.c.Dagger(), fc.mod)
		if err != nil {
			return err
		}

		fc.q = fc.q.Arg(a.Name, v)
	}

	if len(missingFlags) > 0 {
		return fmt.Errorf(`required flag(s) "%s" not set`, strings.Join(missingFlags, `", "`))
	}

	return nil
}

// RunE is the final command in the function chain, where the API request is made.
func (fc *FuncCommand) RunE(ctx context.Context, fn *modFunction) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		q, err := handleObjectLeaf(ctx, fc.q, fn.ReturnType)
		if err != nil {
			return err
		}

		// Silence usage from this point on as errors don't likely come
		// from wrong CLI usage.
		fc.showUsage = false

		o := cmd.OutOrStdout()
		e := cmd.ErrOrStderr()

		// It's possible that a chain ending in an object doesn't have anything
		// else to sub-select. In that case `q` will be nil to signal that we
		// just want to return the object's name, without making an API request.
		if q == nil {
			return handleResponse(fn.ReturnType, nil, o, e)
		}

		var response any

		if err := makeRequest(ctx, q, &response); err != nil {
			return err
		}

		return handleResponse(fn.ReturnType, response, o, e)
	}
}

func handleObjectLeaf(ctx context.Context, q *querybuilder.Selection, typeDef *modTypeDef) (*querybuilder.Selection, error) {
	obj := typeDef.AsFunctionProvider()
	if obj == nil {
		return q, nil
	}

	typeName := obj.ProviderName()
	origSel := q

	// Convenience for sub-selecting `export` when `--output` is used
	// on a core type that supports it.
	// TODO: Replace with interface when possible.
	if outputPath != "" {
		switch typeName {
		case Container, Directory, File:
			q = q.Select("export").Arg("path", outputPath)
			if typeName == File {
				q = q.Arg("allowParentDirPath", true)
			}
			return q, nil
		}
	}

	switch typeName {
	case Container, Terminal:
		// There's no fields in `Container` that trigger container execution so
		// we use `sync` first to evaluate, and then load the new `Container`
		// from that response before continuing.
		// TODO: Use an interface when possible.
		var id string
		q = q.Select("sync")
		if err := makeRequest(ctx, q, &id); err != nil {
			return nil, err
		}
		q = q.Root().Select(fmt.Sprintf("load%sFromID", typeName)).Arg("id", id)
	}

	fns := GetLeafFunctions(obj)
	names := make([]string, 0, len(fns))
	for _, f := range fns {
		names = append(names, f.Name)
	}
	if len(names) > 0 {
		q = q.SelectMultiple(names...)
	}

	// If the selection didn't change, it means selectObjectLeaf
	// didn't handle it, probably because there's no scalars to select
	// for printing. Rather than error, just return an empty
	// response without making an API request. At minimum, the
	// object's name will be returned.
	if q == origSel {
		return nil, nil
	}

	return q, nil
}

func makeRequest(ctx context.Context, q *querybuilder.Selection, response any) error {
	query, _ := q.Build(ctx)

	slog.Debug("executing query", "query", query)

	q = q.Bind(&response)

	if err := q.Execute(ctx); err != nil {
		return err
	}

	return nil
}

func handleResponse(returnType *modTypeDef, response any, o, e io.Writer) error {
	if returnType.Kind == dagger.TypeDefKindVoidKind {
		return nil
	}

	// Handle the `export` convenience, i.e, -o,--output flag.
	switch returnType.Name() {
	case Container, Directory, File:
		if outputPath != "" {
			respPath, ok := response.(string)
			if !ok {
				return fmt.Errorf("unexpected response %T: %+v", response, response)
			}
			fmt.Fprintf(e, "Saved to %q.\n", respPath)
			return nil
		}
	}

	// Command chain ended in an object, so add the _type field.
	if returnType.AsFunctionProvider() != nil {
		typeName := returnType.AsFunctionProvider().ProviderName()

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
	}

	buf := new(bytes.Buffer)
	frmt := outputFormat(returnType)
	if err := printResponse(buf, response, frmt); err != nil {
		return err
	}

	if outputPath != "" {
		if err := writeOutputFile(outputPath, buf); err != nil {
			return fmt.Errorf("couldn't write output to file: %w", err)
		}
		path, err := client.Abs(outputPath)
		if err != nil {
			// don't fail because at this point the output has been saved successfully
			slog.Warn("Failed to get absolute path", "error", err)
			path = outputPath
		}
		fmt.Fprintf(e, "Saved output to %q.\n", path)
	}

	_, err := buf.WriteTo(o)
	if stdoutIsTTY && !strings.HasSuffix(buf.String(), "\n") {
		fmt.Fprintln(o)
	}

	return err
}

func outputFormat(typeDef *modTypeDef) string {
	var outputFormat string

	if typeDef != nil && typeDef.AsFunctionProvider() != nil {
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

	return outputFormat
}

func printResponse(w io.Writer, response any, format string) error {
	switch format {
	case "json":
		// disable HTML escaping to improve readability
		encoder := json.NewEncoder(w)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "    ")
		return encoder.Encode(response)

	case "yaml":
		out, err := yaml.Marshal(response)
		if err != nil {
			return err
		}
		_, err = w.Write(out)
		return err

	case "":
		return printPlainResult(w, response)

	default:
		return fmt.Errorf("wrong output format %q", format)
	}
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

func printPlainResult(w io.Writer, r any) error {
	switch t := r.(type) {
	case []any:
		for _, v := range t {
			if err := printPlainResult(w, v); err != nil {
				return err
			}
			fmt.Fprintln(w)
		}
		return nil
	case map[string]any:
		// NB: we're only interested in values because this is where we unwrap
		// things like {"container":{"from":{"withExec":{"stdout":"foo"}}}}.
		for _, v := range t {
			if err := printPlainResult(w, v); err != nil {
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
