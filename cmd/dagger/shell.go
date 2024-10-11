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
	"strings"
	"text/tabwriter"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"github.com/chzyer/readline"
	"github.com/dagger/dagger/engine/client"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

var shellCode string

const shellStatePrefix = "DSH:"

func init() {
	shellCmd.Flags().StringVarP(&shellCode, "code", "c", "", "command to be executed")
}

var shellCmd = &cobra.Command{
	Use:   "shell [FILE]...",
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
				return fmt.Errorf("error initializing module: %s", err)
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

	// outBuf is used to capture the final output that the runner produces
	outBuf *bytes.Buffer

	// debug writes to the handler context's stderr what the arguments, input,
	// and output are for each commmand that the exec handler processes
	debug bool
}

// RunAll is the entry point for the shell command
//
// It creates the runner and dispatches the execution to different modes:
// - Interactive: when no arguments are provided
// - File: when a file path is provided as an argument
// - Code: when code is passed inline using the `-c,--code` flag or via stdin
func (h *shellCallHandler) RunAll(ctx context.Context, args []string) error {
	h.outBuf = new(bytes.Buffer)

	r, err := interp.New(
		interp.StdIO(nil, h.outBuf, h.stderr),
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

func (h *shellCallHandler) run(ctx context.Context, reader io.Reader, name string) error {
	file, err := syntax.NewParser().Parse(reader, name)
	if err != nil {
		return err
	}

	h.outBuf.Reset()
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
	output := h.outBuf.String()

	s, err := readShellState(h.outBuf)
	if err != nil {
		return err
	}
	if s != nil {
		return h.Result(ctx, *s)
	}

	return h.withTerminal(func(o, _ io.Writer) error {
		_, err := fmt.Fprint(o, output)
		return err
	})
}

func (h *shellCallHandler) runPath(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return h.run(ctx, f, path)
}

func (h *shellCallHandler) runInteractive(ctx context.Context) error {
	h.withTerminal(func(_, e io.Writer) error {
		fmt.Fprintln(e, `Dagger interactive shell. Type ".help" for more information.`)
		return nil
	})
	var runErr error
	for {
		if runErr != nil {
			runErr = h.withTerminal(func(_, e io.Writer) error {
				fmt.Fprintf(e, "Error: %s\n", runErr.Error())
				return nil
			})
		}

		var line string

		err := h.withTerminal(func(_, _ io.Writer) error {
			rl, err := readline.New("> ")
			if err != nil {
				return err
			}
			defer rl.Close()
			line, err = rl.Readline()
			return err
		})
		if err != nil {
			// EOF or Ctrl+D to exit
			if errors.Is(err, io.EOF) || errors.Is(err, readline.ErrInterrupt) {
				break
			}
			return err
		}

		if strings.TrimSpace(line) == "" {
			continue
		}

		runErr = h.run(ctx, strings.NewReader(line), "")
	}
	return nil
}

func (h *shellCallHandler) withTerminal(fn func(stdout, stderr io.Writer) error) error {
	// TODO: handle TUI
	return fn(h.stdout, h.stderr)
}

func (h *shellCallHandler) Exec(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
	return func(ctx context.Context, args []string) error {
		if h.debug {
			shellLog(ctx, "[DBG] args: %v\n", args)
		}

		s, err := shellState(ctx)
		if err != nil {
			return err
		}

		if h.debug {
			shellLog(ctx, "[DBG] input: %v\n", s)
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
			shellLog(ctx, "[DBG] output: %v\n", s)
		}

		return s.Write(ctx)
	}
}

func (h *shellCallHandler) entrypointCall(ctx context.Context, args []string) (*ShellState, error) {
	// shellLog(ctx, "[%s] ENTRYPOINT!!\n", args[0])

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

	// Required fargs here are positional but we have fargs lot of power in our
	// custom flags, so to take advantage of them just add the corresponding
	// `--flag-name` fargs so pflags can parse them.
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

func (h *shellCallHandler) Result(ctx context.Context, s ShellState) error {
	prev := s.Function()
	if prev == nil {
		return fmt.Errorf("no function call in shell state")
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

	return h.withTerminal(func(o, e io.Writer) error {
		return executeRequest(ctx, q, fn.ReturnType, o, e)
	})
}

func shellLog(ctx context.Context, msg string, args ...any) {
	hctx := interp.HandlerCtx(ctx)
	fmt.Fprintf(hctx.Stderr, msg, args...)
}

func shellState(ctx context.Context) (*ShellState, error) {
	return readShellState(interp.HandlerCtx(ctx).Stdin)
}

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

type ShellState struct {
	Calls []FunctionCall `json:"calls"`
}

type FunctionCall struct {
	Object       string         `json:"object"`
	Name         string         `json:"name"`
	Arguments    map[string]any `json:"arguments"`
	ReturnObject string         `json:"returnObject"`
}

func (s ShellState) Write(ctx context.Context) error {
	htcx := interp.HandlerCtx(ctx)
	fmt.Fprint(htcx.Stdout, shellStatePrefix)
	return json.NewEncoder(htcx.Stdout).Encode(s)
}

func (s ShellState) Function() *FunctionCall {
	if len(s.Calls) == 0 {
		return nil
	}
	return &s.Calls[len(s.Calls)-1]
}

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
			Name:         fn.CmdName(),
			ReturnObject: fn.ReturnType.Name(),
			Arguments:    argValues,
		}),
	}
}

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

func (f FunctionCall) GetDef(modDef *moduleDef) (*modFunction, error) {
	return modDef.GetObjectFunction(f.Object, f.Name)
}

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
	if strings.HasPrefix(args[0], ".") {
		args[0] = args[0][1:]
	}
	switch args[0] {
	case "help":
		shellLog(ctx, `
.functions    list available functions
.help         print this help message
.install	  install a dependency
.deps         list dependencies
.uninstall    uninstall a dependency
.login        login to Dagger Cloud
.logout       logout from Dagger Cloud
.core         load a core Dagger type
`)
		return nil
	case "git":
		if len(args) < 2 {
			return fmt.Errorf("usage: .git URL")
		}
		gitUrl, err := parseGitURL(args[1])
		if err != nil {
			return err
		}
		ref := gitUrl.Ref
		if ref == "" {
			ref = "main"
		}
		subdir := gitUrl.Path
		gitUrl.Ref = ""
		gitUrl.Path = ""
		s := &ShellState{
			Calls: []FunctionCall{
				{
					Name: "git",
					Arguments: map[string]any{
						"url": gitUrl.String(),
					},
				},
				{
					Name: "ref",
					Arguments: map[string]interface{}{
						"name": ref,
					},
				},
				{
					Name: "tree",
				},
				{
					Name:         "directory",
					ReturnObject: "Directory",
					Arguments: map[string]interface{}{
						"path": subdir,
					},
				},
			},
		}
		return s.Write(ctx)
	case "install":
		if len(args) < 1 {
			return fmt.Errorf("usage: .install MODULE")
		}
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

	case "uninstall":
	case "login":
		return reexec(ctx, args)
	case "logout":
		return reexec(ctx, args)
	case "core":
		return fmt.Errorf("FIXME: not yet implemented")
	case "config":
	case "functions":
		functions := h.mod.MainObject.AsFunctionProvider().GetFunctions()
		w := tabwriter.NewWriter(interp.HandlerCtx(ctx).Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tDESCRIPTION\tRETURN TYPEs")
		fmt.Fprintln(w, "---\t---\t---")
		for _, fn := range functions {
			fmt.Fprintf(w, "%s\t%s\t%s\n", fn.CmdName(), fn.Description, fn.ReturnType.Name())
		}
		return w.Flush()
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
