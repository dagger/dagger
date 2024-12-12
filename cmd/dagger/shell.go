package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"github.com/adrg/xdg"
	"github.com/chzyer/readline"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/mattn/go-isatty"
	"github.com/muesli/reflow/indent"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.opentelemetry.io/otel/codes"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

const (
	// shellStatePrefix is the prefix that identifies a shell state in input/output
	shellStatePrefix = "DSH:"
	helpIndent       = uint(2)
	shellHandlerExit = 200

	shellStdlibCmdName = ".stdlib"
	shellDepsCmdName   = ".deps"
	shellCoreCmdName   = ".core"

	// We need a prompt that conveys the unique nature of the Dagger shell. Per gpt4:
	// The ⋈ symbol, known as the bowtie, has deep roots in relational databases and set theory,
	// where it denotes a join operation. This makes it especially fitting for a DAG environment,
	// as it suggests the idea of dependencies, intersections, and points where separate paths
	// or data sets come together.
	shellPrompt = "⋈"
)

var coreGroup = &cobra.Group{
	ID:    "core",
	Title: "Dagger Core Commands",
}

var shellGroups = []*cobra.Group{
	moduleGroup,
	coreGroup,
	cloudGroup,
	{
		ID:    "",
		Title: "Additional Commands",
	},
}

// shellCode is the code to be executed in the shell command
var (
	shellCode         string
	shellNoLoadModule bool
)

func init() {
	shellCmd.Flags().StringVarP(&shellCode, "code", "c", "", "Command to be executed")
	shellCmd.Flags().BoolVar(&shellNoLoadModule, "no-mod", false, "Don't load module during shell startup (mutually exclusive with --mod)")
	shellCmd.MarkFlagsMutuallyExclusive("mod", "no-mod")
}

var shellCmd = &cobra.Command{
	Use:   "shell [options] [file...]",
	Short: "Run an interactive dagger shell",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SetContext(idtui.WithPrintTraceLink(cmd.Context(), true))
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			handler := &shellCallHandler{
				dag:    dag,
				stdin:  cmd.InOrStdin(),
				stdout: cmd.OutOrStdout(),
				stderr: cmd.ErrOrStderr(),
				debug:  debug,
			}
			return handler.RunAll(ctx, args)
		})
	},
	Hidden: true,
	Annotations: map[string]string{
		"experimental": "true",
	},
}

type shellCallHandler struct {
	dag    *dagger.Client
	runner *interp.Runner

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	// modRef is a key from modDefs, to set the corresponding module as the default
	// when no state is present, or when the state's ModRef is empty
	modRef string

	// modDefs has the cached module definitions, after loading, and keyed by
	// module reference as inputed by the user
	modDefs map[string]*moduleDef

	// switch to Frontend.Background for rendering output while the TUI is
	// running when in interactive mode
	tui bool

	// stdoutBuf is used to capture the final stdout that the runner produces
	stdoutBuf *bytes.Buffer

	// stderrBuf is used to capture the final stderr that the runner produces
	stderrBuf *bytes.Buffer

	// debug writes to the handler context's stderr what the arguments, input,
	// and output are for each command that the exec handler processes
	debug bool

	// builtins is the list of Dagger Shell builtin commands
	builtins []*ShellCommand

	// stdlib is the list of standard library commands
	stdlib []*ShellCommand
}

// RunAll is the entry point for the shell command
//
// It creates the runner and dispatches the execution to different modes:
// - Interactive: when no arguments are provided
// - File: when a file path is provided as an argument
// - Code: when code is passed inline using the `-c,--code` flag or via stdin
func (h *shellCallHandler) RunAll(ctx context.Context, args []string) error {
	h.tui = !silent && (hasTTY && progress == "auto" || progress == "tty")

	h.stdoutBuf = new(bytes.Buffer)
	h.stderrBuf = new(bytes.Buffer)

	r, err := interp.New(
		interp.StdIO(nil, h.stdoutBuf, h.stderrBuf),
		interp.Params("-e", "-u", "-o", "pipefail"),

		// The "Interactive" option is useful even when not running dagger shell
		// in interactive mode. It expands aliases and maybe more in the future.
		interp.Interactive(true),

		// Interpreter builtins run before the exec handlers, but CallHandler
		// runs before any of that, so we can use it to change the arguments
		// slightly in order to resolve naming conflicts. For example, "echo"
		// is an interpreter builtin but can also be a Dagger function.
		interp.CallHandler(func(ctx context.Context, args []string) ([]string, error) {
			// When there's a Dagger function with a name that conflicts
			// with an interpreter builtin, the Dagger function is favored.
			// To force the builtin to execute instead, prefix the command
			// with "..". For example: "container | from $(..echo alpine)".
			if strings.HasPrefix(args[0], "..") {
				args[0] = strings.TrimPrefix(args[0], "..")
				return args, nil
			}
			// If the command is an interpreter builtin, bypass the interpreter
			// builtins to ensure the exec handler is executed.
			if isInterpBuiltin(args[0]) {
				return append([]string{".dag"}, args...), nil
			}
			return args, nil
		}),
		interp.ExecHandlers(h.Exec),
	)
	if err != nil {
		return err
	}
	h.runner = r
	h.modDefs = make(map[string]*moduleDef)

	var def *moduleDef
	var ref string
	if shellNoLoadModule {
		def, err = initializeCore(ctx, h.dag)
	} else {
		def, ref, err = maybeInitializeDefaultModule(ctx, h.dag)
	}
	if err != nil {
		return err
	}
	h.modRef = ref
	h.modDefs[ref] = def
	h.registerCommands()

	// Example: `dagger shell -c 'container | workdir'`
	if shellCode != "" {
		return h.run(ctx, strings.NewReader(shellCode), "")
	}

	// Use stdin only when no file paths are provided
	if len(args) == 0 {
		// Example: `dagger shell`
		if isatty.IsTerminal(os.Stdin.Fd()) {
			return h.runInteractive(ctx)
		}
		// Example: `echo 'container | workdir' | dagger shell`
		return h.run(ctx, os.Stdin, "-")
	}

	// Example: `dagger shell job1.dsh job2.dsh`
	for _, path := range args {
		if err := h.runPath(ctx, path); err != nil {
			return err
		}
	}

	return nil
}

func isInterpBuiltin(name string) bool {
	switch name {
	case "true", ":", "false", "exit", "set", "shift", "unset",
		"echo", "printf", "break", "continue", "pwd", "cd",
		"wait", "builtin", "trap", "type", "source", ".", "command",
		"dirs", "pushd", "popd", "umask", "alias", "unalias",
		"fg", "bg", "getopts", "eval", "test", "[", "exec",
		"return", "read", "mapfile", "readarray", "shopt":
		return true
	}
	return false
}

func litWord(s string) *syntax.Word {
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: s}}}
}

// run parses code and executes the interpreter's Runner
func (h *shellCallHandler) run(ctx context.Context, reader io.Reader, name string) error {
	file, err := parseShell(reader, name)
	if err != nil {
		return err
	}

	h.stdoutBuf.Reset()
	h.stderrBuf.Reset()

	// Make sure every run flushes any stderr output.
	defer func() {
		h.withTerminal(func(_ io.Reader, stdout, stderr io.Writer) error {
			// We could also have missing output in stdoutBuf, but probably
			// for propagating a ShellState.Error. Just ignore those.
			if h.stderrBuf.Len() > 0 {
				fmt.Fprintln(stderr, h.stderrBuf.String())
				h.stderrBuf.Reset()
			}
			return nil
		})
	}()

	var handlerError bool

	err = h.runner.Run(ctx, file)
	if exit, ok := interp.IsExitStatus(err); ok {
		handlerError = int(exit) == shellHandlerExit
		if !handlerError {
			return ExitError{Code: int(exit)}
		}
		err = nil
	}
	if err != nil {
		return err
	}

	resp, err := h.Result(ctx, h.stdoutBuf, true, handleObjectLeaf)
	if err != nil || resp == nil {
		return err
	}

	return h.withTerminal(func(_ io.Reader, stdout, _ io.Writer) error {
		fmt.Fprint(stdout, resp)
		if sval, ok := resp.(string); ok && stdoutIsTTY && !strings.HasSuffix(sval, "\n") {
			fmt.Fprintln(stdout)
		}
		return nil
	})
}

func parseShell(reader io.Reader, name string) (*syntax.File, error) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(reader, name)
	if err != nil {
		return nil, err
	}

	syntax.Walk(file, func(node syntax.Node) bool {
		if node, ok := node.(*syntax.CmdSubst); ok {
			// Rewrite command substitutions from $(foo; bar) to $(exec <&-; foo; bar)
			// so that all the original commands run with a closed (nil) standard input.
			node.Stmts = append([]*syntax.Stmt{{
				Cmd: &syntax.CallExpr{Args: []*syntax.Word{litWord("..exec")}},
				Redirs: []*syntax.Redirect{{
					Op:   syntax.DplIn,
					Word: litWord("-"),
				}},
			}}, node.Stmts...)
		}
		return true
	})
	return file, nil
}

// runPath executes code from a file
func (h *shellCallHandler) runPath(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h.runner.Reset()
	return h.run(ctx, f, path)
}

// runInteractive executes the runner on a REPL (Read-Eval-Print Loop)
func (h *shellCallHandler) runInteractive(ctx context.Context) error {
	h.withTerminal(func(_ io.Reader, _, stderr io.Writer) error {
		fmt.Fprintln(stderr, `Dagger interactive shell. Type ".help" for more information. Press Ctrl+D to exit.`)
		return nil
	})

	var rl *readline.Instance
	defer func() {
		if rl != nil {
			rl.Close()
		}
	}()

	var runErr error
	for {
		Frontend.SetPrimary(dagui.SpanID{})
		Frontend.Opts().CustomExit = func() {}
		fg := termenv.ANSIGreen

		if runErr != nil {
			fg = termenv.ANSIRed

			h.withTerminal(func(_ io.Reader, _, stderr io.Writer) error {
				out := idtui.NewOutput(stderr)
				fmt.Fprintln(stderr, out.String("Error:", runErr.Error()).Foreground(fg))
				return nil
			})

			// Reset runError for next command
			runErr = nil
		}

		var line string

		err := h.withTerminal(func(stdin io.Reader, stdout, stderr io.Writer) error {
			var err error

			prompt := h.Prompt(idtui.NewOutput(stdout), fg)

			if rl == nil {
				cfg, err := h.loadReadlineConfig(prompt)
				if err != nil {
					return err
				}
				// NOTE: this relies on multiple calls to withTerminal
				// returning the same readers/writers each time
				cfg.Stdin = io.NopCloser(stdin)
				cfg.Stdout = stdout
				cfg.Stderr = stderr

				rl, err = readline.NewEx(cfg)
				if err != nil {
					return err
				}
			} else {
				rl.SetPrompt(prompt)
			}
			line, err = rl.Readline()
			return err
		})
		if err != nil {
			// EOF or Ctrl+D to exit
			if errors.Is(err, io.EOF) {
				Frontend.Opts().Verbosity = 0
				Frontend.Opts().CustomExit = nil
				break
			}
			// Ctrl+C should move to the next line
			if errors.Is(err, readline.ErrInterrupt) {
				continue
			}
			return err
		}

		if strings.TrimSpace(line) == "" {
			continue
		}

		ctx, span := Tracer().Start(ctx, line)
		ctx, cancel := context.WithCancel(ctx)
		Frontend.SetPrimary(dagui.SpanID{SpanID: span.SpanContext().SpanID()})
		Frontend.Opts().CustomExit = cancel
		runErr = h.run(ctx, strings.NewReader(line), "")
		if runErr != nil {
			span.SetStatus(codes.Error, runErr.Error())
		}
		span.End()
	}

	return nil
}

func (h *shellCallHandler) loadReadlineConfig(prompt string) (*readline.Config, error) {
	dataRoot := filepath.Join(xdg.DataHome, "dagger")
	err := os.MkdirAll(dataRoot, 0o700)
	if err != nil {
		return nil, err
	}

	return &readline.Config{
		Prompt:       prompt,
		HistoryFile:  filepath.Join(dataRoot, "histfile"),
		HistoryLimit: 1000,
		AutoComplete: &shellAutoComplete{h},
	}, nil
}

func (h *shellCallHandler) Prompt(out *termenv.Output, fg termenv.Color) string {
	sb := new(strings.Builder)

	if def, _ := h.GetModuleDef(nil); def != nil {
		sb.WriteString(out.String(def.ModRef).Bold().Foreground(termenv.ANSICyan).String())
		sb.WriteString(" ")
	}

	sb.WriteString(out.String(shellPrompt).Bold().Foreground(fg).String())
	sb.WriteString(" ")

	return sb.String()
}

// withTerminal handles using stdin, stdout, and stderr when the TUI is runnin
func (h *shellCallHandler) withTerminal(fn func(stdin io.Reader, stdout, stderr io.Writer) error) error {
	if h.tui {
		return Frontend.Background(&terminalSession{
			fn: func(stdin io.Reader, stdout, stderr io.Writer) error {
				return fn(stdin, stdout, stderr)
			},
		}, false)
	}
	return fn(h.stdin, h.stdout, h.stderr)
}

// Exec is the main handler function, that prepares the command to be executed
// and wraps any returned errors
func (h *shellCallHandler) Exec(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		if h.debug {
			shellDebug(ctx, "Exec(%v)", args)
		}

		// This avoids interpreter builtins running first, which would make it
		// impossible to have a function named "echo", for example. We can
		// remove `.dag` from this point onward.
		if args[0] == ".dag" {
			args = args[1:]
		}

		st, err := h.cmd(ctx, args)
		if err == nil && st != nil {
			if h.debug {
				shellDebug(ctx, "└ OUT(%v): %+v", args, st)
			}
			err = st.Write(ctx)
		}
		if err != nil {
			m := err.Error()
			if h.debug {
				shellDebug(ctx, "Error(%v): %s", args, m)
			}
			// Ensure any error from the handler is written to stdout so that
			// the next command in the chain knows about it.
			if e := (ShellState{Error: &m}.Write(ctx)); e != nil {
				return fmt.Errorf("failed to encode error (%q): %w", m, e)
			}
			// There's a bug in the library where a handler that does `return err`
			// is fatal but NewExitStatus` is not. With a fatal error, if this
			// is in a command substitution, the parent command won't even
			// execute, but the next command in the pipeline will, and with.
			// an empty stdin. This way we pass the error state as an argument
			// to the parent command and fail there when parsing the arguments.
			return interp.NewExitStatus(shellHandlerExit)
		}

		return nil
	}
}

// cmd is the main logic for executing simple commands
func (h *shellCallHandler) cmd(ctx context.Context, args []string) (*ShellState, error) {
	c, a := args[0], args[1:]

	if isFirstShellCommand(ctx) {
		return h.entrypointCall(ctx, c, a)
	}

	var b []byte
	st, b, err := shellState(ctx)
	if err != nil {
		return nil, err
	}
	if st == nil {
		if h.debug {
			shellDebug(ctx, "IN(%v): %q", args, string(b))
		}
		return nil, fmt.Errorf("unexpected input for command %q", c)
	}
	if h.debug {
		shellDebug(ctx, "└ IN(%v): %+v", args, st)
	}

	builtin, err := h.BuiltinCommand(c)
	if err != nil {
		return nil, err
	}
	if builtin != nil {
		return nil, builtin.Execute(ctx, h, a, st)
	}

	if st.IsCommandRoot() {
		switch {
		case st.IsStdlib():
			// Example: .stdlib | <command>`
			stdlib, err := h.StdlibCommand(c)
			if err != nil {
				return nil, err
			}
			return nil, stdlib.Execute(ctx, h, a, nil)

		case st.IsDeps():
			// Example: `.deps | <dependency>`
			st, def, err := h.GetDependency(ctx, c)
			if err != nil {
				return nil, err
			}
			return h.constructorCall(ctx, def, st, a)

		case st.IsCore():
			// Example: `.core | <function>`
			def := h.modDef(st)
			if !def.HasCoreFunction(c) {
				return nil, fmt.Errorf("core function %q not found", c)
			}
			// an empty state's first object is Query by default so
			// functionCall already handles it
		}
	}

	// module or core function call
	return h.functionCall(ctx, st, c, a)
}

// entrypointCall is executed when it's the first command in a pipeline
func (h *shellCallHandler) entrypointCall(ctx context.Context, cmd string, args []string) (*ShellState, error) {
	if h.debug {
		shellDebug(ctx, "└ Entrypoint(%s, %v)", cmd, args)
	}

	if cmd, _ := h.BuiltinCommand(cmd); cmd != nil {
		return nil, cmd.Execute(ctx, h, args, nil)
	}

	st, err := h.stateLookup(ctx, cmd)
	if err != nil {
		return nil, err
	}
	if h.debug {
		shellDebug(ctx, "└ Found(%s, %v): %+v", cmd, args, st)
	}

	if st.IsStdlib() {
		cmd, err := h.StdlibCommand(cmd)
		if err != nil {
			return nil, err
		}
		return st, cmd.Execute(ctx, h, args, nil)
	}

	if md, _ := h.GetModuleDef(st); md != nil {
		// Command is a function in current context
		if h.isCurrentContextFunction(cmd) {
			// We need to assume a constructor call without arguments
			st, err := h.constructorCall(ctx, md, st, nil)
			if err != nil {
				return nil, err
			}
			return h.functionCall(ctx, st, cmd, args)
		}

		// Command is a dependency or module ref, so this is the constructor call
		if st.IsEmpty() {
			return h.constructorCall(ctx, md, st, args)
		}
	}

	return st, nil
}

func (h *shellCallHandler) isCurrentContextFunction(name string) bool {
	md, _ := h.GetModuleDef(nil)
	return md != nil && md.HasMainFunction(name)
}

func (h *shellCallHandler) stateLookup(ctx context.Context, name string) (*ShellState, error) {
	if h.debug {
		shellDebug(ctx, "  └ StateLookup(%v)", name)
	}
	// Is current context a loaded module?
	if md, _ := h.GetModuleDef(nil); md != nil {
		// 1. Function in current context
		if md.HasMainFunction(name) {
			if h.debug {
				shellDebug(ctx, "    - found in current context")
			}
			return h.newState(), nil
		}

		// 2. Dependency short name
		if dep := md.GetDependency(name); dep != nil {
			if h.debug {
				shellDebug(ctx, "    - found dependency (%s)", dep.ModRef)
			}
			depSt, _, err := h.GetDependency(ctx, name)
			return depSt, err
		}
	}

	// 3. Standard library command
	if cmd, _ := h.StdlibCommand(name); cmd != nil {
		if h.debug {
			shellDebug(ctx, "    - found stdlib command")
		}
		return h.newStdlibState(), nil
	}

	// 4. Path to local or remote module source
	// (local paths are relative to the current working directory, not the loaded module)
	st, err := h.getOrInitDefState(name, func() (*moduleDef, error) {
		return tryInitializeModule(ctx, h.dag, name)
	})
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, fmt.Errorf("function or module %q not found", name)
	}
	if h.debug {
		shellDebug(ctx, "    - found module reference")
	}
	return st, nil
}

func (h *shellCallHandler) getOrInitDefState(ref string, fn func() (*moduleDef, error)) (*ShellState, error) {
	_, exists := h.modDefs[ref]
	if !exists {
		if fn == nil {
			return nil, fmt.Errorf("module %q not loaded", ref)
		}
		def, err := fn()
		if err != nil || def == nil {
			return nil, err
		}
		h.modDefs[ref] = def
	}
	return h.newModState(ref), nil
}

func (h *shellCallHandler) constructorCall(ctx context.Context, md *moduleDef, st *ShellState, args []string) (*ShellState, error) {
	fn := md.MainObject.AsObject.Constructor

	values, err := h.parseArgumentValues(ctx, md, fn, args)
	if err != nil {
		return nil, fmt.Errorf("constructor: %w", err)
	}

	return st.WithCall(fn, values), nil
}

// functionCall is executed for every command that the exec handler processes
func (h *shellCallHandler) functionCall(ctx context.Context, st *ShellState, name string, args []string) (*ShellState, error) {
	def := h.modDef(st)
	call := st.Function()

	fn, err := call.GetNextDef(def, name)
	if err != nil {
		return st, err
	}

	argValues, err := h.parseArgumentValues(ctx, def, fn, args)
	if err != nil {
		return st, fmt.Errorf("could not parse arguments for function %q: %w", fn.CmdName(), err)
	}

	return st.WithCall(fn, argValues), nil
}

// shellPreprocessArgs converts positional arguments to flag arguments
//
// Values are not processed. This function is used to leverage pflags to parse
// flags interspersed with positional arguments, so a function's required
// arguments can be placed anywhere. Then we get the unprocessed flags in
// order to validate if the remaining number of positional arguments match
// the number of required arguments.
//
// Required args in dagger shell are positional but we have a lot of power
// in custom flags that we want to reuse, so just add the corresponding
// `--flag-name` args in order for pflags to be able to parse them later.
//
// Additionally, if there's only one required argument that is a list of strings,
// all positional arguments are used as elements of that list.
func shellPreprocessArgs(fn *modFunction, args []string) ([]string, error) {
	flags := pflag.NewFlagSet(fn.CmdName(), pflag.ContinueOnError)

	opts := fn.OptionalArgs()

	// All CLI arguments are strings at first, but booleans can be omitted.
	// We don't wan't to process values yet, just validate and consume the flags
	// so we get the remaining positional args.
	for _, arg := range opts {
		name := arg.FlagName()

		switch arg.TypeDef.Kind {
		case dagger.TypeDefKindListKind:
			switch arg.TypeDef.AsList.ElementTypeDef.Kind {
			case dagger.TypeDefKindBooleanKind:
				flags.BoolSlice(name, nil, "")
			default:
				flags.StringSlice(name, nil, "")
			}
		case dagger.TypeDefKindBooleanKind:
			flags.Bool(name, false, "")
		default:
			flags.String(name, "", "")
		}
	}

	if err := flags.Parse(args); err != nil {
		return args, err
	}

	reqs := fn.RequiredArgs()

	// A command for with-exec could include a `--`, but it's only if it's
	// the first positional argument that means we've stopped processing our
	// flags. So these are equivalent:
	// - with-exec --redirect-stdout /out git checkout -- file
	// - with-exec --redirect-stdout /out -- git checkout -- file
	pos := flags.Args()
	if flags.ArgsLenAtDash() == 1 {
		pos = pos[1:]
	}

	// Final processed arguments that will be parsed in the second phase later on.
	var a []string

	// Convenience for a single required argument of type [String!]!
	// All positional arguments become elements in the list.
	if len(reqs) == 1 && len(pos) > 1 && reqs[0].TypeDef.String() == "[]string" {
		name := reqs[0].FlagName()
		a = make([]string, 0, len(opts)+len(pos))

		for _, value := range pos {
			// Instead of creating a CSV value here, repeat the flag for each
			// one so that pflags is the only one dealing with CSVs.
			a = append(a, fmt.Sprintf("--%s=%v", name, value))
		}
	} else {
		// Normal use case. Positional arguments should match number of required function arguments
		if err := ExactArgs(len(reqs))(pos); err != nil {
			return args, err
		}
		a = make([]string, 0, len(fn.Args))
		// Use the `=` syntax so that each element in the args list corresponds
		// to a single argument instead of two.
		for i, arg := range reqs {
			a = append(a, fmt.Sprintf("--%s=%v", arg.FlagName(), pos[i]))
		}
	}

	// Add all the optional flags
	flags.Visit(func(f *pflag.Flag) {
		if f.Changed {
			a = append(a, fmt.Sprintf("--%s=%v", f.Name, f.Value.String()))
		}
	})

	return a, nil
}

// parseArgumentValues returns a map of argument names and their parsed values
func (h *shellCallHandler) parseArgumentValues(ctx context.Context, md *moduleDef, fn *modFunction, args []string) (map[string]any, error) {
	args, err := shellPreprocessArgs(fn, args)
	if err != nil {
		return nil, err
	}

	if h.debug {
		shellDebug(ctx, "preprocessed arguments: %v", args)
	}

	flags := pflag.NewFlagSet(fn.CmdName(), pflag.ContinueOnError)
	flags.SetOutput(interp.HandlerCtx(ctx).Stderr)

	// Add flags for each argument, including unsupported ones, which we
	// assume it's being supported through some other means, so we just
	// bypass the flags. This how we pass ID values to flag parsing, without
	// having support for it with a custom flag.
	// TODO: Create an "ID" or "Raw" type flag and validate appropriately
	for _, a := range fn.Args {
		err := a.AddFlag(flags)
		var e *UnsupportedFlagError
		if errors.As(err, &e) {
			// This is just enough to trigger passing the value to ParseAll,
			// but will only be used for getting the value if it doesn't
			// originate from a command expansion (subshell).
			// TODO: This will likely fail if value doesn't come from command
			// expansion because the value that is passed goes directly to the
			// API. We should validate this more, or refactor.
			flags.String(a.FlagName(), "", a.Description)
			flags.MarkHidden(a.FlagName())
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("error addding flag: %w", err)
		}
	}

	// Final map of resolved argument values
	values := make(map[string]any, len(fn.Args))

	// Parse arguments using flags to get the values matched with the right
	// argument definition. Bypass the flag if the argument value comes from
	// a command expansion, otherwise set the flag value.
	f := func(flag *pflag.Flag, value string) error {
		a, err := fn.GetArg(flag.Name)
		if err != nil {
			return err
		}
		v, bypass, err := h.parseFlagValue(ctx, value, a.TypeDef)
		if err != nil {
			return fmt.Errorf("cannot expand function argument %q: %w", a.FlagName(), err)
		}
		if v == nil {
			return fmt.Errorf("unexpected nil value while expanding function argument %q", a.FlagName())
		}
		// Flags only support setting their values from strings, so if
		// anything else is returned, we just ignore it.
		// TODO: try to validate this more to avoid surprises
		if sval, ok := v.(string); ok && !bypass {
			return flags.Set(flag.Name, sval)
		}
		// This will bypass using a flag for this argument since we're
		// saying it's a final value already.
		if bypass {
			values[a.Name] = v
		}
		return nil
	}
	if err := flags.ParseAll(args, f); err != nil {
		return nil, err
	}

	// Finally, get the values from the flags that haven't been resolved yet.
	for _, a := range fn.Args {
		if _, exists := values[a.Name]; exists {
			continue
		}
		flag, err := a.GetFlag(flags)
		if err != nil {
			return nil, err
		}
		if !flag.Changed {
			continue
		}
		v, err := a.GetFlagValue(ctx, flag, h.dag, md)
		if err != nil {
			return nil, err
		}
		values[a.Name] = v
	}

	return values, nil
}

// parseFlagValue ensures that a flag value with state gets resolved
//
// This happens most commonly when argument is the result of command expansion
// from a sub-shell.
func (h *shellCallHandler) parseFlagValue(ctx context.Context, value string, argType *modTypeDef) (any, bool, error) {
	if !strings.HasPrefix(value, shellStatePrefix) {
		return value, false, nil
	}

	var bypass bool

	handleObjectID := func(_ context.Context, q *querybuilder.Selection, t *modTypeDef) (*querybuilder.Selection, error) {
		// When an argument returns an object, assume we want its ID
		// TODO: Allow ids in TypeDefs so we can directly check if there's an `id`
		// function in this object.
		if t.AsFunctionProvider() != nil {
			if argType.Name() != t.Name() {
				return nil, fmt.Errorf("expected return type %q, got %q", argType.Name(), t.Name())
			}
			q = q.Select("id")
			bypass = true
		}

		// TODO: do a bit more validation. Consider that values that are not
		// to be replaced should only be strings, because that's what the
		// flagSet supports. This also means the type won't match the expected
		// definition. For example, a function that returns a `Directory` object
		// could have a subshell return a path string so the flag will turn that
		// into the `Directory` object.

		return q, nil
	}
	v, err := h.Result(ctx, strings.NewReader(value), false, handleObjectID)
	return v, bypass, err
}

// Result reads the state from stdin and returns the final result
func (h *shellCallHandler) Result(
	ctx context.Context,
	// r is the reader to read the shell state from
	r io.Reader,
	// doPrintResponse prepares the response for printing according to an output
	// format
	doPrintResponse bool,
	// beforeRequest is a callback that allows modifying the query before making
	// the request
	//
	// It's also useful for validating the query with the function's
	// return type.
	beforeRequest func(context.Context, *querybuilder.Selection, *modTypeDef) (*querybuilder.Selection, error),
) (any, error) {
	st, b, err := readShellState(r)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return string(b), nil
	}

	if st.IsCommandRoot() {
		switch {
		case st.IsStdlib():
			return h.CommandsList(st.Cmd, h.Stdlib()), nil
		case st.IsDeps():
			return h.DependenciesList(), nil
		case st.IsCore():
			def := h.modDef(nil)
			return h.FunctionsList(st.Cmd, def.GetCoreFunctions()), nil
		default:
			return nil, fmt.Errorf("unexpected namespace %q", st.Cmd)
		}
	}

	def := h.modDef(st)

	// Example: `build` (i.e., omitted constructor)
	if def.HasModule() && st.IsEmpty() {
		st, err = h.constructorCall(ctx, def, st, nil)
		if err != nil {
			return nil, err
		}
	}

	fn, err := st.Function().GetDef(def)
	if err != nil {
		return nil, err
	}

	q := st.QueryBuilder(h.dag)
	if beforeRequest != nil {
		q, err = beforeRequest(ctx, q, fn.ReturnType)
		if err != nil {
			return nil, err
		}
	}

	// The beforeRequest hook has a chance to return a nil `q` to signal
	// that we shouldn't proceed with the request. For example, it's
	// possible  that a pipeline ending in an object doesn't have anything
	// to sub-select.
	if q == nil {
		return nil, nil
	}

	var response any

	if err := makeRequest(ctx, q, &response); err != nil {
		return nil, err
	}

	if fn.ReturnType.Kind == dagger.TypeDefKindVoidKind {
		return nil, nil
	}

	if doPrintResponse {
		buf := new(bytes.Buffer)
		frmt := outputFormat(fn.ReturnType)
		if err := printResponse(buf, response, frmt); err != nil {
			return nil, err
		}
		return buf.String(), nil
	}

	return response, nil
}

func shellDebug(ctx context.Context, msg string, args ...any) {
	hctx := interp.HandlerCtx(ctx)
	shellFDebug(hctx.Stderr, msg, args...)
}

func shellFDebug(w io.Writer, msg string, args ...any) {
	cat := termenv.String("[DBG]").Foreground(termenv.ANSIMagenta).String()
	msg = termenv.String(fmt.Sprintf(msg, args...)).Faint().String()
	fmt.Fprintln(w, cat, msg)
}

// First command in pipeline: e.g., `cmd1 | cmd2 | cmd3`
func isFirstShellCommand(ctx context.Context) bool {
	return interp.HandlerCtx(ctx).Stdin == nil
}

func shellState(ctx context.Context) (*ShellState, []byte, error) {
	return readShellState(interp.HandlerCtx(ctx).Stdin)
}

// readShellState deserializes shell state
//
// We use an hardcoded prefix when writing and reading state to make it easy
// to detect if a given input is a shell state or not. This way we can tell
// the difference between a serialized state that failed to unmarshal and
// non-state data.
func readShellState(r io.Reader) (*ShellState, []byte, error) {
	if r == nil {
		return nil, nil, nil
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	p := []byte(shellStatePrefix)
	if !bytes.HasPrefix(b, p) {
		return nil, b, nil
	}
	encoded := bytes.TrimPrefix(b, p)
	decoder := base64.NewDecoder(base64.StdEncoding, bytes.NewReader(encoded))
	jsonDec := json.NewDecoder(decoder)
	jsonDec.UseNumber()

	var s ShellState
	if err := jsonDec.Decode(&s); err != nil {
		return nil, b, fmt.Errorf("decode state: %w", err)
	}
	if s.IsError() {
		return &s, nil, errors.New(*s.Error)
	}
	return &s, nil, nil
}

// ShellState is an intermediate representation of a query
//
// The query builder serializes to a GraphQL query but not back from it so we
// use this data structure to keep track of the command chain in order to
// make it easy to create a querybuilder.Selection from it, when needed.
//
// We could alternatively encode this in the querybuilder itself, except that
// this state also includes key pieces of information from introspection that
// make it very easy to validate and get the next function's definition.
//
// This state is passed around from the stdout of an exec handler to then next
// one's stdin. Each handler in the chain should add a corresponding FunctionCall
// to the state and write it to stdout for the next handler to read.
type ShellState struct {
	// ModRef is the module reference for the current state
	//
	// If empty, it must fall back to the default context.
	// It matches a key in the modDefs map in the handler, which comes from
	// user input, not from the API.
	ModRef string `json:"modRef"`

	// Cmd is non-empty if next command comes from a builtin instead of an API object
	Cmd string `json:"ns"`

	// Calls is the list of functions for building an API query
	Calls []FunctionCall `json:"calls,omitempty"`

	// Error is non-nil if the previous command failed
	Error *string `json:"error,omitempty"`
}

func (st ShellState) IsError() bool {
	return st.Error != nil
}

// IsEmpty returns true if there's no function calls in the chain
func (st ShellState) IsEmpty() bool {
	return len(st.Calls) == 0
}

func (st ShellState) IsCommandRoot() bool {
	return st.IsEmpty() && st.Cmd != ""
}

func (st ShellState) IsStdlib() bool {
	return st.Cmd == shellStdlibCmdName
}

func (st ShellState) IsCore() bool {
	return st.Cmd == shellCoreCmdName
}

func (st ShellState) IsDeps() bool {
	return st.Cmd == shellDepsCmdName
}

// FunctionCall represents a querybyilder.Selection
//
// The query builder only cares about the name of the function and its arguments,
// but we also keep track of its object's name and return type to make it easy
// to get the right definition from the introspection data.
type FunctionCall struct {
	Object       string         `json:"object"`
	Name         string         `json:"name"`
	Arguments    map[string]any `json:"arguments"`
	ReturnObject string         `json:"returnObject"`
}

// Write serializes the shell state to the current exec handler's stdout
func (st ShellState) Write(ctx context.Context) error {
	return st.WriteTo(interp.HandlerCtx(ctx).Stdout)
}

func (st ShellState) WriteTo(w io.Writer) error {
	var buf bytes.Buffer

	// Encode state in base64 to avoid issues with spaces being turned into
	// multiple arguments when the result of a command subsitution.
	bEnc := base64.NewEncoder(base64.StdEncoding, &buf)
	jEnc := json.NewEncoder(bEnc)

	if err := jEnc.Encode(st); err != nil {
		return err
	}
	if err := bEnc.Close(); err != nil {
		return err
	}

	w.Write([]byte(shellStatePrefix))
	w.Write(buf.Bytes())

	return nil
}

// Function returns the last function in the chain, if not empty
func (st ShellState) Function() FunctionCall {
	if st.IsEmpty() {
		// The first call is a field under Query.
		return FunctionCall{
			ReturnObject: "Query",
		}
	}
	return st.Calls[len(st.Calls)-1]
}

// WithCall returns a new state with the given function call added to the chain
func (st ShellState) WithCall(fn *modFunction, argValues map[string]any) *ShellState {
	prev := st.Function()
	return &ShellState{
		Cmd:    st.Cmd,
		ModRef: st.ModRef,
		Calls: append(st.Calls, FunctionCall{
			Object:       prev.ReturnObject,
			Name:         fn.Name,
			ReturnObject: fn.ReturnType.Name(),
			Arguments:    argValues,
		}),
	}
}

// QueryBuilder returns a querybuilder.Selection from the shell state
func (st ShellState) QueryBuilder(dag *dagger.Client) *querybuilder.Selection {
	q := querybuilder.Query().Client(dag.GraphQLClient())
	for _, call := range st.Calls {
		q = q.Select(call.Name)
		for n, v := range call.Arguments {
			q = q.Arg(n, v)
		}
	}
	return q
}

// GetTypeDef returns the introspection definition for the return type of the last function call
func (st *ShellState) GetTypeDef(modDef *moduleDef) (*modTypeDef, error) {
	fn, err := st.GetDef(modDef)
	return fn.ReturnType, err
}

// GetDef returns the introspection definition for the last function call
func (st *ShellState) GetDef(modDef *moduleDef) (*modFunction, error) {
	if st == nil || st.IsEmpty() {
		return modDef.MainObject.AsObject.Constructor, nil
	}
	return st.Function().GetDef(modDef)
}

// GetDef returns the introspection definition for this function call
func (f FunctionCall) GetDef(modDef *moduleDef) (*modFunction, error) {
	return modDef.GetObjectFunction(f.Object, cliName(f.Name))
}

// GetNextDef returns the introspection definition for the next function call, based on
// the current return type and name of the next function
func (f FunctionCall) GetNextDef(modDef *moduleDef, name string) (*modFunction, error) {
	if f.ReturnObject == "" {
		return nil, fmt.Errorf("cannot pipe %q after %q returning a non-object type", name, f.Name)
	}
	return modDef.GetObjectFunction(f.ReturnObject, name)
}

// ShellCommand is a Dagger Shell builtin or stdlib command
type ShellCommand struct {
	// Use is the one-line usage message
	Use string

	// Description is the short description shown in the '.help' output
	Description string

	// Expected arguments
	Args PositionalArgs

	// Expected state
	State StateArg

	// Run is the function that will be executed.
	Run func(cmd *ShellCommand, args []string, st *ShellState) error

	// Complete provides builtin completions
	Complete func(ctx *CompletionContext, args []string) *CompletionContext

	// HelpFunc is a custom function for customizing the help output
	HelpFunc func(cmd *ShellCommand) string

	// The group id under which this command is grouped in the '.help' output
	GroupID string

	// Hidden hides the command from `.help`
	Hidden bool

	ctx    context.Context
	out    io.Writer
	outErr io.Writer
}

// CleanName is the command name without the "." prefix.
func (c *ShellCommand) CleanName() string {
	return strings.TrimPrefix(c.Name(), ".")
}

// Name is the command name.
func (c *ShellCommand) Name() string {
	name := c.Use
	i := strings.Index(name, " ")
	if i >= 0 {
		name = name[:i]
	}
	return name
}

// Short is the the summary for the command
func (c *ShellCommand) Short() string {
	return strings.Split(c.Description, "\n")[0]
}

func (c *ShellCommand) Help() string {
	if c.HelpFunc != nil {
		return c.HelpFunc(c)
	}
	return c.defaultHelp()
}

func (c *ShellCommand) defaultHelp() string {
	var doc ShellDoc

	if c.Description != "" {
		doc.Add("", c.Description)
	}

	doc.Add("Usage", c.Use)

	return doc.String()
}

func (c *ShellCommand) Print(a ...any) error {
	_, err := fmt.Fprint(c.out, a...)
	return err
}

func (c *ShellCommand) Println(a ...any) error {
	_, err := fmt.Fprintln(c.out, a...)
	return err
}

func (c *ShellCommand) Printf(format string, a ...any) error {
	_, err := fmt.Fprintf(c.out, format, a...)
	return err
}

func (c *ShellCommand) SetContext(ctx context.Context) {
	c.ctx = ctx
	c.out = interp.HandlerCtx(ctx).Stdout
	c.outErr = interp.HandlerCtx(ctx).Stderr
}

func (c *ShellCommand) Context() context.Context {
	return c.ctx
}

// Send writes the state to the command's stdout.
func (c *ShellCommand) Send(st *ShellState) error {
	return st.WriteTo(c.out)
}

type PositionalArgs func(args []string) error

func MinimumArgs(n int) PositionalArgs {
	return func(args []string) error {
		if len(args) < n {
			return fmt.Errorf("requires at least %d arg(s), received %d", n, len(args))
		}
		return nil
	}
}

func MaximumArgs(n int) PositionalArgs {
	return func(args []string) error {
		if len(args) > n {
			return fmt.Errorf("accepts at most %d arg(s), received %d", n, len(args))
		}
		return nil
	}
}

func ExactArgs(n int) PositionalArgs {
	return func(args []string) error {
		if len(args) < n {
			return fmt.Errorf("missing %d positional argument(s)", n-len(args))
		}
		if len(args) > n {
			return fmt.Errorf("accepts at most %d positional argument(s), received %d", n, len(args))
		}
		return nil
	}
}

func NoArgs(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("received unknown %d args", len(args))
	}
	return nil
}

type StateArg uint

const (
	AnyState StateArg = iota
	RequiredState
	NoState
)

// Execute is the main dispatcher function for shell builtin commands
func (c *ShellCommand) Execute(ctx context.Context, h *shellCallHandler, args []string, st *ShellState) error {
	switch c.State {
	case AnyState:
	case RequiredState:
		if st == nil {
			return fmt.Errorf("command %q must be piped\nusage: %s", c.Name(), c.Use)
		}
	case NoState:
		if st != nil {
			return fmt.Errorf("command %q cannot be piped\nusage: %s", c.Name(), c.Use)
		}
	}
	if c.Args != nil {
		if err := c.Args(args); err != nil {
			return fmt.Errorf("command %q %w\nusage: %s", c.Name(), err, c.Use)
		}
	}
	// Resolve state values in arguments
	a := make([]string, 0, len(args))
	for i, arg := range args {
		if strings.HasPrefix(arg, shellStatePrefix) {
			w := strings.NewReader(arg)
			v, err := h.Result(ctx, w, false, nil)
			if err != nil {
				return fmt.Errorf("cannot expand command argument at %d", i)
			}
			if v == nil {
				return fmt.Errorf("unexpected nil value while expanding argument at %d", i)
			}
			arg = fmt.Sprintf("%v", v)
		}
		a = append(a, arg)
	}
	if h.debug {
		shellDebug(ctx, "└ CmdExec(%v)", a)
	}
	c.SetContext(ctx)
	return c.Run(c, a, st)
}

// shellFunctionUseLine returns the usage line fine for a function
func shellFunctionUseLine(md *moduleDef, fn *modFunction) string {
	sb := new(strings.Builder)

	if fn == md.MainObject.AsObject.Constructor {
		sb.WriteString(md.ModRef)
	} else {
		sb.WriteString(fn.CmdName())
	}

	for _, arg := range fn.RequiredArgs() {
		sb.WriteString(" <")
		sb.WriteString(arg.FlagName())
		sb.WriteString(">")
	}

	if len(fn.OptionalArgs()) > 0 {
		sb.WriteString(" [options]")
	}

	return sb.String()
}

func (h *shellCallHandler) GroupBuiltins(groupID string) []*ShellCommand {
	l := make([]*ShellCommand, 0, len(h.builtins))
	for _, c := range h.Builtins() {
		if c.GroupID == groupID {
			l = append(l, c)
		}
	}
	return l
}

func (h *shellCallHandler) BuiltinCommand(name string) (*ShellCommand, error) {
	if name == "." || !strings.HasPrefix(name, ".") || strings.Contains(name, "/") {
		return nil, nil
	}
	for _, c := range h.builtins {
		if c.Name() == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("command not found %q", name)
}

func (h *shellCallHandler) StdlibCommand(name string) (*ShellCommand, error) {
	for _, c := range h.stdlib {
		if c.Name() == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("command not found %q", name)
}

func (h *shellCallHandler) Builtins() []*ShellCommand {
	l := make([]*ShellCommand, 0, len(h.builtins))
	for _, c := range h.builtins {
		if !c.Hidden {
			l = append(l, c)
		}
	}
	return l
}

func (h *shellCallHandler) Stdlib() []*ShellCommand {
	l := make([]*ShellCommand, 0, len(h.stdlib))
	for _, c := range h.stdlib {
		if !c.Hidden {
			l = append(l, c)
		}
	}
	return l
}

func (h *shellCallHandler) FunctionsList(name string, fns []*modFunction) string {
	if len(fns) == 0 {
		return ""
	}

	sb := new(strings.Builder)

	sb.WriteString("Available functions:\n")
	for _, f := range fns {
		sb.WriteString("  - ")
		sb.WriteString(f.CmdName())
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf(`Use "%s | .doc" for more details.`, name))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf(`Use "%s | .doc <function>" for more information on a function.`, name))
	sb.WriteString("\n")

	return sb.String()
}

func (h *shellCallHandler) CommandsList(name string, cmds []*ShellCommand) string {
	if len(cmds) == 0 {
		return ""
	}

	sb := new(strings.Builder)

	sb.WriteString("Available commands:\n")
	for _, c := range cmds {
		sb.WriteString("  - ")
		sb.WriteString(c.Name())
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf(`Use "%s | .doc" for more details.`, name))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf(`Use "%s | .doc <command>" for more information on a command.`, name))
	sb.WriteString("\n")

	return sb.String()
}

func (h *shellCallHandler) DependenciesList() string {
	// This is validated in the .deps command
	def, _ := h.GetModuleDef(nil)
	if def == nil || len(def.Dependencies) == 0 {
		return ""
	}

	sb := new(strings.Builder)

	sb.WriteString("Available dependencies:\n")
	for _, dep := range def.Dependencies {
		sb.WriteString("  - ")
		sb.WriteString(dep.Name)
		sb.WriteString("\n")
	}

	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf(`Use "%s | .doc" for more details.`, shellDepsCmdName))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf(`Use "%s | .doc <dependency>" for more information on a dependency.`, shellDepsCmdName))
	sb.WriteString("\n")

	return sb.String()
}

func (h *shellCallHandler) StdlibHelp() string {
	var doc ShellDoc

	doc.Add("Commands", nameShortWrapped(h.Stdlib(), func(c *ShellCommand) (string, string) {
		return c.Name(), c.Description
	}))

	doc.Add("", fmt.Sprintf(`Use "%s | .doc <command>" for more information on a command.`, shellStdlibCmdName))

	return doc.String()
}

func (h *shellCallHandler) CoreHelp() string {
	var doc ShellDoc

	def := h.modDef(nil)

	doc.Add(
		"Available Functions",
		nameShortWrapped(def.GetCoreFunctions(), func(f *modFunction) (string, string) {
			return f.CmdName(), f.Short()
		}),
	)

	doc.Add("", fmt.Sprintf(`Use "%s | .doc <function>" for more information on a function.`, shellCoreCmdName))

	return doc.String()
}

func (h *shellCallHandler) DepsHelp() string {
	// This is validated in the .deps command
	def, _ := h.GetModuleDef(nil)
	if def == nil {
		return ""
	}

	var doc ShellDoc

	doc.Add(
		"Module Dependencies",
		nameShortWrapped(def.Dependencies, func(dep *moduleDependency) (string, string) {
			return dep.Name, dep.Short()
		}),
	)

	doc.Add("", fmt.Sprintf(`Use "%s | .doc <dependency>" for more information on a dependency.`, shellDepsCmdName))

	return doc.String()
}

type ShellDoc struct {
	Groups []ShellDocSection
}

type ShellDocSection struct {
	Title  string
	Body   string
	Indent uint
}

func (d *ShellDoc) Add(title, body string) {
	d.Groups = append(d.Groups, ShellDocSection{Title: title, Body: body})
}

func (d *ShellDoc) AddSection(title, body string) {
	d.Groups = append(d.Groups, ShellDocSection{Title: title, Body: body, Indent: helpIndent})
}

func (d ShellDoc) String() string {
	width := getViewWidth()

	sb := new(strings.Builder)
	for i, grp := range d.Groups {
		body := grp.Body

		if grp.Title != "" {
			sb.WriteString(indent.String(toUpperBold(grp.Title), grp.Indent))
			sb.WriteString("\n")

			// Indent body if there's a title
			var i uint
			if !strings.HasPrefix(body, strings.Repeat(" ", int(helpIndent))) {
				i = helpIndent + grp.Indent
			} else if grp.Indent > 0 && !strings.HasPrefix(body, strings.Repeat(" ", int(helpIndent+grp.Indent))) {
				i = grp.Indent
			}
			if i > 0 {
				wrapped := wordwrap.String(grp.Body, width-int(i))
				body = indent.String(wrapped, i)
			}
		}
		sb.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			sb.WriteString("\n")
		}
		// Extra new line between groups
		if i < len(d.Groups)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func shellModuleDoc(st *ShellState, m *moduleDef) string {
	var doc ShellDoc

	meta := new(strings.Builder)
	meta.WriteString(m.Name)
	if m.Description != "" {
		meta.WriteString("\n\n")
		meta.WriteString(m.Description)
	}
	if meta.Len() > 0 {
		doc.Add("Module", meta.String())
	}

	fn := m.MainObject.AsObject.Constructor
	if len(fn.Args) > 0 {
		constructor := new(strings.Builder)
		constructor.WriteString("Usage: ")
		constructor.WriteString(shellFunctionUseLine(m, fn))

		if fn.Description != "" {
			constructor.WriteString("\n\n")
			constructor.WriteString(fn.Description)
		}

		doc.Add("Entrypoint", constructor.String())

		if args := fn.RequiredArgs(); len(args) > 0 {
			doc.AddSection(
				"Required Arguments",
				nameShortWrapped(args, func(a *modFunctionArg) (string, string) {
					return strings.TrimPrefix(a.Usage(), "--"), a.Long()
				}),
			)
		}
		if args := fn.OptionalArgs(); len(args) > 0 {
			doc.AddSection(
				"Optional Arguments",
				nameShortWrapped(args, func(a *modFunctionArg) (string, string) {
					return a.Usage(), a.Long()
				}),
			)
		}
	}

	// If it's just `.doc` and the current module doesn't have required args,
	// can use the default constructor and show available functions.
	if st.IsEmpty() && st.ModRef == "" && !fn.HasRequiredArgs() {
		if fns := m.MainObject.AsFunctionProvider().GetFunctions(); len(fns) > 0 {
			doc.Add(
				"Available Functions",
				nameShortWrapped(fns, func(f *modFunction) (string, string) {
					return f.CmdName(), f.Short()
				}),
			)
			doc.Add("", `Use ".doc <function>" for more information on a function.`)
		}
	}

	return doc.String()
}

func shellTypeDoc(t *modTypeDef) string {
	var doc ShellDoc

	fp := t.AsFunctionProvider()
	if fp == nil {
		doc.Add(t.KindDisplay(), t.Long())

		// If not an object, only have the type to show.
		return doc.String()
	}

	if fp.ProviderName() != "Query" {
		doc.Add(t.KindDisplay(), t.Long())
	}

	if fns := fp.GetFunctions(); len(fns) > 0 {
		doc.Add(
			"Available Functions",
			nameShortWrapped(fns, func(f *modFunction) (string, string) {
				return f.CmdName(), f.Short()
			}),
		)
		doc.Add("", `Use ".doc <function>" for more information on a function.`)
	}

	return doc.String()
}

func shellFunctionDoc(md *moduleDef, fn *modFunction) string {
	var doc ShellDoc

	if fn.Description != "" {
		doc.Add("", fn.Description)
	}

	usage := shellFunctionUseLine(md, fn)
	if usage != "" {
		doc.Add("Usage", usage)
	}

	if args := fn.RequiredArgs(); len(args) > 0 {
		doc.Add(
			"Required Arguments",
			nameShortWrapped(args, func(a *modFunctionArg) (string, string) {
				return strings.TrimPrefix(a.Usage(), "--"), a.Long()
			}),
		)
	}

	if args := fn.OptionalArgs(); len(args) > 0 {
		doc.Add(
			"Optional Arguments",
			nameShortWrapped(args, func(a *modFunctionArg) (string, string) {
				return a.Usage(), a.Long()
			}),
		)
	}

	if rettype := fn.ReturnType.Short(); rettype != "" {
		doc.Add("Returns", rettype)
	}

	if fn.ReturnType.AsFunctionProvider() != nil {
		u := strings.TrimSuffix(usage, " [options]")
		doc.Add("", fmt.Sprintf(`Use "%s | .doc" for available functions.`, u))
	}

	return doc.String()
}

func (h *shellCallHandler) isDefaultState(st *ShellState) bool {
	return st == nil || st.ModRef == "" || st.ModRef == h.modRef
}

func (h *shellCallHandler) newModState(ref string) *ShellState {
	return &ShellState{
		ModRef: ref,
	}
}

func (h *shellCallHandler) newStdlibState() *ShellState {
	return &ShellState{
		Cmd: shellStdlibCmdName,
	}
}

func (h *shellCallHandler) newCoreState() *ShellState {
	return &ShellState{
		Cmd: shellCoreCmdName,
	}
}

func (h *shellCallHandler) newDepsState() *ShellState {
	return &ShellState{
		Cmd: shellDepsCmdName,
	}
}

func (h *shellCallHandler) newState() *ShellState {
	return &ShellState{}
}

func (h *shellCallHandler) modDef(st *ShellState) *moduleDef {
	ref := h.modRef
	if !h.isDefaultState(st) {
		ref = st.ModRef
	}
	if modDef, ok := h.modDefs[ref]; ok {
		return modDef
	}
	// Every time h.modRef is set, there should be a corresponding value in
	// h.modDefs. Otherwise there's a bug in the CLI.
	panic(fmt.Sprintf("module %q not loaded", ref))
}

func (h *shellCallHandler) GetModuleDef(st *ShellState) (*moduleDef, error) {
	if def := h.modDef(st); def.HasModule() {
		return def, nil
	}
	return nil, fmt.Errorf("module not loaded")
}

func (h *shellCallHandler) GetDependency(ctx context.Context, name string) (*ShellState, *moduleDef, error) {
	modDef, err := h.GetModuleDef(nil)
	if err != nil {
		return nil, nil, err
	}
	dep := modDef.GetDependency(name)
	if dep == nil {
		return nil, nil, fmt.Errorf("dependency %q not found", name)
	}
	st, err := h.getOrInitDefState(dep.ModRef, func() (*moduleDef, error) {
		var opts []dagger.ModuleSourceOpts
		if dep.RefPin != "" {
			opts = append(opts, dagger.ModuleSourceOpts{RefPin: dep.RefPin})
		}
		return initializeModule(ctx, h.dag, dep.ModRef, false, opts...)
	})
	if err != nil {
		return nil, nil, err
	}
	def, err := h.GetModuleDef(st)
	if err != nil {
		return nil, nil, err
	}
	return st, def, nil
}

func (h *shellCallHandler) registerCommands() { //nolint:gocyclo
	var builtins []*ShellCommand
	var stdlib []*ShellCommand

	builtins = append(builtins,
		&ShellCommand{
			Use:    ".debug",
			Hidden: true,
			Args:   NoArgs,
			State:  NoState,
			Run: func(cmd *ShellCommand, args []string, _ *ShellState) error {
				// Toggles debug mode, which can be useful when in interactive mode
				h.debug = !h.debug
				return nil
			},
		},
		&ShellCommand{
			Use:         ".help [command]",
			Description: "Print this help message",
			Args:        MaximumArgs(1),
			State:       NoState,
			Run: func(cmd *ShellCommand, args []string, _ *ShellState) error {
				if len(args) == 1 {
					c, err := h.BuiltinCommand(args[0])
					if err != nil {
						return err
					}
					if c == nil {
						err = fmt.Errorf("command not found %q", args[0])
						if !strings.HasPrefix(args[0], ".") {
							if builtin, _ := h.BuiltinCommand("." + args[0]); builtin != nil {
								err = fmt.Errorf("%w, did you mean %q?", err, "."+args[0])
							}
						}
						return err
					}
					return cmd.Println(c.Help())
				}

				var doc ShellDoc

				for _, group := range shellGroups {
					cmds := h.GroupBuiltins(group.ID)
					if len(cmds) == 0 {
						continue
					}
					doc.Add(
						group.Title,
						nameShortWrapped(cmds, func(c *ShellCommand) (string, string) {
							return c.Name(), c.Short()
						}),
					)
				}

				doc.Add("", `Use ".help <command>" for more information.`)

				return cmd.Println(doc.String())
			},
		},
		&ShellCommand{
			Use: ".doc [module]\n<function> | .doc [function]",
			Description: `Show documentation for a module, a type, or a function


Local module paths are resolved relative to the workdir on the host, not relative
to the currently loaded module.
`,
			Args: MaximumArgs(1),
			Run: func(cmd *ShellCommand, args []string, st *ShellState) error {
				var err error

				ctx := cmd.Context()

				// First command in chain
				if st == nil {
					if len(args) == 0 {
						// No arguments, e.g, `.doc`.
						st = h.newState()
					} else {
						// Use the same function lookup as when executing so
						// that `> .doc wolfi` documents `> wolfi`.
						st, err = h.stateLookup(ctx, args[0])
						if err != nil {
							return err
						}
						if st.ModRef != "" {
							// First argument to `.doc` is a module reference, so
							// remove it from list of arguments now that it's loaded.
							// The rest of the arguments should be passed on to
							// the constructor.
							args = args[1:]
						}
					}
				}

				def := h.modDef(st)

				if st.IsEmpty() {
					switch {
					case st.IsStdlib():
						// Document stdlib
						// Example: `.stdlib | .doc`
						if len(args) == 0 {
							return cmd.Println(h.StdlibHelp())
						}
						// Example: .stdlib | .doc <command>`
						c, err := h.StdlibCommand(args[0])
						if err != nil {
							return err
						}
						return cmd.Println(c.Help())

					case st.IsDeps():
						// Document dependency
						// Example: `.deps | .doc`
						if len(args) == 0 {
							return cmd.Println(h.DepsHelp())
						}
						// Example: `.deps | .doc <dependency>`
						depSt, depDef, err := h.GetDependency(ctx, args[0])
						if err != nil {
							return err
						}
						return cmd.Println(shellModuleDoc(depSt, depDef))

					case st.IsCore():
						// Document core
						// Example: `.core | .doc`
						if len(args) == 0 {
							return cmd.Println(h.CoreHelp())
						}
						// Example: `.core | .doc <function>`
						fn := def.GetCoreFunction(args[0])
						if fn == nil {
							return fmt.Errorf("core function %q not found", args[0])
						}
						return cmd.Println(shellFunctionDoc(def, fn))

					case len(args) == 0:
						if !def.HasModule() {
							return fmt.Errorf("module not loaded.\nUse %q to see what's available", shellStdlibCmdName)
						}
						// Document module
						// Example: `.doc [module]`
						return cmd.Println(shellModuleDoc(st, def))
					}
				}

				t, err := st.GetTypeDef(def)
				if err != nil {
					return err
				}

				// Document type
				// Example: `container | .doc`
				if len(args) == 0 {
					return cmd.Println(shellTypeDoc(t))
				}

				fp := t.AsFunctionProvider()
				if fp == nil {
					return fmt.Errorf("type %q does not provide functions", t.String())
				}

				// Document function from type
				// Example: `container | .doc with-exec`
				fn, err := def.GetFunction(fp, args[0])
				if err != nil {
					return err
				}
				return cmd.Println(shellFunctionDoc(def, fn))
			},
		},
		&ShellCommand{
			Use: ".use <module>",
			Description: `Set a module as the default for the session

Local module paths are resolved relative to the workdir on the host, not relative
to the currently loaded module.
`,
			GroupID: moduleGroup.ID,
			Args:    ExactArgs(1),
			State:   NoState,
			Run: func(cmd *ShellCommand, args []string, _ *ShellState) error {
				st, err := h.getOrInitDefState(args[0], func() (*moduleDef, error) {
					return initializeModule(cmd.Context(), h.dag, args[0], true)
				})
				if err != nil {
					return err
				}

				if st.ModRef != h.modRef {
					h.modRef = st.ModRef
				}

				return nil
			},
		},
		&ShellCommand{
			Use:         shellDepsCmdName,
			Description: "Dependencies from the module loaded in the current context",
			GroupID:     moduleGroup.ID,
			Args:        NoArgs,
			State:       NoState,
			Run: func(cmd *ShellCommand, _ []string, _ *ShellState) error {
				_, err := h.GetModuleDef(nil)
				if err != nil {
					return err
				}
				return cmd.Send(h.newDepsState())
			},
			Complete: func(ctx *CompletionContext, _ []string) *CompletionContext {
				return &CompletionContext{
					Completer: ctx.Completer,
					CmdRoot:   shellDepsCmdName,
					root:      true,
				}
			},
		},
		&ShellCommand{
			Use:         shellStdlibCmdName,
			Description: "Standard library functions",
			Args:        NoArgs,
			State:       NoState,
			Run: func(cmd *ShellCommand, _ []string, _ *ShellState) error {
				return cmd.Send(h.newStdlibState())
			},
			Complete: func(ctx *CompletionContext, _ []string) *CompletionContext {
				return &CompletionContext{
					Completer: ctx.Completer,
					CmdRoot:   shellStdlibCmdName,
					root:      true,
				}
			},
		},
		&ShellCommand{
			Use:         ".core [function]",
			Description: "Load any core Dagger type",
			State:       NoState,
			Run: func(cmd *ShellCommand, args []string, _ *ShellState) error {
				return cmd.Send(h.newCoreState())
			},
			Complete: func(ctx *CompletionContext, _ []string) *CompletionContext {
				return &CompletionContext{
					Completer: ctx.Completer,
					CmdRoot:   shellCoreCmdName,
					root:      true,
				}
			},
		},
		cobraToShellCommand(loginCmd),
		cobraToShellCommand(logoutCmd),
		cobraToShellCommand(moduleInstallCmd),
		cobraToShellCommand(moduleUnInstallCmd),
		// TODO: Add update command when available:
		// - https://github.com/dagger/dagger/pull/8839
	)

	def := h.modDef(nil)

	for _, fn := range def.GetCoreFunctions() {
		// TODO: Don't hardcode this list.
		promoted := []string{
			"cache-volume",
			"container",
			"directory",
			"engine",
			"git",
			"host",
			"http",
			"set-secret",
			"version",
		}

		if !slices.Contains(promoted, fn.CmdName()) {
			continue
		}

		stdlib = append(stdlib,
			&ShellCommand{
				Use:         shellFunctionUseLine(def, fn),
				Description: fn.Description,
				State:       NoState,
				HelpFunc: func(cmd *ShellCommand) string {
					return shellFunctionDoc(def, fn)
				},
				Run: func(cmd *ShellCommand, args []string, _ *ShellState) error {
					ctx := cmd.Context()

					st := h.newState()
					st, err := h.functionCall(ctx, st, fn.CmdName(), args)
					if err != nil {
						return err
					}

					return cmd.Send(st)
				},
				Complete: func(ctx *CompletionContext, args []string) *CompletionContext {
					return &CompletionContext{
						Completer:   ctx.Completer,
						ModFunction: fn,
						root:        true,
					}
				},
			},
		)
	}

	sort.Slice(builtins, func(i, j int) bool {
		return builtins[i].Use < builtins[j].Use
	})

	sort.Slice(stdlib, func(i, j int) bool {
		return stdlib[i].Use < stdlib[j].Use
	})

	h.builtins = builtins
	h.stdlib = stdlib
}

func cobraToShellCommand(c *cobra.Command) *ShellCommand {
	return &ShellCommand{
		Use:         "." + c.Use,
		Description: c.Short,
		GroupID:     c.GroupID,
		State:       NoState,
		Run: func(cmd *ShellCommand, args []string, _ *ShellState) error {
			// Re-execute the dagger command (hack)
			args = append([]string{cmd.CleanName()}, args...)
			ctx := cmd.Context()
			hctx := interp.HandlerCtx(ctx)
			c := exec.CommandContext(ctx, "dagger", args...)
			c.Stdout = hctx.Stdout
			c.Stderr = hctx.Stderr
			c.Stdin = hctx.Stdin
			return c.Run()
		},
	}
}
