package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"dagger.io/dagger"
	"github.com/adrg/xdg"
	"github.com/chzyer/readline"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/codes"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

const (
	// We need a prompt that conveys the unique nature of the Dagger shell. Per gpt4:
	// The ⋈ symbol, known as the bowtie, has deep roots in relational databases and set theory,
	// where it denotes a join operation. This makes it especially fitting for a DAG environment,
	// as it suggests the idea of dependencies, intersections, and points where separate paths
	// or data sets come together.
	shellPrompt = "⋈"
)

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

type safeBuffer struct {
	bu bytes.Buffer
	mu sync.Mutex
}

func (s *safeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bu.Write(p)
}

func (s *safeBuffer) Read(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bu.Read(p)
}

func (s *safeBuffer) HasUnread() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bu.Len() > 0
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bu.String()
}

func (s *safeBuffer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bu.Reset()
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

	// stdoutBuf is used to capture the final stdout that the runner produces
	stdoutBuf *safeBuffer

	// stderrBuf is used to capture the final stderr that the runner produces
	stderrBuf *safeBuffer

	// debug writes to the handler context's stderr what the arguments, input,
	// and output are for each command that the exec handler processes
	debug bool

	// builtins is the list of Dagger Shell builtin commands
	builtins []*ShellCommand

	// stdlib is the list of standard library commands
	stdlib []*ShellCommand

	// modRef is a key from modDefs, to set the corresponding module as the default
	// when no state is present, or when the state's ModRef is empty
	modRef string

	// modDefs has the cached module definitions, after loading, and keyed by
	// module reference as inputed by the user
	modDefs sync.Map

	// mu is used to synchronize access to the default module's definitions via modRef
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

	h.stdoutBuf = new(safeBuffer)
	h.stderrBuf = new(safeBuffer)

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
	h.modDefs.Store(ref, def)
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
			if h.stderrBuf.HasUnread() {
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
	if err != nil {
		exitCode := 1
		var ex *dagger.ExecError
		if errors.As(err, &ex) {
			if h.repl || !h.tty {
				return wrapExecError(ex)
			}
			exitCode = ex.ExitCode
		}
		if !h.repl && h.tty {
			return ExitError{Code: exitCode}
		}
		return err
	}
	if resp == nil {
		return nil
	}

	return h.withTerminal(func(_ io.Reader, stdout, _ io.Writer) error {
		fmt.Fprint(stdout, resp)
		if sval, ok := resp.(string); ok && stdoutIsTTY && !strings.HasSuffix(sval, "\n") {
			fmt.Fprintln(stdout)
		}
		return nil
	})
}

func wrapExecError(e *dagger.ExecError) error {
	out := make([]string, 0, 2)
	if e.Stdout != "" {
		out = append(out, "Stdout:\n"+e.Stdout)
	}
	if e.Stderr != "" {
		out = append(out, "Stderr:\n"+e.Stderr)
	}
	if len(out) > 0 {
		return fmt.Errorf("%w\n%s", e, strings.Join(out, "\n"))
	}
	return e
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
					Cmd: &syntax.CallExpr{Args: []*syntax.Word{litWord("..exec")}},
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
		Frontend.SetCustomExit(func() {})
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
				Frontend.SetCustomExit(nil)
				Frontend.SetVerbosity(0)
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

		spanCtx, span := Tracer().Start(ctx, line)
		newCtx, cancel := context.WithCancel(spanCtx)
		Frontend.SetPrimary(dagui.SpanID{SpanID: span.SpanContext().SpanID()})
		Frontend.SetCustomExit(cancel)
		runErr = h.run(newCtx, strings.NewReader(line), "")
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

// withTerminal handles using stdin, stdout, and stderr when the TUI is running
func (h *shellCallHandler) withTerminal(fn func(stdin io.Reader, stdout, stderr io.Writer) error) error {
	if h.repl && h.tty {
		return Frontend.Background(&terminalSession{
			fn: func(stdin io.Reader, stdout, stderr io.Writer) error {
				return fn(stdin, stdout, stderr)
			},
		}, false)
	}
	return fn(h.stdin, h.stdout, h.stderr)
}

func (*shellCallHandler) Print(ctx context.Context, args ...any) error {
	hctx := interp.HandlerCtx(ctx)
	_, err := fmt.Fprintln(hctx.Stdout, args...)
	return err
}
