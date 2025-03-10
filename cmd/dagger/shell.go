package main

import (
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
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

const (
	// We need a prompt that conveys the unique nature of the Dagger shell. Per gpt4:
	// The ⋈ symbol, known as the bowtie, has deep roots in relational databases and set theory,
	// where it denotes a join operation. This makes it especially fitting for a DAG environment,
	// as it suggests the idea of dependencies, intersections, and points where separate paths
	// or data sets come together.
	shellPromptSymbol = "⋈"
)

// shellCode is the code to be executed in the shell command
var (
	shellCode         string
	shellNoLoadModule bool
)

func shellAddFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&shellCode, "code", "c", "", "Command to be executed")
	cmd.Flags().BoolVarP(&shellNoLoadModule, "no-mod", "n", false, "Don't load module during shell startup (mutually exclusive with --mod)")
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

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

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

	// state stores the pipeline state between commands in a chain
	state *ShellStateStore

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

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	h.stdoutWriter = newTerminalWriter(stdio.Stdout.Write)
	h.stderrWriter = newTerminalWriter(stdio.Stderr.Write)

	r, err := interp.New(
		interp.StdIO(nil, h.stdoutWriter, h.stderrWriter),
		interp.Params("-e", "-u", "-o", "pipefail"),
		interp.CallHandler(h.Call),
		interp.ExecHandlers(h.Exec),

		// The "Interactive" option is useful even when not running dagger shell
		// in interactive mode. It expands aliases and maybe more in the future.
		interp.Interactive(true),
	)
	if err != nil {
		return err
	}
	h.runner = r

	h.state = NewStateStore(h.runner)

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

func litWord(s string) *syntax.Word {
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: s}}}
}

// run parses code and executes the interpreter's Runner
func (h *shellCallHandler) run(ctx context.Context, reader io.Reader, name string) error {
	file, err := parseShell(reader, name)
	if err != nil {
		return err
	}

	h.stdoutWriter.SetProcessFunc(h.stateResolver(ctx))

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
			stdoutW.SetProcessFunc(h.stateResolver(ctx))
			stderrW := newTerminalWriter(stdio.Stderr.Write)
			interp.StdIO(nil, stdoutW, stderrW)(h.runner)

			// Note: This may not be worth it as items will already be pruned
			// when last used. We should only have orphans at this point if
			// there's a variable that gets reset with a different value and
			// that should hardly cause memory issues.
			defer h.state.Prune()
			if h.debug {
				defer h.state.debug(ctx)
			}

			return h.run(ctx, strings.NewReader(line), "")
		},
		complete.Do,
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

	sb.WriteString(out.String(shellPromptSymbol).Bold().Foreground(fg).String())
	sb.WriteString(out.String(out.String(" ").String()).String())

	return sb.String()
}

func (*shellCallHandler) Print(ctx context.Context, args ...any) error {
	hctx := interp.HandlerCtx(ctx)
	_, err := fmt.Fprintln(hctx.Stdout, args...)
	return err
}
