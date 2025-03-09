package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/vito/bubbline/computil"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

const (
	// shellInternalCmd is the command that is used internally to avoid conflicts
	// with interpreter builtins. For example when `echo` is used, the command becomes
	// `__dag echo`. Otherwise we can't have a function named `echo`.
	shellInternalCmd = "__dag"

	// shellInterpBuiltinPrefix is the prefix that users should add to an
	// interpreter builtin command to force running it.
	shellInterpBuiltinPrefix = "_"
)

// shellCode is the code to be executed in the shell command
var (
	shellCode         string
	shellNoLoadModule bool
)

func shellAddFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&shellCode, "code", "c", "", "Command to be executed")
	cmd.Flags().BoolVar(&shellNoLoadModule, "no-mod", false, "Don't load module during shell startup (mutually exclusive with --mod)")
	cmd.MarkFlagsMutuallyExclusive("mod", "no-mod")
}

var shellCmd = &cobra.Command{
	Use:   "shell [options] [file...]",
	Short: "Run an interactive dagger shell",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SetContext(idtui.WithPrintTraceLink(cmd.Context(), true))
		return withEngine(cmd.Context(), client.Params{}, func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			handler := &shellCallHandler{
				dag:   dag,
				debug: debug,
			}
			return handler.RunAll(ctx, args)
		})
	},
	Hidden: true,
	Annotations: map[string]string{
		"experimental": "true",
	},
}

func newTerminalWriter(fn func([]byte) (int, error)) *terminalWriter {
	return &terminalWriter{
		fn: fn,
	}
}

// terminalWriter is a custom io.Writer that synchronously calls the handler's
// withTerminal on each write from the runner
type terminalWriter struct {
	mu sync.Mutex
	fn func([]byte) (int, error)

	// processFn is a function that can be used to process the incoming data
	// before writing to the terminal
	//
	// This can be used to resolve shell state just before printing to screen,
	// and make necessary API requests.
	processFn func([]byte) ([]byte, error)
}

func (o *terminalWriter) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	if o.processFn != nil {
		r, err := o.processFn(p)
		if err != nil {
			return 0, err
		}
		p = r
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.fn(p)
}

// Shell state is piped between exec handlers and only in the end the runner
// writes the final output to the stdoutWriter. We need to check if that
// state needs to be resolved into an API request and handle the response
// appropriately. Note that this can happen in parallel if commands are
// separated with a '&'.
func (o *terminalWriter) SetProcessFunc(fn func([]byte) ([]byte, error)) {
	o.processFn = fn
}

type shellCallHandler struct {
	dag    *dagger.Client
	runner *interp.Runner

	// tty is set to true when running the TUI (pretty frontend)
	tty bool

	// repl is set to true when running in interactive mode
	repl bool

	// stdoutWriter is used to call withTerminal on each write the runner makes to stdout
	stdoutWriter *terminalWriter

	// stderrWriter is used to call withTerminal on each write the runner makes to stderr
	stderrWriter *terminalWriter

	// debug writes to the handler context's stderr what the arguments, input,
	// and output are for each command that the exec handler processes
	debug bool

	// builtins is the list of Dagger Shell builtin commands
	builtins []*ShellCommand

	// stdlib is the list of standard library commands
	stdlib []*ShellCommand

	// modDefs has the cached module definitions, after loading, and
	// keyed by module digest
	modDefs sync.Map

	// initwd is used to return to the initial context
	initwd shellWorkdir

	// wd is the current working directory
	wd shellWorkdir

	// oldpwd is used to return to the previous working directory
	oldwd shellWorkdir

	// mu is used to synchronize access to the workdir
	mu sync.RWMutex
}

// RunAll is the entry point for the shell command
//
// It creates the runner and dispatches the execution to different modes:
// - Interactive: when no arguments are provided
// - File: when a file path is provided as an argument
// - Code: when code is passed inline using the `-c,--code` flag or via stdin
func (h *shellCallHandler) RunAll(ctx context.Context, args []string) error {
	h.tty = !silent && (hasTTY && progress == "auto" || progress == "tty")

	if err := h.Initialize(ctx); err != nil {
		return err
	}

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

func (h *shellCallHandler) Initialize(ctx context.Context) error {
	r, err := interp.New(
		interp.Params("-e", "-u", "-o", "pipefail"),

		// The "Interactive" option is useful even when not running dagger shell
		// in interactive mode. It expands aliases and maybe more in the future.
		interp.Interactive(true),

		// Interpreter builtins run before the exec handlers, but CallHandler
		// runs before any of that, so we can use it to change the arguments
		// slightly in order to resolve naming conflicts. For example, "echo"
		// is an interpreter builtin but can also be a Dagger function.
		interp.CallHandler(func(ctx context.Context, args []string) ([]string, error) {
			if args[0] == shellInternalCmd {
				return args, fmt.Errorf("command %q is reserved for internal use", shellInternalCmd)
			}
			// When there's a Dagger function with a name that conflicts
			// with an interpreter builtin, the Dagger function is favored.
			// To force the builtin to execute instead, prefix the command
			// with "_". For example: "container | from $(_echo alpine)".
			if strings.HasPrefix(args[0], shellInterpBuiltinPrefix) {
				args[0] = strings.TrimPrefix(args[0], shellInterpBuiltinPrefix)
				return args, nil
			}
			// We may allow some interpreter builtins to be used as dagger shell
			// builtins, but there's no way to directly call the interpreter
			// command from there so we use ShellCommand just for the documentation
			// (.help) but strip the builtin prefix here ('.') when executing.
			if cmd, _ := h.BuiltinCommand(args[0]); cmd != nil && cmd.Run == nil {
				if name := strings.TrimPrefix(args[0], "."); isInterpBuiltin(name) {
					args[0] = name
					return args, nil
				}
			}
			// If the command is an interpreter builtin, bypass the interpreter
			// builtins to ensure the exec handler is executed.
			if isInterpBuiltin(args[0]) {
				return append([]string{shellInternalCmd}, args...), nil
			}
			return args, nil
		}),
		interp.ExecHandlers(h.Exec),
	)
	if err != nil {
		return err
	}
	h.runner = r

	// collect initial env + vars
	h.runner.Reset()

	// TODO: use `--workdir` and `--no-workdir` flags
	ref, _ := getExplicitModuleSourceRef()
	if ref == "" {
		ref = moduleURLDefault
	}

	var def *moduleDef
	var cfg *configuredModule

	if !shellNoLoadModule {
		def, cfg, err = h.maybeLoadModule(ctx, ref)
		if err != nil {
			return err
		}
	}

	// Could be `--no-mod` or module not found from current dir
	if def == nil {
		def, err = initializeCore(ctx, h.dag)
		if err != nil {
			return err
		}
		h.modDefs.Store("", def)
	}

	subpath := ref
	if cfg != nil {
		subpath = cfg.Subpath
	}

	wd, err := h.newWorkdir(ctx, def, subpath)
	if err != nil {
		return fmt.Errorf("initial context: %w", err)
	}

	h.initwd = *wd
	h.wd = h.initwd

	if h.debug {
		shellDebug(ctx, "initial context", h.initwd, h.debugLoadedModules())
	}

	h.registerCommands()
	return nil
}

func isInterpBuiltin(name string) bool {
	// Allow the following:
	//  - invalid function/module names: "[", ":"
	//  - unlikely to conflict: "true", "false"
	switch name {
	case "exit", "set", "shift", "unset",
		"echo", "printf", "break", "continue", "pwd", "cd",
		"wait", "builtin", "trap", "type", "source", ".", "command",
		"dirs", "pushd", "popd", "alias", "unalias",
		"getopts", "eval", "test", "exec",
		"return", "read", "mapfile", "readarray", "shopt",
		//  not implemented
		"umask", "fg", "bg":
		return true
	}
	return false
}

func litWord(s string) *syntax.Word {
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: s}}}
}

func (h *shellCallHandler) shellStateProcessor(ctx context.Context) func([]byte) ([]byte, error) {
	return func(b []byte) ([]byte, error) {
		resp, typeDef, err := h.Result(ctx, bytes.NewReader(b), handleObjectLeaf)
		if err != nil {
			return nil, err
		}
		if typeDef != nil && typeDef.Kind == dagger.TypeDefKindVoidKind {
			return nil, nil
		}
		buf := new(bytes.Buffer)
		err = printResponse(buf, resp, typeDef)
		return buf.Bytes(), err
	}
}

func (h *shellCallHandler) Eval(ctx context.Context, code string) (resp any, typeDef *modTypeDef, err error) {
	file, err := parseShell(strings.NewReader(code), "")
	if err != nil {
		return nil, nil, err
	}

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	h.stdoutWriter = newTerminalWriter(stdio.Stdout.Write)
	h.stderrWriter = newTerminalWriter(stdio.Stderr.Write)
	interp.StdIO(nil, h.stdoutWriter, h.stderrWriter)(h.runner)

	h.stdoutWriter.SetProcessFunc(func(b []byte) ([]byte, error) {
		resp, typeDef, err = h.Result(ctx, bytes.NewReader(b), handleObjectLeaf)
		if err != nil {
			return nil, err
		}
		if typeDef != nil && typeDef.Kind == dagger.TypeDefKindVoidKind {
			return nil, nil
		}
		buf := new(bytes.Buffer)
		err = printResponse(buf, resp, typeDef)
		return buf.Bytes(), err
	})

	err = h.runner.Run(ctx, file)
	if exit, ok := interp.IsExitStatus(err); ok {
		if int(exit) != shellHandlerExit {
			return nil, nil, ExitError{Code: int(exit)}
		}
		err = nil
	}

	return resp, typeDef, err
}

// run parses code and executes the interpreter's Runner
func (h *shellCallHandler) run(ctx context.Context, reader io.Reader, name string) error {
	file, err := parseShell(reader, name)
	if err != nil {
		return err
	}

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	h.stdoutWriter = newTerminalWriter(stdio.Stdout.Write)
	h.stderrWriter = newTerminalWriter(stdio.Stderr.Write)
	interp.StdIO(nil, h.stdoutWriter, h.stderrWriter)(h.runner)
	h.stdoutWriter.SetProcessFunc(h.shellStateProcessor(ctx))

	err = h.runner.Run(ctx, file)
	if exit, ok := interp.IsExitStatus(err); ok {
		if int(exit) != shellHandlerExit {
			return ExitError{Code: int(exit)}
		}
		err = nil
	}
	return err
}

func parseShell(reader io.Reader, name string, opts ...syntax.ParserOption) (*syntax.File, error) {
	opts = append([]syntax.ParserOption{syntax.Variant(syntax.LangPOSIX)}, opts...)
	file, err := syntax.NewParser(opts...).Parse(reader, name)
	if err != nil {
		return nil, err
	}

	syntax.Walk(file, func(node syntax.Node) bool {
		if node, ok := node.(*syntax.CmdSubst); ok {
			if len(node.Stmts) > 0 {
				// Rewrite command substitutions from $(foo; bar) to $(exec <&-; foo; bar)
				// so that all the original commands run with a closed (nil) standard input.
				node.Stmts = append([]*syntax.Stmt{{
					Cmd: &syntax.CallExpr{Args: []*syntax.Word{litWord(shellInterpBuiltinPrefix + "exec")}},
					Redirs: []*syntax.Redirect{{
						Op:   syntax.DplIn,
						Word: litWord("-"),
					}},
				}}, node.Stmts...)
			}
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
	h.repl = true

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// give ourselves a blank slate by zooming into a passthrough span
	shellCtx, shellSpan := Tracer().Start(ctx, "shell", telemetry.Passthrough())
	defer telemetry.End(shellSpan, func() error { return nil })
	Frontend.SetPrimary(dagui.SpanID{SpanID: shellSpan.SpanContext().SpanID()})

	mu := &sync.Mutex{}
	complete := &shellAutoComplete{h}
	Frontend.Shell(shellCtx,
		func(ctx context.Context, line string) (rerr error) {
			mu.Lock()
			defer mu.Unlock()

			if line == "exit" {
				cancel()
				return nil
			}

			if strings.TrimSpace(line) == "" {
				return nil
			}

			ctx, span := Tracer().Start(ctx, line)
			defer telemetry.End(span, func() error { return rerr })

			// redirect stdio to the current span
			stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
			defer stdio.Close()

			stdoutW := newTerminalWriter(stdio.Stdout.Write)
			// handle shell state
			stdoutW.SetProcessFunc(h.shellStateProcessor(ctx))
			stderrW := newTerminalWriter(stdio.Stderr.Write)
			interp.StdIO(nil, stdoutW, stderrW)(h.runner)

			return h.run(ctx, strings.NewReader(line), "")
		},
		complete.Do,
		shellIsComplete,
		h.prompt,
	)

	return nil
}

func (h *shellCallHandler) prompt(out idtui.TermOutput, fg termenv.Color) string {
	sb := new(strings.Builder)

	if def, _ := h.GetModuleDef(nil); def != nil {
		sb.WriteString(out.String(def.Name).Bold().Foreground(termenv.ANSICyan).String())
		sb.WriteString(out.String(" ").String())
	}

	sb.WriteString(out.String(idtui.ShellPrompt).Bold().Foreground(fg).String())
	sb.WriteString(out.String(out.String(" ").String()).String())

	return sb.String()
}

func (*shellCallHandler) Print(ctx context.Context, args ...any) error {
	hctx := interp.HandlerCtx(ctx)
	_, err := fmt.Fprintln(hctx.Stdout, args...)
	return err
}

func shellIsComplete(entireInput [][]rune, line int, col int) bool {
	input, _ := computil.Flatten(entireInput, line, col)
	_, err := syntax.NewParser().Parse(strings.NewReader(input), "")
	if err != nil {
		if syntax.IsIncomplete(err) {
			// only return false here if it's incomplete
			return false
		}
	}
	return true
}
