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
	"sort"
	"strings"
	"text/tabwriter"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"github.com/adrg/xdg"
	"github.com/chzyer/readline"
	"github.com/dagger/dagger/engine/client"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// shellCode is the code to be executed in the shell command
var shellCode string

// shellStatePrefix is the prefix that identifies a shell state in input/output
const shellStatePrefix = "DSH:"

const shellHandlerExit = 200

func init() {
	shellCmd.Flags().StringVarP(&shellCode, "code", "c", "", "command to be executed")
}

var shellCmd = &cobra.Command{
	Use:   "shell [options] [file...]",
	Short: "Run an interactive dagger shell",
	RunE: func(cmd *cobra.Command, args []string) error {
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			// TODO: this should be moved into the handler to avoid the unnecessary
			// work on certain cases (e.g., the .help builtin), but also probably
			// needs to be scoped by module when we support calling from a dependency,
			// since its types are different.
			modDef, err := initializeModule(ctx, dag, true)
			if err != nil {
				return fmt.Errorf("error initializing module: %w", err)
			}
			handler := &shellCallHandler{
				dag:    dag,
				mod:    modDef,
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

	// mod has the module type definitions from introspection
	mod *moduleDef

	// cfg holds the final values for the module's constructor, i.e., the module configuration
	cfg map[string]any

	// switch to Frontend.Background for rendering output while the TUI is
	// running when in interactive mode
	term bool

	// stdoutBuf is used to capture the final stdout that the runner produces
	stdoutBuf *bytes.Buffer

	// stderrBuf is used to capture the final stderr that the runner produces
	stderrBuf *bytes.Buffer

	// debug writes to the handler context's stderr what the arguments, input,
	// and output are for each command that the exec handler processes
	debug bool

	builtins []*ShellCommand
}

// RunAll is the entry point for the shell command
//
// It creates the runner and dispatches the execution to different modes:
// - Interactive: when no arguments are provided
// - File: when a file path is provided as an argument
// - Code: when code is passed inline using the `-c,--code` flag or via stdin
func (h *shellCallHandler) RunAll(ctx context.Context, args []string) error {
	h.term = !silent && (hasTTY && progress == "auto" || progress == "tty")

	h.stdoutBuf = new(bytes.Buffer)
	h.stderrBuf = new(bytes.Buffer)

	r, err := interp.New(
		interp.StdIO(nil, h.stdoutBuf, h.stderrBuf),
		// interp.Params("-e", "-u", "-o", "pipefail"),

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
	h.registerBuiltins()

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
		return h.run(ctx, os.Stdin, "")
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

// run parses code and and executes the interpreter's Runner
func (h *shellCallHandler) run(ctx context.Context, reader io.Reader, name string) error {
	file, err := syntax.NewParser().Parse(reader, name)
	if err != nil {
		return err
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

	h.runner.Reset()
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

	s, b, err := readShellState(h.stdoutBuf)
	if err != nil {
		return err
	}
	if s != nil {
		return h.Result(ctx, *s)
	}

	return h.withTerminal(func(_ io.Reader, stdout, _ io.Writer) error {
		_, err := fmt.Fprint(stdout, string(b))
		return err
	})
}

// runPath executes code from a file
func (h *shellCallHandler) runPath(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
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
		Frontend.SetPrimary(trace.SpanID{})
		Frontend.Opts().CustomExit = func() {}

		if runErr != nil {
			h.withTerminal(func(_ io.Reader, _, stderr io.Writer) error {
				fmt.Fprintf(stderr, "Error: %s\n", runErr.Error())
				return nil
			})
			// Reset runError for next command
			runErr = nil
		}

		var line string

		err := h.withTerminal(func(stdin io.Reader, stdout, stderr io.Writer) error {
			var err error
			if rl == nil {
				cfg, err := loadReadlineConfig()
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
		Frontend.SetPrimary(span.SpanContext().SpanID())
		Frontend.Opts().CustomExit = cancel
		runErr = h.run(ctx, strings.NewReader(line), "")
		if runErr != nil {
			span.SetStatus(codes.Error, runErr.Error())
		}
		span.End()
	}

	return nil
}

func loadReadlineConfig() (*readline.Config, error) {
	dataRoot := filepath.Join(xdg.DataHome, "dagger")
	err := os.MkdirAll(dataRoot, 0o700)
	if err != nil {
		return nil, err
	}

	return &readline.Config{
		// We need a prompt that conveys the unique nature of the Dagger shell. Per gpt4:
		// The ⋈ symbol, known as the bowtie, has deep roots in relational databases and set theory,
		// where it denotes a join operation. This makes it especially fitting for a DAG environment,
		// as it suggests the idea of dependencies, intersections, and points where separate paths
		// or data sets come together.
		Prompt:       "⋈ ",
		HistoryFile:  filepath.Join(dataRoot, "histfile"),
		HistoryLimit: 1000,
	}, nil
}

// withTerminal handles using stdin, stdout, and stderr when the TUI is runnin
func (h *shellCallHandler) withTerminal(fn func(stdin io.Reader, stdout, stderr io.Writer) error) error {
	if h.term {
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
			shellLogf(ctx, "[DBG] Exec(%v)[%d]\n", args, len(args))
		}

		// This avoids interpreter builtins running first, which would make it
		// impossible to have a function named "echo", for example. We can
		// remove `.dag` from this point onward.
		if args[0] == ".dag" {
			args = args[1:]
		}

		err := h.cmd(ctx, args)
		if err != nil {
			m := err.Error()
			if h.debug {
				shellLogf(ctx, "[DBG] Error(%v): %s\n", args, m)
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

// cmd is tt he main logic for executing simple commands
func (h *shellCallHandler) cmd(ctx context.Context, args []string) error {
	var st *ShellState
	var err error

	builtin, err := h.BuiltinCommand(args[0])
	if err != nil {
		return err
	}

	if isFirstShellCommand(ctx) {
		if builtin != nil {
			return builtin.Execute(ctx, args[1:], nil)
		}
		st, err = h.entrypointCall(ctx, args)
		if err != nil {
			return err
		}
	} else {
		var b []byte
		st, b, err = shellState(ctx)
		if err != nil {
			return err
		}
		if st == nil {
			if h.debug {
				shellLogf(ctx, "[DBG] IN(%v): %q\n", args, string(b))
			}
			return fmt.Errorf("unexpected input for command %q", args[0])
		}
	}

	if h.debug {
		shellLogf(ctx, "[DBG] IN(%v): %v\n", args, st)
	}

	if builtin != nil {
		return builtin.Execute(ctx, args[1:], st)
	}

	st, err = h.functionCall(ctx, st, args[0], args[1:])
	if err != nil {
		return err
	}

	if h.debug {
		shellLogf(ctx, "[DBG] OUT(%v): %v\n", args, st)
	}

	return st.Write(ctx)
}

// entrypointCall is executed when it's the first in a command pipeline
func (h *shellCallHandler) entrypointCall(ctx context.Context, args []string) (*ShellState, error) {
	if h.debug {
		shellLogf(ctx, "[DBG] └ Entrypoint(%v)\n", args)
	}

	// 1. Same-module call (eg. 'build')
	if h.mod.HasFunction(args[0]) {
		fn := h.mod.MainObject.AsObject.Constructor
		expected := len(fn.RequiredArgs())
		actual := len(h.cfg)

		if expected > actual {
			return nil, fmt.Errorf(`missing %d required argument(s) for the module. Use ".config [options]" to set them`, expected-actual)
		}

		return ShellState{}.WithCall(fn, h.cfg), nil
	}

	// 2. Core function call (eg. 'git')
	if h.mod.HasCoreFunction(args[0]) {
		return &ShellState{}, nil
	}

	// TODO: 3. Dependency short name (eg. 'wolfi container')

	return nil, fmt.Errorf("there is no module or core function %q", args[0])
}

// functionCall is executed for every command that the exec handler processes
func (h *shellCallHandler) functionCall(ctx context.Context, prev *ShellState, name string, args []string) (*ShellState, error) {
	call := prev.Function()

	fn, err := call.GetNextDef(h.mod, name)
	if err != nil {
		return prev, err
	}

	argValues, err := h.parseArgumentValues(ctx, fn, args)
	if err != nil {
		return prev, fmt.Errorf("could not parse arguments for function %q: %w", fn.CmdName(), err)
	}

	return prev.WithCall(fn, argValues), nil
}

// parseArgumentValues returns a map of argument names and their parsed values
func (h *shellCallHandler) parseArgumentValues(ctx context.Context, fn *modFunction, args []string) (map[string]any, error) {
	req := fn.RequiredArgs()

	// Required args in dagger shell are positional but we have a lot of power
	// in custom flags that we want to reuse, so just add the corresponding
	// `--flag-name` args in order for pflags to be able to parse them.
	pos := make([]string, 0, len(req)*2)
	for i, arg := range args {
		if strings.HasPrefix(arg, "--") {
			break
		}
		if i >= len(req) {
			return nil, fmt.Errorf("too many positional arguments: expected %d", len(req))
		}
		pos = append(pos, "--"+req[i].FlagName(), arg)
	}

	if len(req) > len(pos)/2 {
		numMissing := len(req) - len(pos)/2
		missing := make([]string, 0, numMissing)
		for _, arg := range req[len(req)-numMissing:] {
			missing = append(missing, arg.FlagName())
		}
		return nil, fmt.Errorf("missing %d positional argument(s): %s", numMissing, strings.Join(missing, ", "))
	}

	rem := args[len(req):]

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
		if strings.HasPrefix(value, shellStatePrefix) {
			v, replace, err := h.parseStateArgument(ctx, a, value)
			if err != nil {
				return fmt.Errorf("failed expanding argument %q: %w", a.FlagName(), err)
			}
			// Flags only support setting their values from strings, so if
			// anything else is returned, we just ignore it.
			// TODO: try to validate this more to avoid surprises
			if sval, ok := v.(string); ok && !replace {
				return flags.Set(flag.Name, sval)
			}
			// This will bypass using a flag for this argument since we're
			// saying it's a final value alreadyl
			if replace {
				values[a.Name] = v
			}
			return nil
		}
		return flags.Set(flag.Name, value)
	}
	if err := flags.ParseAll(append(pos, rem...), f); err != nil {
		return nil, err
	}

	// Finally, get the values from the flags that haven't been resolved yet.
	for _, a := range fn.Args {
		if _, exists := values[a.Name]; exists || a.IsUnsupportedFlag() {
			continue
		}
		flag, err := a.GetFlag(flags)
		if err != nil {
			return nil, err
		}
		if !flag.Changed {
			continue
		}
		v, err := a.GetFlagValue(ctx, flag, h.dag, h.mod)
		if err != nil {
			return nil, err
		}
		values[a.Name] = v
	}

	return values, nil
}

func (h *shellCallHandler) parseStateArgument(ctx context.Context, arg *modFunctionArg, value string) (any, bool, error) {
	// Does this replace the source value or do we pass it on to flag parsing?
	var replace bool

	st, b, err := readShellState(strings.NewReader(value))
	if err != nil {
		return nil, replace, err
	}
	// Not state, but has some other content
	if st == nil && len(b) > 0 {
		return string(b), replace, nil
	}
	fn, err := st.Function().GetDef(h.mod)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get function definition: %w", err)
	}

	q := st.QueryBuilder(h.dag)

	// When an argument returns an object, assume we want its ID
	// TODO: Allow ids in TypeDefs so we can directly check if there's an `id`
	// function in this object.
	if fn.ReturnType.AsFunctionProvider() != nil {
		if st.Function().ReturnObject != arg.TypeDef.Name() {
			return nil, replace, fmt.Errorf("expected return type %q, got %q", arg.TypeDef.Name(), st.Function().ReturnObject)
		}
		q = q.Select("id")
		replace = true
	}

	// TODO: do a bit more validation. Consider that values that are not
	// to be replaced should only be strings, because that's what the
	// flagSet supports. This also means the type won't match the expected
	// definition. For example, a function that returns a `Directory` object
	// could have a subshell return a path string so the flag will turn that
	// into the `Directory` object.

	var response any
	err = makeRequest(ctx, q, &response)
	return response, replace, err
}

// Result handles making the final request and printing the response
func (h *shellCallHandler) Result(ctx context.Context, s ShellState) error {
	prev := s.Function()

	fn, err := prev.GetDef(h.mod)
	if err != nil {
		return err
	}

	sel := s.QueryBuilder(h.dag)
	q, err := handleObjectLeaf(ctx, sel, fn.ReturnType)
	if err != nil {
		return err
	}

	return h.executeRequest(ctx, q, fn.ReturnType)
}

func (h *shellCallHandler) executeRequest(ctx context.Context, q *querybuilder.Selection, returnType *modTypeDef) error {
	if q == nil {
		return h.withTerminal(func(stdin io.Reader, stdout, stderr io.Writer) error {
			return handleResponse(returnType, nil, stdout, stderr)
		})
	}

	var response any

	if err := makeRequest(ctx, q, &response); err != nil {
		return err
	}

	return h.withTerminal(func(stdin io.Reader, stdout, stderr io.Writer) error {
		return handleResponse(returnType, response, stdout, stderr)
	})
}

func shellLogf(ctx context.Context, msg string, args ...any) {
	hctx := interp.HandlerCtx(ctx)
	fmt.Fprintf(hctx.Stderr, msg, args...)
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

	var s ShellState
	if err := json.NewDecoder(decoder).Decode(&s); err != nil {
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
	Calls []FunctionCall `json:"calls,omitempty"`
	Error *string        `json:"error,omitempty"`
}

func (s ShellState) IsError() bool {
	return s.Error != nil
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
func (s ShellState) Write(ctx context.Context) error {
	return s.WriteTo(interp.HandlerCtx(ctx).Stdout)
}

func (s ShellState) WriteTo(w io.Writer) error {
	var buf bytes.Buffer

	// Encode state in base64 to avoid issues with spaces being turned into
	// multiple arguments when the result of a command subsitution.
	bEnc := base64.NewEncoder(base64.StdEncoding, &buf)
	jEnc := json.NewEncoder(bEnc)

	if err := jEnc.Encode(s); err != nil {
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
func (s ShellState) Function() FunctionCall {
	if len(s.Calls) == 0 {
		// The first call is a field under Query.
		return FunctionCall{
			ReturnObject: "Query",
		}
	}
	return s.Calls[len(s.Calls)-1]
}

// WithCall returns a new state with the given function call added to the chain
func (s ShellState) WithCall(fn *modFunction, argValues map[string]any) *ShellState {
	prev := s.Function()
	return &ShellState{
		Calls: append(s.Calls, FunctionCall{
			Object:       prev.ReturnObject,
			Name:         fn.Name,
			ReturnObject: fn.ReturnType.Name(),
			Arguments:    argValues,
		}),
	}
}

// QueryBuilder returns a querybuilder.Selection from the shell state
func (s ShellState) QueryBuilder(dag *dagger.Client) *querybuilder.Selection {
	q := querybuilder.Query().Client(dag.GraphQLClient())
	for _, call := range s.Calls {
		q = q.Select(call.Name)
		for n, v := range call.Arguments {
			q = q.Arg(n, v)
		}
	}
	return q
}

// GetDef returns the introspection definition for this function call
func (f FunctionCall) GetDef(modDef *moduleDef) (*modFunction, error) {
	return modDef.GetObjectFunction(f.Object, f.Name)
}

// GetNextDef returns the introspection definition for the next function call, based on
// the current return type and name of the next function
func (f FunctionCall) GetNextDef(modDef *moduleDef, name string) (*modFunction, error) {
	if f.ReturnObject == "" {
		return nil, fmt.Errorf("cannot chain %q after %q returning a non-object type", name, f.Name)
	}
	return modDef.GetObjectFunction(f.ReturnObject, name)
}

// ShellCommand is a Dagger Shell builtin command
type ShellCommand struct {
	// Use is the one-line usage message.
	Use string
	//
	// Short is the short description shown in the '.help' output.
	Short string

	// Args is the expected number of positional arguments.
	Args int

	// Run is the function that will be executed if it's the first command
	// in the pipeline and RunState is not defined.
	Run func(cmd *ShellCommand, args []string) error

	// RunState is the function for executing a command that can be chained
	// in a pipeline.
	//
	// If defined, it's always used, even if it's the first command in the
	// pipeline. For commands that should only be the first, define `Run` instead.
	RunState func(cmd *ShellCommand, args []string, st *ShellState) error

	// Hidden hides the command from `.help`.
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

func (c *ShellCommand) Printer() io.Writer {
	return c.out
}

func (c *ShellCommand) Execute(ctx context.Context, args []string, st *ShellState) error {
	if st != nil && c.RunState == nil {
		return fmt.Errorf("command %q cannot be chained", c.Name())
	}
	if len(args) < c.Args {
		return fmt.Errorf("usage: %s", c.Use)
	}
	c.SetContext(ctx)
	if c.RunState != nil {
		return c.RunState(c, args, st)
	}
	return c.Run(c, args)
}

func (h *shellCallHandler) BuiltinCommand(name string) (*ShellCommand, error) {
	if name == "." || !strings.HasPrefix(name, ".") {
		return nil, nil
	}
	for _, c := range h.builtins {
		if c.Name() == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("no such command: %s", name)
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

func (h *shellCallHandler) addBuiltin(cmds ...*ShellCommand) {
	h.builtins = append(h.builtins, cmds...)
}

func (h *shellCallHandler) registerBuiltins() { //nolint:gocyclo
	h.addBuiltin(
		&ShellCommand{
			Use:    ".debug",
			Hidden: true,
			Run: func(cmd *ShellCommand, args []string) error {
				// Toggles debug mode, which can be useful when in interactive mode
				h.debug = !h.debug
				return nil
			},
		},
		&ShellCommand{
			Use:   ".help",
			Short: "print this help message",
			Run: func(cmd *ShellCommand, args []string) error {
				line := nameShortWrapped(h.Builtins(), func(c *ShellCommand) (string, string) {
					return c.Name(), c.Short
				})
				return cmd.Println(line)
			},
		},
		&ShellCommand{
			Use:   ".config [options]",
			Short: "set module constructor options",
			Args:  1, // at least one, i..e, not empty
			Run: func(cmd *ShellCommand, args []string) error {
				cfg, err := h.parseArgumentValues(cmd.Context(), h.mod.MainObject.AsObject.Constructor, args[1:])
				if err != nil {
					return err
				}
				h.cfg = cfg
				return nil
			},
		},
		&ShellCommand{
			Use:   ".deps",
			Short: "list dependencies",
			Run: func(cmd *ShellCommand, args []string) error {
				ctx := cmd.Context()
				deps, err := h.mod.Source.AsModule().Dependencies(ctx)
				if err != nil {
					return err
				}
				w := tabwriter.NewWriter(cmd.Printer(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "NAME\tDESCRIPTION")
				fmt.Fprintln(w, " \t ")
				for _, dep := range deps {
					name, err := dep.Name(ctx)
					if err != nil {
						return err
					}
					desc, err := dep.Description(ctx)
					if err != nil {
						return err
					}
					shortDesc := strings.SplitN(desc, "\n", 2)[0]
					fmt.Fprintf(w, "%s\t%s\n", name, shortDesc)
				}
				return w.Flush()
			},
		},
		&ShellCommand{
			Use:   ".functions",
			Short: "list available functions",
			Run: func(cmd *ShellCommand, args []string) error {
				return functionListRun(
					h.mod.MainObject.AsFunctionProvider(),
					cmd.Printer(),
					false,
				)
			},
		},
		&ShellCommand{
			Use:   ".core [function]",
			Short: "load a core Dagger type",
			Run: func(cmd *ShellCommand, args []string) error {
				ctx := cmd.Context()

				if len(args) == 0 {
					return functionListRun(
						h.mod.GetFunctionProvider("Query"),
						cmd.Printer(),
						false,
					)
				}

				st := &ShellState{}
				st, err := h.functionCall(ctx, st, args[0], args[1:])
				if err != nil {
					return err
				}

				return cmd.Send(st)
			},
		},
		&ShellCommand{
			Use:   ".git <url>",
			Short: "load a directory from a git URL",
			Args:  1,
			Run: func(cmd *ShellCommand, args []string) error {
				ctx := cmd.Context()

				gitURL, err := parseGitURL(args[0])
				if err != nil {
					return err
				}
				gitDir := makeGitDirectory(gitURL, h.dag)

				// It would be nice to get the querybuilder from `dagger.Directory`
				// instance. That way we could return the object directly instead
				// of via the ID.
				id, err := gitDir.ID(ctx)
				if err != nil {
					return err
				}

				core := ShellState{}

				// Could use h.functionCall but this avoids passing the id through
				// h.parseArgumentValues which adds unnecessary complication to
				// bypass the flag parsing.
				fn, err := core.Function().GetNextDef(h.mod, "load-directory-from-id")
				if err != nil {
					return err
				}

				values := map[string]any{"id": string(id)}
				return cmd.Send(core.WithCall(fn, values))
			},
		},
	)

	coreAlias := func(cmd *ShellCommand, args []string) error {
		ctx := cmd.Context()

		st := &ShellState{}
		st, err := h.functionCall(ctx, st, cmd.CleanName(), args)
		if err != nil {
			return err
		}

		return cmd.Send(st)
	}

	h.addBuiltin(
		&ShellCommand{
			Use:   ".container",
			Short: "create a new container",
			Run:   coreAlias,
		},
		&ShellCommand{
			Use:   ".directory",
			Short: "create a new directory",
			Run:   coreAlias,
		},
		&ShellCommand{
			Use:   ".http",
			Short: "download a file over http",
			Run:   coreAlias,
		},
	)

	// Re-execute the dagger command (hack)
	reexec := func(cmd *ShellCommand, args []string) error {
		args = append([]string{cmd.Name()}, args...)
		ctx := cmd.Context()
		hctx := interp.HandlerCtx(ctx)
		c := exec.CommandContext(ctx, "dagger", args...)
		c.Stdout = hctx.Stdout
		c.Stderr = hctx.Stderr
		c.Stdin = hctx.Stdin
		return c.Run()
	}

	h.addBuiltin(
		&ShellCommand{
			Use:   ".install <module>",
			Short: "install a dependency",
			Args:  1,
			Run:   reexec,
		},
		&ShellCommand{
			Use:   ".uninstall <module>",
			Short: "uninstall a dependency",
			Args:  1,
			Run:   reexec,
		},
		&ShellCommand{
			Use:   ".login",
			Short: "login to Dagger Cloud",
			Run:   reexec,
		},
		&ShellCommand{
			Use:   ".logout",
			Short: "logout from Dagger Cloud",
			Run:   reexec,
		},
	)

	sort.Slice(h.builtins, func(i, j int) bool {
		return h.builtins[i].Use < h.builtins[j].Use
	})
}
