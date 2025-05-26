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
	"sort"
	"strings"

	"github.com/opencontainers/go-digest"
	"github.com/sourcegraph/conc/pool"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/engine/slog"
)

var (
	// jsonOutput is true if the `-j,--json` flag is used.
	jsonOutput bool

	// outputPath is the parsed value of the `--output` flag.
	outputPath string
)

const (
	Directory     string = "Directory"
	Container     string = "Container"
	File          string = "File"
	Secret        string = "Secret"
	Service       string = "Service"
	PortForward   string = "PortForward"
	CacheVolume   string = "CacheVolume"
	ModuleSource  string = "ModuleSource"
	Module        string = "Module"
	Platform      string = "Platform"
	BuildArg      string = "BuildArg"
	Socket        string = "Socket"
	GitRepository string = "GitRepository"
	GitRef        string = "GitRef"
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

	q   *querybuilder.Selection
	c   *client.Client
	ctx context.Context
}

func (fc *FuncCommand) Command() *cobra.Command {
	if fc.cmd == nil {
		fc.cmd = &cobra.Command{
			Use:                   fc.Name,
			Aliases:               fc.Aliases,
			Short:                 fc.Short,
			Long:                  fc.Long,
			Example:               fc.Example,
			Annotations:           fc.Annotations,
			Args:                  cobra.ArbitraryArgs,
			GroupID:               moduleGroup.ID,
			DisableFlagsInUseLine: true,

			// We need to handle flag parsing ourselves to avoid --help short
			// circuiting before we have a chance to load the module.
			DisableFlagParsing: true,

			// Parse flags in PreRunE because it still runs before flag
			// validation but is already past the --help short circuit.
			PreRunE: func(c *cobra.Command, a []string) error {
				c.DisableFlagParsing = false
				c.FParseErrWhitelist.UnknownFlags = true

				if err := c.ParseFlags(a); err != nil {
					return c.FlagErrorFunc()(c, err)
				}

				// Don't short circuit yet. Let's load the module first.
				helpVal, _ := c.Flags().GetBool("help")
				fc.needsHelp = helpVal

				return nil
			},
			RunE: func(c *cobra.Command, a []string) error {
				if isPrintTraceLinkEnabled(c.Annotations) {
					c.SetContext(idtui.WithPrintTraceLink(c.Context(), true))
				}
				return withEngine(c.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) (rerr error) {
					// withEngine changes the context.
					c.SetContext(ctx)

					// Flag parsing can be explicitly requested (with `--`)
					// in order to cleanly separate `dagger call` flags and
					// function argument flags and avoid any conflicts.
					args := c.Flags().Args()
					dash := c.Flags().ArgsLenAtDash() // -1 by default

					// This is an unfortunate hack to avoid the breaking change
					// of requiring `--` to cleanly separate `dagger call` flags
					// and function argument flags, which is necessary to avoid
					// conflicts between these flag sets. So this is an attempt
					// at backwards compatibility but at risk of not being as
					// robust as desired. In any case, the simple solution
					// to get out of a pothole is to put all CLI flags behind
					// `--`.
					if dash < 0 {
						// When pflags parses the list of arguments, it excludes
						// unknown flags and their values. We actually want the
						// opposite behavior: remove all known/parsed flags
						// because they've already been parsed and we need to
						// get the function specific flags for the children
						// traversal parsing.
						args = stripKnownArgs(a, c.Flags())
					} else if fc.needsHelp {
						// `--help` before `--`, so print help for `dagger call`
						// and ignore what comes after, including loading the module.
						// If `dagger call -- --help` (after `--`) then needsHelp
						// will be false and the root command built from the
						// constructor will parse and print the help instead.
						c.Help()
						return nil
					}

					fc.c = engineClient
					fc.q = querybuilder.Query().Client(engineClient.Dagger().GraphQLClient())

					if err := fc.execute(ctx, c, args); err != nil {
						// We've already handled printing the error in `fc.execute`.
						// Returning ExitError here will prevent the error from being printed
						// twice on main().
						var ex *dagger.ExecError
						if errors.As(err, &ex) {
							tty := !silent && (hasTTY && progress == "auto" || progress == "tty")
							// Only the pretty frontend prints the stderr of
							// the exec error in the final render
							if !tty && ex.Stdout != "" {
								c.PrintErrln("Stdout:")
								c.PrintErrln(ex.Stdout)
							}
							if !tty && ex.Stderr != "" {
								c.PrintErrln("Stderr:")
								c.PrintErrln(ex.Stderr)
							}
							return ExitError{Code: ex.ExitCode, Original: err}
						}
						return ExitError{Code: 1, Original: err}
					}

					return nil
				})
			},
		}

		fc.cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Save the result to a local file or directory")
		fc.cmd.Flags().BoolVarP(&jsonOutput, "json", "j", false, "Present result as JSON")
	}
	return fc.cmd
}

// execute runs the main logic for the top level command's RunE function.
func (fc *FuncCommand) execute(ctx context.Context, c *cobra.Command, a []string) (rerr error) {
	var cmd *cobra.Command
	defer func() {
		if cmd == nil { // errored during loading
			cmd = c
		}
		if ctx.Err() != nil {
			cmd.PrintErrln("Canceled.")
		} else if rerr != nil {
			if fc.needsHelp {
				// Explicitly show the help here while still returning the error.
				// This handles the case of `dagger call --help` run on a broken module; in that case
				// we want to error out since we can't actually load the module and show all subcommands
				// and flags in the help output, but we still want to show the user *something*.
				cmd.Help()
				cmd.Println()
			}

			cmd.PrintErrln(cmd.ErrPrefix(), rerr.Error())

			tty := !silent && (hasTTY && progress == "auto" || progress == "tty")
			// Only the pretty frontend prints the stderr of
			// the exec error in the final render
			if !tty {
				var ex *dagger.ExecError
				if errors.As(rerr, &ex) {
					if ex.Stdout != "" {
						c.Println("Stdout:")
						c.Println(ex.Stdout)
					}
					if ex.Stderr != "" {
						c.PrintErrln("Stderr:")
						c.PrintErrln(ex.Stderr)
					}
				}
			}

			if fc.showUsage && !fc.needsHelp {
				cmd.PrintErrf("Run '%v --help' for usage.\n", cmd.CommandPath())
			}
		}
	}()

	var mod *moduleDef
	var err error
	if fc.DisableModuleLoad || moduleNoURL {
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

	// Call PersistentPreRunE on all parent commands, from the root to the final
	// command. Used for query builder selections.
	cobra.EnableTraverseRunHooks = true

	// TODO: if needsHelp, add dagger call local flags so it shows in the help output
	cmd, err = fc.buildCommandTree(ctx, c, a)
	if err != nil {
		return err
	}

	// Separate flag parsing from making the final request. In RunE we end
	// this span and go back to using the root context.
	fc.ctx = ctx
	ctx, span := Tracer().Start(ctx, "parsing command line arguments")
	defer telemetry.End(span, func() error { return rerr })

	cmd, err = cmd.ExecuteContextC(ctx)

	// Cobra usually handles this but only on the target command, not when
	// parsed on a parent command during children traversal. In that case,
	// the returned cmd will be of the parent where --help was found.
	if errors.Is(err, pflag.ErrHelp) {
		cmd.Help()
		return nil
	}

	return err
}

// buildCommandTree builds the command tree based on the list of positional arguments provided
func (fc *FuncCommand) buildCommandTree(ctx context.Context, c *cobra.Command, a []string) (rcmd *cobra.Command, rerr error) {
	if debug {
		spanCtx, span := Tracer().Start(ctx, "building command tree",
			telemetry.Internal(),
			telemetry.Encapsulate(),
		)
		defer telemetry.End(span, func() error { return rerr })
		ctx = spanCtx
	}

	// In order to avoid conflicts between function arguments and CLI flags
	// we create a new root command to build the tree off of. This way there's
	// no inherited flags to worry about and can be cleanly separated with `--`
	// since we basically just pass whatever is on the right as the list of
	// arguments here.
	root := fc.makeSubCmd(ctx, fc.mod.MainObject.AsObject.Constructor)
	root.Use = c.Name()
	root.Long = fc.mod.Description // TODO: add some fallback descriptions
	root.SetUsageTemplate(usageTemplate)
	root.SetOut(c.OutOrStdout())
	root.SetErr(c.ErrOrStderr())
	root.SetArgs(a)

	// Override the display name so it's shown as `dagger call foo` in --help,
	// not just `call foo`.
	root.Annotations[cobra.CommandDisplayNameAnnotation] = c.CommandPath()

	// Parse the flags from parent commands while looking for the last command
	// in the chain to execute.
	root.TraverseChildren = true

	// If --help is set on a parent command (e.g., `dagger call -- --help foo`)
	// it's possible that it short-circuits on the children traversal phase
	// with the `pflag: help requested` error, which is a problem because
	// cobra only handles it on the last command's own flag parsing so we
	// need to handle printing the error ourselves.
	root.SilenceErrors = true
	root.SilenceUsage = true

	// Disable help and completions subcommands which are created by cobra
	// automatically. This keeps the --help screen clean and avoids function
	// name conflicts.
	root.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}
	root.SetHelpCommand(&cobra.Command{Hidden: true, GroupID: funcGroup.ID})

	// Allow using flags with the name that was reported by the SDK.
	// This avoids confusion as users are editing a module and trying
	// to test its functions. For example, if a function argument is
	// `dockerConfig` in code, the user can type `--dockerConfig` or even
	// `--DockerConfig` as this normalization function rewrites to the
	// equivalent `--docker-config` in kebab-case.
	root.SetGlobalNormalizationFunc(func(f *pflag.FlagSet, name string) pflag.NormalizedName {
		return pflag.NormalizedName(cliName(name))
	})

	_, _, err := fc.traverse(root, a)
	return root, err
}

// traverse adds flags and subcommands, then recursively finds the next one
// based on remaining arguments.
func (fc *FuncCommand) traverse(c *cobra.Command, a []string) (rcmd *cobra.Command, rargs []string, rerr error) {
	// We rely on cobra's Find to do the heavy lifting but we need to attach
	// a function with a command so we use PreRunE to wrap the function when
	// the command is created and remove it afterwards.
	if err := c.PreRunE(c, a); err != nil {
		return c, a, err
	}

	// Don't want this to execute later. It's just a workaround to facilitate
	// building the command tree.
	c.PreRunE = nil

	cmd, args, err := c.Find(a)
	if err != nil {
		return cmd, args, err
	}

	// Leaf command
	if cmd == c {
		return cmd, args, nil
	}

	return fc.traverse(cmd, args)
}

// PreRunE adds the necessary flags and next set of sub-commands to a command.
//
// This is a workaround in order to attach a function definition to a command,
// which allows leveraging cobra's Find() recursively to find all the functions
// in the chain while skipping over flags. Flags are thus not parsed during
// build and we can cleanly separate a command tree building phase, from a
// flag parsing phase.
//
// It's only used while building the command tree and deleted before execution.
// There's nothing particular about being "PreRunE" here, we're simply limited
// by cobra.Command's API and "pre run" is the most intuitive name for this.
func (fc *FuncCommand) PreRunE(ctx context.Context, fn *modFunction) func(*cobra.Command, []string) error {
	return func(c *cobra.Command, a []string) (rerr error) {
		cmdCtx := ctx

		if debug {
			spanCtx, span := Tracer().Start(ctx, c.CommandPath())
			defer telemetry.End(span, func() error { return rerr })
			cmdCtx = spanCtx
		}

		if err := fc.addFlagsForFunction(cmdCtx, c, fn); err != nil {
			return err
		}

		fc.mod.LoadTypeDef(fn.ReturnType)

		fnProvider := fn.ReturnType.AsFunctionProvider()
		if fnProvider == nil {
			return nil
		}

		fns, skipped := GetSupportedFunctions(fnProvider)

		if !fnProvider.IsCore() && len(skipped) > 0 {
			slog := slog.SpanLogger(cmdCtx, InstrumentationLibrary)
			slog.Debug("skipping unsupported functions", "functions", skipped)
		}

		// Create subcommands with parent context to avoid nesting
		fc.addSubCommands(ctx, c, fns)

		return nil
	}
}

// addFlagsForFunction creates the flags for a function's arguments.
func (fc *FuncCommand) addFlagsForFunction(ctx context.Context, cmd *cobra.Command, fn *modFunction) error {
	slog := slog.SpanLogger(ctx, InstrumentationLibrary)

	for _, arg := range fn.Args {
		fc.mod.LoadTypeDef(arg.TypeDef)

		if err := arg.AddFlag(cmd.Flags()); err != nil {
			var e *UnsupportedFlagError
			if errors.As(err, &e) {
				slog.Debug("skipped unsupported flag", "name", e.Name, "type", e.Type)
				continue
			}
			return err
		}

		flag, err := arg.GetFlag(cmd.Flags())
		if err != nil {
			return err
		}

		slog.Debug("added flag", "name", flag.Name, "type", flag.Value.Type())

		if arg.IsRequired() {
			cmd.MarkFlagRequired(arg.FlagName())
		}

		cmd.Flags().SetAnnotation(
			arg.FlagName(),
			"help:group",
			[]string{"Arguments"},
		)
	}

	if cmd.Flags().HasAvailableFlags() {
		cmd.Use += " [arguments]"
	}

	return nil
}

// addSubCommands creates sub-commands for the functions in an object or interface type definition.
func (fc *FuncCommand) addSubCommands(ctx context.Context, cmd *cobra.Command, fns []*modFunction) {
	cmd.AddGroup(funcGroup)

	for _, fn := range fns {
		subCmd := fc.makeSubCmd(ctx, fn)
		cmd.AddCommand(subCmd)
	}

	if cmd.HasAvailableSubCommands() {
		cmd.Use += " <function>"
	}
}

// makeSubCmd creates a sub-command for a function definition.
func (fc *FuncCommand) makeSubCmd(ctx context.Context, fn *modFunction) *cobra.Command {
	newCmd := &cobra.Command{
		Use:         cliName(fn.Name),
		Short:       fn.Short(),
		Long:        fn.Description,
		GroupID:     funcGroup.ID,
		Annotations: map[string]string{},
		// Args are reserved for commands. These should accept only flags.
		Args: cobra.NoArgs,
		// We use [arguments] instead of [flags] in the usage line.
		DisableFlagsInUseLine: true,
		// PreRunE is used as a workaround and called directly by dagger when
		// building the command tree. It's not called by cobra's Execute as usual.
		PreRunE: fc.PreRunE(ctx, fn),
		// RunE is only executed by the final/leaf command. It's what builds
		// the final query and prints the response.
		RunE: fc.RunE(fn),
	}

	// The function name can be empty if it's the mocked constructor for
	// the root type (Query). That constructor has `fn.ReturnType` set to
	// the root type itself, but empty name so we can exclude a selection
	// in the query builder here.
	if fn.Name != "" {
		// PersistentPreRunE is called after flag parsing and validation, and
		// before RunE. It's executed for all parent commands until the final
		// one, and is used to make all the query builder selections in the
		// right order.
		newCmd.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
			// NB: The root command only passes the context to the target command,
			// not parent commands. It's unusual that unlike other functions in
			// cobra.Command, Context() doesn't call parent.Context() until the
			// root if it's not defined.
			ctx := newCmd.Root().Context()
			return fc.selectFunc(ctx, fn, newCmd)
		}
	}

	// Always stop parsing at the next positional argument.
	newCmd.Flags().SetInterspersed(false)

	return newCmd
}

// selectFunc adds the function selection to the query builder.
func (fc *FuncCommand) selectFunc(ctx context.Context, fn *modFunction, cmd *cobra.Command) error {
	slog := slog.SpanLogger(ctx, InstrumentationLibrary)
	slog.Debug("selecting function", "function", fn.CmdName())

	fc.q = fc.q.Select(fn.Name)

	missingFlags := []string{}

	type flagResult struct {
		idx   int
		flag  string
		value any
	}

	p := pool.NewWithResults[flagResult]().WithErrors()

	for i, a := range fn.SupportedArgs() {
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

		p.Go(func() (flagResult, error) {
			v, err := a.GetFlagValue(ctx, flag, fc.c.Dagger(), fc.mod)
			if err != nil {
				return flagResult{}, err
			}
			return flagResult{i, a.Name, v}, nil
		})
	}

	vals, err := p.Wait()
	if err != nil {
		return err
	}

	sort.Slice(vals, func(i, j int) bool {
		return vals[i].idx < vals[j].idx
	})

	for _, flag := range vals {
		fc.q = fc.q.Arg(flag.flag, flag.value)
	}

	if len(missingFlags) > 0 {
		return fmt.Errorf(`required flag(s) "%s" not set`, strings.Join(missingFlags, `", "`))
	}

	return nil
}

// RunE is the final command in the function chain, where the API request is made.
func (fc *FuncCommand) RunE(fn *modFunction) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) (rerr error) {
		// End the flag parsing span
		span := trace.SpanFromContext(cmd.Context())
		span.End()

		// Recover the root span's context
		ctx := fc.ctx
		cmd.SetContext(ctx)

		// Silence usage from this point on as errors don't likely come
		// from wrong CLI usage.
		fc.showUsage = false

		q := handleObjectLeaf(fc.q, fn.ReturnType)

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

func handleObjectLeaf(q *querybuilder.Selection, typeDef *modTypeDef) *querybuilder.Selection {
	obj := typeDef.AsFunctionProvider()
	if obj == nil {
		return q
	}

	// Use duck typing to detect supported functions.
	var hasSync bool
	var hasExport bool
	var hasExportAllowParentDirPath bool
	fns := obj.GetFunctions()
	for _, fn := range fns {
		if fn.Name == "sync" && len(fn.SupportedArgs()) == 0 {
			hasSync = true
		}
		if fn.Name == "export" {
			for _, a := range fn.SupportedArgs() {
				if a.Name == "path" {
					hasExport = true
				}
				if a.Name == "allowParentDirPath" {
					hasExportAllowParentDirPath = true
				}
			}
		}
	}

	// Convenience for sub-selecting `export` when `--output` is used
	// on a core type that supports it.
	// TODO: Replace with interface when possible.
	if outputPath != "" && hasExport {
		q = q.Select("export").Arg("path", outputPath)
		if hasExportAllowParentDirPath {
			q = q.Arg("allowParentDirPath", true)
		}
		return q
	}

	// TODO: Replace with interface when possible.
	if hasSync {
		return q.SelectWithAlias("id", "sync")
	}

	return q.Select("id")
}

func makeRequest(ctx context.Context, q *querybuilder.Selection, response any) error {
	query, _ := q.Build(ctx)

	slog := slog.SpanLogger(ctx, InstrumentationLibrary)
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
		return printID(o, response, returnType)
	}

	buf := new(bytes.Buffer)
	if err := printResponse(buf, response, returnType); err != nil {
		return err
	}

	if outputPath != "" {
		if err := writeOutputFile(outputPath, buf); err != nil {
			return fmt.Errorf("couldn't write output to file: %w", err)
		}
		path, err := pathutil.Abs(outputPath)
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

func printID(w io.Writer, response any, typeDef *modTypeDef) error {
	switch {
	case typeDef.AsList != nil:
		for _, v := range response.([]any) {
			fmt.Fprint(w, "- ")
			if err := printID(w, v, typeDef.AsList.ElementTypeDef); err != nil {
				return err
			}
		}
		return nil
	case typeDef.AsObject != nil:
		switch v := response.(type) {
		case string:
			return printEncodedID(w, v)
		case map[string]any:
			encodedID, ok := v["id"].(string)
			if !ok {
				return fmt.Errorf("printID: no ID found in object: %+v", v)
			}
			return printEncodedID(w, encodedID)
		default:
			return fmt.Errorf("printID: unexpected type for object: %T", v)
		}
	default:
		return fmt.Errorf("printID: unexpected type: %s", typeDef.String())
	}
}

func printEncodedID(w io.Writer, encodedID string) error {
	var id call.ID
	if err := id.Decode(encodedID); err != nil {
		return fmt.Errorf("failed to decode ID: %w", err)
	}
	_, err := fmt.Fprintf(w, "%s@%s\n", id.Type().ToAST().Name(), id.Digest())
	return err
}

func idDigest(encodedID string) (digest.Digest, error) {
	var id call.ID
	if err := id.Decode(encodedID); err != nil {
		return "", fmt.Errorf("failed to decode ID: %w", err)
	}
	return id.Digest(), nil
}

func printResponse(w io.Writer, response any, typeDef *modTypeDef) error {
	if jsonOutput {
		// disable HTML escaping to improve readability
		encoder := json.NewEncoder(w)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "    ")
		return encoder.Encode(response)
	}

	if typeDef != nil && typeDef.AsFunctionProvider() != nil {
		return printID(w, response, typeDef)
	}

	return printPlainResult(w, response)
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
