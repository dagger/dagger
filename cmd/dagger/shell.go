package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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

var shellCode string

// shellStatePrefix is the prefix that identifies a shell state in input/output
const shellStatePrefix = "DSH:"

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
				// debug: true,
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
}

// RunAll is the entry point for the shell command
//
// It creates the runner and dispatches the execution to different modes:
// - Interactive: when no arguments are provided
// - File: when a file path is provided as an argument
// - Code: when code is passed inline using the `-c,--code` flag or via stdin
func (h *shellCallHandler) RunAll(ctx context.Context, args []string) error {
	h.stdoutBuf = new(bytes.Buffer)
	h.stderrBuf = new(bytes.Buffer)

	r, err := interp.New(
		interp.StdIO(nil, h.stdoutBuf, h.stderrBuf),
		interp.ExecHandlers(h.Exec),
	)
	if err != nil {
		return err
	}
	h.runner = r

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

// run parses code and and executes the interpreter's Runner
func (h *shellCallHandler) run(ctx context.Context, reader io.Reader, name string) error {
	file, err := syntax.NewParser().Parse(reader, name)
	if err != nil {
		return err
	}

	h.stdoutBuf.Reset()
	h.runner.Reset()

	err = h.runner.Run(ctx, file)
	if exit, ok := interp.IsExitStatus(err); ok {
		return ExitError{Code: int(exit)}
	}
	if err != nil {
		return err
	}

	// Reading state may advance the buffer's position so just copy it now
	// in case it's not a state value so we can print it.
	output := h.stdoutBuf.String()

	s, err := readShellState(h.stdoutBuf)
	if err != nil {
		return err
	}
	if s != nil {
		return h.Result(ctx, *s)
	}

	return h.withTerminal(func(_ io.Reader, stdout, _ io.Writer) error {
		_, err := fmt.Fprint(stdout, output)
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
	h.term = stdoutIsTTY
	h.withTerminal(func(_ io.Reader, _, stderr io.Writer) error {
		fmt.Fprintln(stderr, `Dagger interactive shell. Type ".help" for more information.`)
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

		if h.stderrBuf.Len() > 0 {
			h.withTerminal(func(_ io.Reader, _, stderr io.Writer) error {
				fmt.Fprint(stderr, h.stderrBuf.String())
				h.stderrBuf.Reset()
				return nil
			})
		}
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
			if errors.Is(err, io.EOF) || errors.Is(err, readline.ErrInterrupt) {
				Frontend.Opts().Verbosity = 0
				Frontend.Opts().CustomExit = nil
				break
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

// Exec is the main handler function for the runner to execute simple commands
func (h *shellCallHandler) Exec(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		if h.debug {
			shellLogf(ctx, "[DBG] args: %v\n", args)
		}

		s, err := shellState(ctx)
		if err != nil {
			return err
		}

		if h.debug {
			shellLogf(ctx, "[DBG] input: %v\n", s)
		}

		// First command in pipe line: e.g., `cmd1 | cmd2 | cmd3`
		if s == nil {
			if strings.HasPrefix(args[0], ".") {
				return h.Builtin(ctx, args)
			}
			s, err = h.entrypointCall(ctx, args)
			if err != nil {
				return err
			}
		}

		s, err = h.call(ctx, s, args[0], args[1:])
		if err != nil {
			return err
		}

		if h.debug {
			shellLogf(ctx, "[DBG] output: %v\n", s)
		}

		return s.Write(ctx)
	}
}

// entrypointCall is executed when it's the first in a command pipeline
func (h *shellCallHandler) entrypointCall(ctx context.Context, args []string) (*ShellState, error) { //nolint:unparam
	// shellLogf(ctx, "[%s] ENTRYPOINT!!\n", args[0])

	// You're the entrypoint
	// 1. Interpret args as same-module call (eg. 'build')
	// 2. If no match: interpret args as core function call (eg. 'git')
	// 3. If no match (to be done later): interpret args as dependency short name (eg. 'wolfi container')
	// --> craft the call

	obj := h.mod.MainObject.AsObject
	//
	// TODO: constructor arguments
	return ShellState{}.WithCall(obj.Constructor, h.cfg), nil
}

// call is executed for every command that the exec handler processes
func (h *shellCallHandler) call(ctx context.Context, prev *ShellState, name string, args []string) (*ShellState, error) {
	call := prev.Function()

	fn, err := call.GetNextDef(h.mod, name)
	if err != nil {
		return nil, err
	}

	argValues, err := h.argumentValues(ctx, fn, args)
	if err != nil {
		return nil, fmt.Errorf("could not parse arguments for function %q: %w", fn.CmdName(), err)
	}

	return prev.WithCall(fn, argValues), nil
}

// argumentValues returns a map of argument names and their parsed values
func (h *shellCallHandler) argumentValues(ctx context.Context, fn *modFunction, args []string) (map[string]any, error) {
	flags := pflag.NewFlagSet(fn.CmdName(), pflag.ContinueOnError)

	// TODO: Handle "stitching", i.e., some arguments could be encoded ShellState values
	var reqArgs []*modFunctionArg
	for _, argDef := range fn.SupportedArgs() {
		if err := argDef.AddFlag(flags); err != nil {
			return nil, fmt.Errorf("error addding flag: %w", err)
		}
		if argDef.IsRequired() {
			reqArgs = append(reqArgs, argDef)
		}
	}

	if len(reqArgs) > len(args) {
		return nil, fmt.Errorf("not enough arguments in %q: expected %d, got %d", fn.Name, len(reqArgs), len(args))
	}

	// Required args here are positional but we have a lot of power in our
	// custom flags, so to take advantage of them just add the corresponding
	// `--flag-name` args so pflags can parse them.
	fargs := make([]string, 0, len(reqArgs)+len(args))
	for i, arg := range reqArgs {
		fargs = append(fargs, "--"+arg.flagName, args[i])
	}
	fargs = append(fargs, args[len(reqArgs):]...)

	if len(fargs) == 0 {
		return nil, nil
	}

	if err := flags.Parse(fargs); err != nil {
		return nil, fmt.Errorf("error parsing flags: %w", err)
	}

	a := make(map[string]any)
	err := fn.WalkValues(ctx, flags, h.mod, h.dag, func(argDef *modFunctionArg, val any) {
		a[argDef.Name] = val
	})

	return a, err
}

// Result handles making the final request and printing the response
func (h *shellCallHandler) Result(ctx context.Context, s ShellState) error {
	prev := s.Function()
	if prev == nil {
		return fmt.Errorf("no function call found for command")
	}

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

func shellLog(ctx context.Context, msg string) {
	hctx := interp.HandlerCtx(ctx)
	fmt.Fprint(hctx.Stderr, msg)
}

func shellLogf(ctx context.Context, msg string, args ...any) {
	hctx := interp.HandlerCtx(ctx)
	fmt.Fprintf(hctx.Stderr, msg, args...)
}

func shellWrite(ctx context.Context, msg string) {
	hctx := interp.HandlerCtx(ctx)
	fmt.Fprint(hctx.Stdout, msg)
}

func shellState(ctx context.Context) (*ShellState, error) {
	return readShellState(interp.HandlerCtx(ctx).Stdin)
}

// readShellState deserializes shell state
//
// We use an hardocded prefix when writing and reading state to make it easy
// to detect if a given input is a shell state or not. This way we can tell
// the difference between a serialized state that failed to unmarshal and
// non-state data.
func readShellState(input io.Reader) (*ShellState, error) {
	if f, ok := input.(*os.File); ok && f == nil {
		return nil, nil
	}
	r := bufio.NewReader(input)
	n := len(shellStatePrefix)
	peeked, err := r.Peek(n)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("check state: %w", err)
	}
	if string(peeked) != shellStatePrefix {
		return nil, nil
	}
	_, _ = r.Discard(n)
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var s ShellState
	err = json.Unmarshal(b, &s)
	if err != nil {
		return nil, fmt.Errorf("read state: %w (%s)", err, string(b))
	}
	return &s, nil
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
	Calls []FunctionCall `json:"calls"`
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
	htcx := interp.HandlerCtx(ctx)
	fmt.Fprint(htcx.Stdout, shellStatePrefix)
	return json.NewEncoder(htcx.Stdout).Encode(s)
}

// Function returns the last function in the chain, if not empty
func (s ShellState) Function() *FunctionCall {
	if len(s.Calls) == 0 {
		return nil
	}
	return &s.Calls[len(s.Calls)-1]
}

// WithCall returns a new state with the given function call added to the chain
func (s ShellState) WithCall(fn *modFunction, argValues map[string]any) *ShellState {
	var typeName string
	if prev := s.Function(); prev != nil {
		typeName = prev.ReturnObject
	} else {
		// If it's the first call, it's a field under Query
		typeName = "Query"
	}
	return &ShellState{
		Calls: append(s.Calls, FunctionCall{
			Object:       typeName,
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
	return modDef.GetObjectFunction(f.ReturnObject, name)
}

// Re-execute the dagger command (hack)
func reexec(ctx context.Context, args []string) error {
	hctx := interp.HandlerCtx(ctx)
	cmd := exec.CommandContext(ctx, "dagger", args...)
	cmd.Stdout = hctx.Stdout
	cmd.Stderr = hctx.Stderr
	cmd.Stdin = hctx.Stdin
	return cmd.Run()
}

// TODO: Some builtins may make sense only in certain cases, for example, only
// when in interactive, or only as a first argument vs anywhere in the chain.
func (h *shellCallHandler) Builtin(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no specified builtin")
	}
	args[0] = strings.TrimPrefix(args[0], ".")
	switch args[0] {
	case "help":
		shellWrite(ctx, `
.functions    list available functions
.deps         list dependencies
.core         load a core Dagger type
.install      install a dependency
.uninstall    uninstall a dependency
.login        login to Dagger Cloud
.logout       logout from Dagger Cloud
.help         print this help message
`[1:])
		return nil
	case "git":
		if len(args) < 2 {
			return fmt.Errorf("usage: .git URL")
		}
		gitURL, err := parseGitURL(args[1])
		if err != nil {
			return err
		}
		ref := gitURL.Ref
		if ref == "" {
			ref = "main"
		}
		subdir := gitURL.Path
		gitURL.Ref = ""
		gitURL.Path = ""

		s := &ShellState{
			Calls: []FunctionCall{
				{
					Object: "Query",
					Name:   "git",
					Arguments: map[string]any{
						"url": gitURL.String(),
					},
					ReturnObject: "GitRepository",
				},
				{
					Object: "GitRepository",
					Name:   "ref",
					Arguments: map[string]interface{}{
						"name": ref,
					},
					ReturnObject: "GitRef",
				},
				{
					Object:       "GitRef",
					Name:         "tree",
					ReturnObject: "Directory",
				},
				{
					Object: "Directory",
					Name:   "directory",
					Arguments: map[string]interface{}{
						"path": subdir,
					},
					ReturnObject: "Directory",
				},
			},
		}
		return s.Write(ctx)
	case "container":
		if len(args) < 2 {
			return fmt.Errorf("usage: .container REF")
		}
		s := &ShellState{
			Calls: []FunctionCall{
				{
					Object:       "Query",
					Name:         "container",
					ReturnObject: "Container",
				},
				{
					Object: "Container",
					Name:   "from",
					Arguments: map[string]interface{}{
						"address": args[1],
					},
					ReturnObject: "Container",
				},
			},
		}
		return s.Write(ctx)
	case "install", "uninstall":
		if len(args) < 1 {
			return fmt.Errorf("usage: .%s <module>", args[0])
		}
		return reexec(ctx, args)
	case "login", "logout":
		return reexec(ctx, args)
	case "deps":
		deps, err := h.mod.Source.AsModule().Dependencies(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(interp.HandlerCtx(ctx).Stdout, 0, 0, 2, ' ', 0)
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

	case "core":
		return fmt.Errorf("FIXME: not yet implemented")
	case "config":
	case "functions":
		return functionListRun(
			ctx,
			h.mod.MainObject.AsFunctionProvider(),
			interp.HandlerCtx(ctx).Stdout,
			false,
		)
	default:
		return fmt.Errorf("no such command: %s", args[0])
	}
	return nil
}

// GitURL represents the different parts of a git-style URL.
type GitURL struct {
	Scheme string
	Host   string
	Owner  string
	Repo   string
	Ref    string
	Path   string
}

// ParseGitURL parses a git-style URL into its components.
func parseGitURL(gitURL string) (*GitURL, error) {
	u, err := url.Parse(gitURL)
	if err != nil {
		return nil, err
	}

	// Splitting the path part to extract owner and repo
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid repository path: %s", u.Path)
	}
	owner := parts[0]
	repo := parts[1]

	// Check if there is a fragment (ref and path)
	var ref, path string
	if u.Fragment != "" {
		fragmentParts := strings.SplitN(u.Fragment, "/", 2)
		ref = fragmentParts[0]
		if len(fragmentParts) > 1 {
			path = fragmentParts[1]
		}
	}

	return &GitURL{
		Scheme: u.Scheme,
		Host:   u.Host,
		Owner:  owner,
		Repo:   repo,
		Ref:    ref,
		Path:   path,
	}, nil
}

// String reconstructs the git-style URL from its components.
func (p GitURL) String() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s://%s/%s/%s", p.Scheme, p.Host, p.Owner, p.Repo))

	// Append branch and path if present
	if p.Ref != "" {
		sb.WriteString(fmt.Sprintf("#%s", p.Ref))
		if p.Path != "" {
			sb.WriteString(fmt.Sprintf("/%s", p.Path))
		}
	}
	return sb.String()
}

func loadReadlineConfig() (*readline.Config, error) {
	dataRoot := filepath.Join(xdg.DataHome, "dagger")
	err := os.MkdirAll(dataRoot, 0o700)
	if err != nil {
		return nil, err
	}

	return &readline.Config{
		Prompt:       "> ",
		HistoryFile:  filepath.Join(dataRoot, "histfile"),
		HistoryLimit: 1000,
	}, nil
}
