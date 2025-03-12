package main

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"dagger.io/dagger/telemetry"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/muesli/termenv"
	"github.com/opencontainers/go-digest"
	"github.com/spf13/cobra"
	"github.com/vito/bubbline/complete"
	"github.com/vito/bubbline/computil"
	"github.com/vito/bubbline/editline"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Variables for llm command flags
var (
	llmModel string
)

func llmAddFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&llmModel, "model", "", "LLM model to use (e.g., 'claude-3-5-sonnet', 'gpt-4o')")
}

var llmCmd = &cobra.Command{
	Use:   "llm [options]",
	Short: "Run an interactive LLM interface",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SetContext(idtui.WithPrintTraceLink(cmd.Context(), true))
		return withEngine(cmd.Context(), client.Params{}, LLMLoop)
	},
	Hidden: true,
	Annotations: map[string]string{
		"experimental": "true",
	},
}

type LLMShellHandler struct {
	s      *LLMSession
	cancel func()
}

func (h *LLMShellHandler) Handle(ctx context.Context, line string) (rerr error) {
	if line == "/exit" {
		h.cancel()
		return nil
	}
	new, err := h.s.Interpret(ctx, line)
	if err != nil {
		return err
	}
	h.s = new
	return nil
}

func (h *LLMShellHandler) AutoComplete(entireInput [][]rune, line, col int) (string, editline.Completions) {
	return h.s.Complete(entireInput, line, col)
}

func (h *LLMShellHandler) IsComplete(entireInput [][]rune, line, col int) bool {
	return h.s.IsComplete(entireInput, line, col)
}

func (h *LLMShellHandler) Prompt(out idtui.TermOutput, fg termenv.Color) string {
	return h.s.Prompt(out, fg)
}

func (h *LLMShellHandler) ReactToInput(msg tea.KeyMsg) bool {
	if s, ok := h.s.ReactToInput(msg); ok {
		h.s = s
		return true
	}
	return false
}

func (h *LLMShellHandler) DecodeHistory(entry string) string {
	return h.s.DecodeHistory(entry)
}

func (h *LLMShellHandler) EncodeHistory(entry string) string {
	return h.s.EncodeHistory(entry)
}

func (h *LLMShellHandler) SaveBeforeHistory() {
	h.s.SaveBeforeHistory()
}

func (h *LLMShellHandler) RestoreAfterHistory() {
	h.s.RestoreAfterHistory()
}

func (h *LLMShellHandler) KeyBindings() []key.Binding {
	return h.s.KeyBindings()
}

func (s *LLMSession) ReactToInput(msg tea.KeyMsg) (*LLMSession, bool) {
	switch msg.String() {
	case "*":
		s.oneshotMode = modePrompt
		return s, true
	case "!":
		s.oneshotMode = modeShell
		return s, true
	case "backspace":
		s.oneshotMode = modeUnset
		return s, true
	}
	return s, false
}

func (s *LLMSession) EncodeHistory(entry string) string {
	switch s.mode() {
	case modePrompt:
		return "*" + entry
	case modeShell:
		return "!" + entry
	}
	return entry
}

func (s *LLMSession) KeyBindings() []key.Binding {
	return []key.Binding{
		key.NewBinding(
			key.WithKeys("!"),
			key.WithHelp("!", "run shell"),
			idtui.KeyEnabled(s.mode() == modePrompt),
		),
		key.NewBinding(
			key.WithKeys("*"),
			key.WithHelp("*", "run prompt"),
			idtui.KeyEnabled(s.mode() == modeShell),
		),
	}
}

func (s *LLMSession) DecodeHistory(entry string) string {
	if len(entry) > 0 {
		switch entry[0] {
		case '*':
			s.oneshotMode = modePrompt
			return entry[1:]
		case '!':
			s.oneshotMode = modeShell
			return entry[1:]
		}
	}
	s.oneshotMode = modeUnset
	return entry
}

func (s *LLMSession) SaveBeforeHistory() {
	s.savedMode = s.mode()
}

func (s *LLMSession) RestoreAfterHistory() {
	s.oneshotMode = s.savedMode
	s.savedMode = modeUnset
}

func LLMLoop(ctx context.Context, engineClient *client.Client) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	dag := engineClient.Dagger()

	// start a new LLM session, which we'll reassign throughout
	s, err := NewLLMSession(ctx, dag, llmModel)
	if err != nil {
		return err
	}

	// give ourselves a blank slate by zooming into a passthrough span
	ctx, span := Tracer().Start(ctx, "llm", telemetry.Passthrough())
	defer telemetry.End(span, func() error { return nil })
	Frontend.SetPrimary(dagui.SpanID{SpanID: span.SpanContext().SpanID()})

	// TODO: initialize LLM with current module, matching shell behavior?

	// start the shell loop
	Frontend.Shell(ctx, &LLMShellHandler{
		s:      s,
		cancel: cancel,
	})

	return nil
}

type interpreterMode int

const (
	modeUnset interpreterMode = iota
	modePrompt
	modeShell
)

type LLMSession struct {
	undo           *LLMSession
	dag            *dagger.Client
	llm            *dagger.Llm
	llmModel       string
	shell          *shellCallHandler
	persistentMode interpreterMode
	oneshotMode    interpreterMode
	// mode from before going through history
	savedMode  interpreterMode
	syncedVars map[string]digest.Digest
}

func NewLLMSession(ctx context.Context, dag *dagger.Client, llmModel string) (*LLMSession, error) {
	shellHandler := &shellCallHandler{
		dag:   dag,
		debug: debug,
	}

	if err := shellHandler.Initialize(ctx); err != nil {
		return nil, err
	}

	initialVars := make(map[string]digest.Digest)
	// HACK: pretend we synced the initial env, we don't want to just toss the
	// entire os.Environ into the LLM
	for k, v := range shellHandler.runner.Env.Each {
		initialVars[k] = dagql.HashFrom(v.String())
	}
	for k, v := range shellHandler.runner.Vars {
		initialVars[k] = dagql.HashFrom(v.String())
	}

	s := &LLMSession{
		dag:            dag,
		llmModel:       llmModel,
		shell:          shellHandler,
		persistentMode: modePrompt,
		syncedVars:     initialVars,
	}
	s.reset()

	// figure out what the model resolved to
	model, err := s.llm.Model(ctx)
	if err != nil {
		return nil, err
	}
	s.llmModel = model

	return s, nil
}

func (s *LLMSession) reset() {
	s.llm = s.dag.Llm(dagger.LlmOpts{Model: s.llmModel})
}

func (s *LLMSession) mode() interpreterMode {
	if s.oneshotMode != modeUnset {
		return s.oneshotMode
	}
	return s.persistentMode
}

func (s *LLMSession) Fork() *LLMSession {
	cp := *s
	cp.undo = s
	return &cp
}

var slashCommands = []slashCommand{
	{
		name:    "/undo",
		desc:    "Undo the last command",
		handler: (*LLMSession).Undo,
	},
	{
		name:    "/shell",
		desc:    "Switch into shell mode",
		handler: (*LLMSession).ShellMode,
	},
	{
		name:    "/prompt",
		desc:    "Switch into prompt mode",
		handler: (*LLMSession).PromptMode,
	},
	{
		name:    "/clear",
		desc:    "Clear the LLM history",
		handler: (*LLMSession).Clear,
	},
	{
		name:    "/compact",
		desc:    "Compact the LLM history",
		handler: (*LLMSession).Compact,
	},
	{
		name:    "/history",
		desc:    "Show the LLM history",
		handler: (*LLMSession).History,
	},
	{
		name:    "/model",
		desc:    "Swap out the LLM model",
		handler: (*LLMSession).Model,
	},
}

func (s *LLMSession) Interpret(ctx context.Context, input string) (ret *LLMSession, rerr error) {
	if strings.TrimSpace(input) == "" {
		return s, nil
	}

	ctx, span := Tracer().Start(ctx, input)
	defer telemetry.End(span, func() error { return rerr })
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()

	if strings.HasPrefix(input, "/") {
		for _, cmd := range slashCommands {
			if strings.HasPrefix(input, cmd.name) {
				input = strings.TrimSpace(strings.TrimPrefix(input, cmd.name))
				return cmd.handler(s, ctx, input)
			}
		}
		return s, fmt.Errorf("unknown slash command: %s", input)
	}

	// reset any oneshot mode after the command is interpreted
	defer func() {
		if ret.oneshotMode != modeUnset {
			ret.oneshotMode = modeUnset
		}
	}()

	switch s.mode() {
	case modePrompt:
		return s.interpretPrompt(ctx, input)
	case modeShell:
		return s.interpretShell(ctx, input)
	default:
		return s, fmt.Errorf("unknown mode: %d", s.mode())
	}
}

func (s *LLMSession) interpretPrompt(ctx context.Context, input string) (*LLMSession, error) {
	s = s.Fork()

	prompted, err := s.llm.WithPrompt(input).Sync(ctx)
	if err != nil {
		return s, err
	}

	s.llm = prompted

	vars, err := s.llm.Variables(ctx)
	if err != nil {
		return s, err
	}

	for _, v := range vars {
		name, err := v.Name(ctx)
		if err != nil {
			dbg.Println("error getting var name", err)
			return s, err
		}
		typeName, err := v.TypeName(ctx)
		if err != nil {
			dbg.Println("error getting var type name", err)
			return s, err
		}
		hash, err := v.Hash(ctx)
		if err != nil {
			dbg.Println("error getting var hash", err)
			return s, err
		}
		digest := digest.Digest(hash)

		if s.syncedVars[name] == digest {
			// already synced
			continue
		}

		dbg.Println("syncing llm => var", name, typeName, hash)

		switch typeName {
		case "String":
			val, err := s.llm.GetString(ctx, name)
			if err != nil {
				return s, err
			}
			dbg.Println("syncing string", name, val)
			// TODO: maybe there's a better way to set this
			s.shell.runner.Vars[name] = expand.Variable{
				Kind: expand.String,
				Str:  val,
			}
		default:
			llmId, err := s.llm.ID(ctx)
			if err != nil {
				return s, err
			}
			var objId string
			if err := s.dag.QueryBuilder().
				Select("loadLlmFromID").
				Arg("id", llmId).
				Select(fmt.Sprintf("get%s", typeName)).
				Arg("name", name).
				Select("id").
				Bind(&objId).
				Execute(ctx); err != nil {
				return s, err
			}
			dbg.Println("syncing object", name)
			var buf bytes.Buffer
			st := ShellState{
				Calls: []FunctionCall{
					// not sure this is right
					{
						Object: "Query",
						Name:   "load" + typeName + "FromID",
						Arguments: map[string]any{
							"id": objId,
						},
						ReturnObject: typeName,
					},
				},
			}
			if err := st.WriteTo(&buf); err != nil {
				return s, err
			}
			quoted, err := syntax.Quote(buf.String(), syntax.LangBash)
			if err != nil {
				return s, err
			}
			if _, _, err := s.shell.Eval(ctx, fmt.Sprintf("%s=%s", name, quoted)); err != nil {
				return s, err
			}
		}
		s.syncedVars[name] = digest
	}
	return s, nil
}

func (s *LLMSession) interpretShell(ctx context.Context, input string) (*LLMSession, error) {
	_, _, err := s.shell.Eval(ctx, input)
	if err != nil {
		return s, err
	}
	// TODO: is there anything useful to do with the result here?
	return s.syncVars(ctx)
}

var dbg *log.Logger

func init() {
	if fn := os.Getenv("DAGUI_DEBUG"); fn != "" {
		debugFile, _ := os.Create(fn)
		dbg = log.New(debugFile, "", 0)
	} else {
		dbg = log.New(io.Discard, "", 0)
	}
}

var skipEnv = map[string]bool{
	// these vars are set by the sh package
	"GID":    true,
	"UID":    true,
	"EUID":   true,
	"OPTIND": true,
	"IFS":    true,
	// the rest should be filtered out already by skipping the first batch
	// (sourced from os.Environ)
}

func (s *LLMSession) syncVars(ctx context.Context) (*LLMSession, error) {
	oldVars := s.syncedVars
	s = s.Fork()
	s.syncedVars = make(map[string]digest.Digest)
	// TODO: overlay? bad scaling characteristics. maybe overkill anyway
	for k, v := range oldVars {
		s.syncedVars[k] = v
	}

	llmId, err := s.llm.ID(ctx)
	if err != nil {
		return s, err
	}
	syncedLlmQ := s.dag.QueryBuilder().
		Select("loadLlmFromID").
		Arg("id", llmId)

	var changed bool
	for name, value := range s.shell.runner.Vars {
		if s.syncedVars[name] == dagql.HashFrom(value.String()) {
			continue
		}
		if skipEnv[name] {
			continue
		}

		dbg.Printf("syncing var %q => llm\n", name)

		changed = true

		if strings.HasPrefix(value.String(), shellStatePrefix) {
			w := strings.NewReader(value.String())
			v, t, err := s.shell.Result(ctx, w, func(_ context.Context, q *querybuilder.Selection, t *modTypeDef) (*querybuilder.Selection, error) {
				// When an argument returns an object, assume we want its ID
				if t.AsFunctionProvider() != nil {
					q = q.Select("id")
				}
				return q, nil
			})
			if err != nil {
				return s, err
			}
			if v == nil {
				return s, fmt.Errorf("unexpected nil value for var %q", name)
			}
			digest, err := idDigest(v.(string))
			if err != nil {
				return s, err
			}
			if t.AsFunctionProvider() != nil {
				typeName := t.Name()
				syncedLlmQ = syncedLlmQ.
					Select(fmt.Sprintf("set%s", typeName)).
					Arg("name", name).
					Arg("value", v)
			}
			s.syncedVars[name] = digest
		} else {
			s.syncedVars[name] = dagql.HashFrom(value.String())
			syncedLlmQ = syncedLlmQ.
				Select("setString").
				Arg("name", name).
				Arg("value", value.String())
		}
	}
	if !changed {
		return s, nil
	}
	if err := syncedLlmQ.Select("id").Bind(&llmId).Execute(ctx); err != nil {
		return s, err
	}
	s.llm = s.dag.LoadLlmFromID(llmId)
	return s, nil
}

func (s *LLMSession) Undo(ctx context.Context, _ string) (*LLMSession, error) {
	if s.undo == nil {
		return s, nil
	}
	return s.undo, nil
}

// TODO: maybe these go away and instead we sync the env
// func (s *LLMSession) With(ctx context.Context, args string) (*LLMSession, error) {
// 	s, err := s.Set(ctx, "_ "+args)
// 	if err != nil {
// 		return s, err
// 	}
// 	return s.Get(ctx, "_")
// }

// TODO: maybe these go away and instead we sync the env
// func (s *LLMSession) Set(ctx context.Context, args string) (*LLMSession, error) {
// 	name, script, ok := strings.Cut(args, " ")
// 	if !ok {
// 		return s, fmt.Errorf("expected name and script")
// 	}
// 	resp, typeDef, err := s.shell.Eval(ctx, script)
// 	if err != nil {
// 		return s, err
// 	}
// 	if typeDef.AsFunctionProvider() != nil {
// 		llmId, err := s.llm.ID(ctx)
// 		if err != nil {
// 			return s, err
// 		}
// 		s = s.Fork()
// 		if err := s.dag.QueryBuilder().
// 			Select("loadLlmFromID").
// 			Arg("id", llmId).
// 			Select(fmt.Sprintf("set%s", typeDef.Name())).
// 			Arg("name", name).
// 			Arg("value", resp).
// 			Select("id").
// 			Bind(&llmId).
// 			Execute(ctx); err != nil {
// 			return s, err
// 		}
// 		s.llm = s.dag.LoadLlmFromID(llmId)
// 		return s, nil
// 	}
// 	return s, fmt.Errorf("cannot change scope to %s - script must return an Object type", typeDef.Name())
// }

// TODO: maybe these go away and instead we sync the env
// func (s *LLMSession) Get(ctx context.Context, name string) (*LLMSession, error) {
// 	s = s.Fork()
// 	llmId, err := s.llm.ID(ctx)
// 	if err != nil {
// 		return s, err
// 	}
// 	s = s.Fork()
// 	if err := s.dag.QueryBuilder().
// 		Select("loadLlmFromID").
// 		Arg("id", llmId).
// 		Select(fmt.Sprintf("get%s", typeDef.Name())).
// 		Arg("name", name).
// 		Select("id").
// 		Bind(&llmId).
// 		Execute(ctx); err != nil {
// 		return s, err
// 	}
// 	s.llm = s.dag.LoadLlmFromID(llmId)
// 	return s, nil
// }

func (s *LLMSession) Complete(entireInput [][]rune, row, col int) (msg string, comp editline.Completions) {
	if s.mode() == modeShell {
		return s.shell.AutoComplete(entireInput, row, col)
	}
	word, wstart, wend := computil.FindWord(entireInput, row, col)
	if !strings.HasPrefix(word, "/") {
		return "", nil
	}
	var commands []slashCommand
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd.name, string(word)) {
			commands = append(commands, cmd)
		}
	}
	return "", &slashCompletions{groups: []slashCommandGroup{
		{name: "", commands: commands},
	}, cursor: col, start: wstart, end: wend}
}

func (s *LLMSession) IsComplete(entireInput [][]rune, line int, col int) bool {
	if s.mode() == modeShell {
		return s.shell.IsComplete(entireInput, line, col)
	}
	return true
}

func (s *LLMSession) Clear(ctx context.Context, _ string) (_ *LLMSession, rerr error) {
	s.reset()
	return s, nil
}

var compact = `Please summarize our conversation so far into a concise context that:

1. Preserves all critical information including:
   - Key questions asked and answers provided
   - Important code snippets and their purposes
   - Project structure and technical details
   - Decisions made and rationales

2. Condenses or removes:
   - Verbose explanations
   - Redundant information
   - Preliminary explorations that didn't lead anywhere
   - Courtesy exchanges and non-technical chat

3. Maintains awareness of file changes:
   - Track which files have been viewed, created, or modified
   - Remember the current state of important files
   - Preserve knowledge of project structure

4. Formats the summary in a structured way:
   - Project context (language, framework, objectives)
   - Current task status
   - Key technical details discovered
   - Next steps or pending questions

Present this summary in a compact form that retains all essential context needed to continue our work effectively, then continue our conversation from this point forward as if we had the complete conversation history.
	
This will be a note to yourself, not shown to the user, so prioritize your own understanding and don't ask any questions, because they won't be seen by anyone.
`

func (s *LLMSession) Compact(ctx context.Context, _ string) (_ *LLMSession, rerr error) {
	ctx, span := Tracer().Start(ctx, "compact", telemetry.Internal(), telemetry.Encapsulate())
	defer telemetry.End(span, func() error { return rerr })
	summary, err := s.llm.WithPrompt(compact).LastReply(ctx)
	if err != nil {
		return s, err
	}
	fresh := s.dag.Llm(dagger.LlmOpts{
		Model: s.llmModel,
	})
	compacted, err := fresh.WithPrompt(summary).Sync(ctx)
	if err != nil {
		return s, err
	}
	// TODO: restore previous state
	s.llm = compacted
	return s, nil
}

func (s *LLMSession) History(ctx context.Context, _ string) (*LLMSession, error) {
	history, err := s.llm.History(ctx)
	if err != nil {
		return s, err
	}
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	for _, h := range history {
		fmt.Fprintln(stdio.Stdout, h)
	}
	return s, nil
}

func (s *LLMSession) ShellMode(ctx context.Context, script string) (*LLMSession, error) {
	if script != "" {
		return s.interpretShell(ctx, script)
	}
	s = s.Fork()
	s.persistentMode = modeShell
	s.oneshotMode = modeUnset
	return s, nil
}

func (s *LLMSession) PromptMode(ctx context.Context, prompt string) (*LLMSession, error) {
	if prompt != "" {
		return s.interpretPrompt(ctx, prompt)
	}
	s = s.Fork()
	s.persistentMode = modePrompt
	s.oneshotMode = modeUnset
	return s, nil
}

func (s *LLMSession) Prompt(out idtui.TermOutput, fg termenv.Color) string {
	switch s.mode() {
	case modePrompt:
		sb := new(strings.Builder)
		sb.WriteString(out.String(s.llmModel).Bold().Foreground(termenv.ANSICyan).String())
		sb.WriteString(out.String(" ").String())
		sb.WriteString(out.String(idtui.LLMPrompt).Bold().Foreground(fg).String())
		sb.WriteString(out.String(out.String(" ").String()).String())
		return sb.String()
	case modeShell:
		return s.shell.Prompt(out, fg)
	default:
		return fmt.Sprintf("unknown mode: %d", s.mode())
	}
}

func (s *LLMSession) Model(ctx context.Context, model string) (*LLMSession, error) {
	s = s.Fork()
	s.llm = s.llm.WithModel(model)
	model, err := s.llm.Model(ctx)
	if err != nil {
		return nil, err
	}
	s.llmModel = model
	return s, nil
}

type slashCommand struct {
	name    string
	desc    string
	handler func(s *LLMSession, ctx context.Context, script string) (*LLMSession, error)
}

type slashCompletions struct {
	groups             []slashCommandGroup
	cursor, start, end int
}

type slashCommandGroup struct {
	name     string
	commands []slashCommand
}

var _ editline.Completions = (*slashCompletions)(nil)

func (c *slashCompletions) NumCategories() int {
	return len(c.groups)
}

func (c *slashCompletions) CategoryTitle(catIdx int) string {
	return c.groups[catIdx].name
}

func (c *slashCompletions) NumEntries(catIdx int) int {
	return len(c.groups[catIdx].commands)
}

func (c *slashCompletions) Entry(catIdx, entryIdx int) complete.Entry {
	return &slashCompletion{c, &c.groups[catIdx].commands[entryIdx]}
}

func (c *slashCompletions) Candidate(e complete.Entry) editline.Candidate {
	return e.(*slashCompletion)
}

type slashCompletion struct {
	s   *slashCompletions
	cmd *slashCommand
}

var _ complete.Entry = (*slashCompletion)(nil)

func (c *slashCompletion) Title() string {
	return c.cmd.name
}

func (c *slashCompletion) Description() string {
	return c.cmd.desc
}

var _ editline.Candidate = (*slashCompletion)(nil)

func (c *slashCompletion) Replacement() string {
	return c.cmd.name
}

func (c *slashCompletion) MoveRight() int {
	return c.s.end - c.s.cursor
}

func (c *slashCompletion) DeleteLeft() int {
	return c.s.end - c.s.start
}
