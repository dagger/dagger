package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/vito/bubbline/computil"
	"github.com/vito/bubbline/editline"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// shellCode is the code to be executed in the shell command
var (
	shellCode         string
	shellNoLoadModule bool

	llmModel string
)

func shellAddFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&shellCode, "code", "c", "", "Command to be executed")
	cmd.Flags().BoolVarP(&shellNoLoadModule, "no-mod", "n", false, "Don't load module during shell startup (mutually exclusive with --mod)")
	cmd.Flags().StringVar(&llmModel, "model", "", "LLM model to use (e.g., 'claude-3-5-sonnet', 'gpt-4o')")
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
				dag:      dag,
				debug:    debug,
				llmModel: llmModel,
				mode:     modeShell,
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

	// lastResult is the last result from the shell
	lastResult *Result

	// llm is the LLM session for the shell
	llmSession *LLMSession
	llmModel   string

	// mu is used to synchronize access to the workdir and interpreter
	mu sync.RWMutex

	// interpreter mode (shell or prompt)
	mode      interpreterMode
	savedMode interpreterMode // for coming back from history

	// cancel interrupts the entire shell session
	cancel func()
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

	// collect initial env + vars
	h.runner.Reset()

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
	return nil
}

func litWord(s string) *syntax.Word {
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{Value: s}}}
}

func (h *shellCallHandler) Eval(ctx context.Context, code string) error {
	return h.run(ctx, strings.NewReader(code), "")
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
	h.cancel = cancel

	// give ourselves a blank slate by zooming into a passthrough span
	ctx, shellSpan := Tracer().Start(ctx, "shell", telemetry.Passthrough())
	defer telemetry.End(shellSpan, func() error { return nil })
	Frontend.SetPrimary(dagui.SpanID{SpanID: shellSpan.SpanContext().SpanID()})

	// Start the shell loop (either in LLM mode or normal shell mode)
	Frontend.Shell(ctx, h)

	return nil
}

var _ idtui.ShellHandler = (*shellCallHandler)(nil)

func (h *shellCallHandler) Handle(ctx context.Context, line string) (rerr error) {
	// Quick sanitization
	line = strings.TrimSpace(line)

	// If in exit command
	if line == "exit" || line == "/exit" {
		h.cancel()
		return nil
	}

	// Create a new span for this command
	ctx, span := Tracer().Start(ctx, line,
		trace.WithAttributes(
			attribute.String(telemetry.ContentTypeAttr, h.mode.ContentType()),
			attribute.Bool(telemetry.CanceledAttr, line == ""),
		),
	)
	defer telemetry.End(span, func() error { return rerr })

	// Empty input
	if line == "" {
		return nil
	}

	// Handle based on mode
	if h.mode == modePrompt {
		llm, err := h.llm(ctx)
		if err != nil {
			return err
		}
		newLLM, err := llm.WithPrompt(ctx, line)
		if err != nil {
			return err
		}
		h.llmSession = newLLM
		return nil
	}

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

	// Run shell command - optionally sync vars back to LLM after running
	return h.run(ctx, strings.NewReader(line), "")
}

func (h *shellCallHandler) Prompt(out idtui.TermOutput, fg termenv.Color) string {
	sb := new(strings.Builder)

	sb.WriteString(termenv.CSI + termenv.ResetSeq + "m") // clear background

	// Use LLM prompt if LLM session is active and in prompt mode
	switch h.mode {
	case modeShell:
		if def, _ := h.GetModuleDef(nil); def != nil {
			sb.WriteString(out.String(def.Name).Bold().Foreground(termenv.ANSICyan).String())
			sb.WriteString(out.String(" ").String())
		}

		sb.WriteString(out.String(idtui.ShellPrompt).Bold().Foreground(fg).String())
		sb.WriteString(out.String(out.String(" ").String()).String())
	case modePrompt:
		// initialize LLM session if not already initialized
		llm, err := h.llm(context.TODO())
		if err != nil {
			sb.WriteString(out.String(err.Error()).Bold().Foreground(termenv.ANSIRed).String())
			sb.WriteString(out.String(" ").String())
		} else {
			sb.WriteString(out.String(llm.model).Bold().Foreground(termenv.ANSICyan).String())
			sb.WriteString(out.String(" ").String())
		}
		sb.WriteString(out.String(idtui.LLMPrompt).Bold().Foreground(fg).String())
		sb.WriteString(out.String(out.String(" ").String()).String())
	}

	return sb.String()
}

func (*shellCallHandler) Print(ctx context.Context, args ...any) error {
	hctx := interp.HandlerCtx(ctx)
	_, err := fmt.Fprintln(hctx.Stdout, args...)
	return err
}

func (h *shellCallHandler) AutoComplete(entireInput [][]rune, line int, col int) (string, editline.Completions) {
	if h.mode == modePrompt {
		word, wstart, wend := computil.FindWord(entireInput, line, col)
		if strings.HasPrefix(word, "$") {
			prefix := strings.TrimPrefix(word, "$")
			vars := h.runner.Vars
			var completions []string
			for k := range vars {
				if strings.HasPrefix(k, prefix) {
					completions = append(completions, "$"+k)
				}
			}
			return "", editline.SimpleWordsCompletion(completions, "variable", col, wstart, wend)
		}
		return "", nil
	}

	return (&shellAutoComplete{h}).Do(entireInput, line, col)
}

func (h *shellCallHandler) IsComplete(entireInput [][]rune, line int, col int) bool {
	if h.mode == modePrompt {
		return true // LLM prompt mode always considers input complete
	}

	// Regular shell mode
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

func (h *shellCallHandler) llm(ctx context.Context) (*LLMSession, error) {
	if h.llmSession == nil {
		// this blocks the UI, so set a brief timeout
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		s, err := NewLLMSession(ctx, h.dag, h.llmModel, h)
		if err != nil {
			return nil, err
		}
		h.llmSession = s
	}
	return h.llmSession, nil
}

func (h *shellCallHandler) ReactToInput(msg tea.KeyMsg) bool {
	switch msg.String() {
	case ">":
		h.mode = modePrompt
		return true
	case "!":
		h.mode = modeShell
		return true
	}
	return false
}

func (h *shellCallHandler) EncodeHistory(entry string) string {
	switch h.mode {
	case modePrompt:
		return ">" + entry
	case modeShell:
		return "!" + entry
	}
	return entry
}

func (h *shellCallHandler) DecodeHistory(entry string) string {
	if len(entry) > 0 {
		switch entry[0] {
		case '*':
			// Legacy format in history
			h.mode = modePrompt
			return entry[1:]
		case '>':
			h.mode = modePrompt
			return entry[1:]
		case '!':
			h.mode = modeShell
			return entry[1:]
		default:
			h.mode = modeUnset
		}
	}
	return entry
}

func (h *shellCallHandler) SaveBeforeHistory() {
	h.savedMode = h.mode
}

func (h *shellCallHandler) RestoreAfterHistory() {
	h.mode = h.savedMode
	h.savedMode = modeUnset
}

func (h *shellCallHandler) KeyBindings() []key.Binding {
	return []key.Binding{
		key.NewBinding(
			key.WithKeys("!"),
			key.WithHelp("!", "run shell"),
			idtui.KeyEnabled(h.mode == modePrompt),
		),
		key.NewBinding(
			key.WithKeys(">"),
			key.WithHelp(">", "run prompt"),
			idtui.KeyEnabled(h.mode == modeShell),
		),
	}
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
