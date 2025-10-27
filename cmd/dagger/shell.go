package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/slog"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/vito/bubbline/computil"
	"github.com/vito/bubbline/editline"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// shellCode is the code to be executed in the shell command
var (
	shellCode string

	llmModel string
)

func shellAddFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&shellCode, "command", "c", "", "Execute a dagger shell command")
	cmd.Flags().StringVar(&llmModel, "model", "", "LLM model to use (e.g., 'claude-sonnet-4-5', 'gpt-4.1')")
}

var shellCmd = &cobra.Command{
	Use:   "shell [options] [file...]",
	Short: "Run an interactive dagger shell",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SetContext(idtui.WithPrintTraceLink(cmd.Context(), true))
		return withEngine(cmd.Context(), initModuleParams(args), func(ctx context.Context, engineClient *client.Client) error {
			dag := engineClient.Dagger()
			handler := newShellCallHandler(dag, Frontend)

			err := handler.RunAll(ctx, args)

			// Don't bother printing the error message if the TUI is enabled.
			var es interp.ExitStatus
			if handler.tty && errors.As(err, &es) {
				return ExitError{Code: int(es)}
			}

			return err
		})
	},
	Hidden: true,
}

type shellCallHandler struct {
	dag    *dagger.Client
	runner *interp.Runner

	// don't detect + load a module, just stick to dagger core
	noModule bool
	// a module ref to load
	moduleURL string

	// frontend to integrate with
	frontend idtui.Frontend

	// tty is set to true when running the TUI (pretty frontend)
	tty bool

	// repl is set to true when running in interactive mode
	repl bool

	// stdoutWriter is used to call withTerminal on each write the runner makes to stdout
	stdoutWriter *terminalWriter

	// stderrWriter is used to call withTerminal on each write the runner makes to stderr
	stderrWriter *terminalWriter

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
	llmErr     error // error initializing LLM
	llmModel   string
	llmL       sync.Mutex // synchronizing LLM init status

	// debug mode toggle
	debug bool

	// mu is used to synchronize access between the global handler and interpreter runs
	mu sync.RWMutex

	// interpreter mode (shell or prompt)
	mode      interpreterMode
	savedMode interpreterMode // for coming back from history

	// cancel interrupts the entire shell session
	cancel func()
}

func newShellCallHandler(dag *dagger.Client, fe idtui.Frontend) *shellCallHandler {
	ref, _ := getExplicitModuleSourceRef()
	if ref == "" {
		ref = moduleURLDefault
	}
	return &shellCallHandler{
		dag:       dag,
		llmModel:  llmModel,
		mode:      modeShell,
		noModule:  moduleNoURL,
		moduleURL: ref,
		frontend:  fe,
	}
}

// Debug prints to stderr internal command handler state and workflow that
// can be helpful while developing the shell or even troubhleshooting, and
// is toggled with the hidden builtin .debug
func (h *shellCallHandler) Debug() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.debug
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
	var def *moduleDef
	var cfg *configuredModule

	if !h.noModule {
		def, cfg, err = h.maybeLoadModule(ctx, h.moduleURL)
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

	subpath := h.moduleURL
	if cfg != nil {
		subpath = cfg.Subpath
	}

	wd, err := h.newWorkdir(ctx, def, subpath)
	if err != nil {
		return fmt.Errorf("initial context: %w", err)
	}

	h.initwd = *wd
	h.wd = h.initwd

	// not h.Debug() on purpose because it's only set from within an interpreter run
	if debugFlag {
		slog := slog.SpanLogger(ctx, InstrumentationLibrary)
		slog.Debug("initial workdir",
			"context", h.initwd.Context,
			"path", h.initwd.Path,
			"module", h.initwd.Module,
			"loaded modules", h.debugLoadedModules(),
		)
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

	return h.runner.Run(ctx, file)
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
	slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))

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

	// Empty input
	if line == "" {
		// add an immediately-canceled blank span, to emulate submitting blank shell
		// commands to space things apart
		_, span := Tracer().Start(ctx, "",
			telemetry.Reveal(),
			trace.WithAttributes(attribute.Bool(telemetry.CanceledAttr, true)))
		span.End()
		return nil
	}

	// Handle based on mode
	if h.mode == modePrompt {
		// NB: no span in this case, just let the LLM APIs create the user/assistant
		// message spans

		llm, err := h.llm(ctx)
		if err != nil {
			return err
		}
		newLLM, err := llm.WithPrompt(ctx, line)
		if err != nil {
			return err
		}
		h.llmSession = newLLM
		h.llmModel = newLLM.model
		return nil
	}

	// Ensure we always see new telemetry for shell commands, rather than
	// "resurrecting" the same telemetry from previous commands
	if bag, err := baggage.Parse("repeat-telemetry=true"); err == nil {
		ctx = baggage.ContextWithBaggage(ctx, bag)
	}

	// Create a new span for this command
	var span trace.Span
	ctx, span = Tracer().Start(ctx, line,
		telemetry.Reveal(),
		trace.WithAttributes(
			attribute.String(telemetry.ContentTypeAttr, h.mode.ContentType()),
		))
	defer telemetry.End(span, func() error {
		if errors.Is(rerr, context.Canceled) {
			span.SetAttributes(attribute.Bool(telemetry.CanceledAttr, true))
			return nil
		}
		return rerr
	})

	// redirect stdio to the current span
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close() // ensure we send EOF this regardless so TUI can flush

	stdoutW := newTerminalWriter(stdio.Stdout.Write)
	// handle shell state
	stdoutW.SetProcessFunc(h.stateResolver(ctx))
	stderrW := newTerminalWriter(stdio.Stderr.Write)
	interp.StdIO(nil, stdoutW, stderrW)(h.runner)

	// Try to prevent the state store from chugging memory on possibly
	// long interactive sessions.
	// Note: This may not be worth it as items will already be pruned
	// when last used. We should only have orphans at this point if
	// there's a variable that gets reset with a different value and
	// that should hardly cause memory issues.
	defer h.state.Prune(ctx)

	if debugFlag {
		// requires `--debug -vvvv` and .debug` for full dump
		defer h.state.debug(ctx, h.Debug())
	}

	// Run shell command
	return h.run(ctx, strings.NewReader(line), "")
}

func (h *shellCallHandler) Prompt(ctx context.Context, out idtui.TermOutput, fg termenv.Color) (string, tea.Cmd) {
	sb := new(strings.Builder)

	sb.WriteString(termenv.CSI + termenv.ResetSeq + "m") // clear background

	var init tea.Cmd

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
		llm, err := h.llmMaybe()
		if err != nil {
			sb.WriteString(out.String("error").Bold().Foreground(termenv.ANSIRed).String())
			sb.WriteString(out.String(" ").String())
			fg = termenv.ANSIRed
		} else if llm != nil {
			sb.WriteString(out.String(llm.model).Bold().Foreground(termenv.ANSICyan).String())
			sb.WriteString(out.String(" ").String())
		} else {
			sb.WriteString(out.String("loading...").Bold().Foreground(termenv.ANSIYellow).String())
			sb.WriteString(out.String(" ").String())
			init = func() tea.Msg {
				h.llm(ctx) // initialize LLM
				return idtui.UpdatePromptMsg{}
			}
		}
		sb.WriteString(out.String(idtui.LLMPrompt).Bold().Foreground(fg).String())
		sb.WriteString(out.String(out.String(" ").String()).String())
	}

	return sb.String(), init
}

func (*shellCallHandler) Print(ctx context.Context, args ...any) error {
	hc := interp.HandlerCtx(ctx)
	_, err := fmt.Fprintln(hc.Stdout, args...)
	return err
}

func (h *shellCallHandler) AutoComplete(entireInput [][]rune, line int, col int) (string, editline.Completions) {
	if h.mode == modePrompt {
		word, wstart, wend := computil.FindWord(entireInput, line, col)
		if after, ok := strings.CutPrefix(word, "$"); ok {
			prefix := after
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

func (h *shellCallHandler) llmMaybe() (*LLMSession, error) {
	h.llmL.Lock()
	defer h.llmL.Unlock()
	return h.llmSession, h.llmErr
}

func (h *shellCallHandler) llm(ctx context.Context) (*LLMSession, error) {
	if s, e := h.llmMaybe(); s != nil || e != nil {
		return s, e
	}

	// initialize without the lock held
	s, err := NewLLMSession(ctx, h.dag, h.llmModel, h, h.frontend)

	h.llmL.Lock()
	defer h.llmL.Unlock()

	if err != nil {
		slog.Error("failed to initialize LLM", "error", err)
		h.llmErr = err
		return nil, err
	}
	h.llmSession = s
	h.llmModel = s.model
	return h.llmSession, h.llmErr
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
		key.NewBinding(
			key.WithKeys("ctrl+u"),
			key.WithHelp("ctrl+u", "upload changes"),
			idtui.KeyEnabled(h.mode == modePrompt),
		),
	}
}

func (h *shellCallHandler) ReactToInput(ctx context.Context, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case ">":
		h.mode = modePrompt
		return func() tea.Msg {
			h.llm(ctx) // initialize LLM
			return idtui.UpdatePromptMsg{}
		}
	case "!":
		h.mode = modeShell
		return func() tea.Msg {
			return idtui.UpdatePromptMsg{}
		}
	case "ctrl+s":
		if h.llmSession != nil {
			return func() tea.Msg {
				if err := h.llmSession.SyncToLocal(ctx); err != nil {
					slog.Error("failed to sync changes to local filesystem", "error", err.Error())
					// Show error in sidebar
					Frontend.SetSidebarContent(idtui.SidebarSection{
						Title:   "Changes",
						Content: termenv.String("SAVE ERROR: " + err.Error()).Foreground(termenv.ANSIRed).String(),
					})
				}
				return idtui.UpdatePromptMsg{}
			}
		}
	case "ctrl+u":
		if h.llmSession != nil {
			return func() tea.Msg {
				if err := h.llmSession.SyncFromLocal(ctx); err != nil {
					slog.Error("failed to load current working directory into agent workspace", "error", err.Error())
					// Show error in sidebar
					Frontend.SetSidebarContent(idtui.SidebarSection{
						Title:   "Changes",
						Content: termenv.String("UPLOAD ERROR: " + err.Error()).Foreground(termenv.ANSIRed).String(),
					})
				}
				return idtui.UpdatePromptMsg{}
			}
		}
	}
	return nil
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
