package main

import (
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
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/client"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/vito/bubbline/complete"
	"github.com/vito/bubbline/computil"
	"github.com/vito/bubbline/editline"
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
	Frontend.Shell(ctx,
		func(ctx context.Context, line string) (rerr error) {
			if line == "/exit" {
				cancel()
				return nil
			}
			new, err := s.Interpret(ctx, line)
			if err != nil {
				return err
			}
			s = new
			return nil
		},
		// NOTE: these close over s
		func(entireInput [][]rune, row, col int) (msg string, comp editline.Completions) {
			return s.Complete(entireInput, row, col)
		},
		func(entireInput [][]rune, row, col int) bool {
			return s.IsComplete(entireInput, row, col)
		},
		func(out idtui.TermOutput, fg termenv.Color) string {
			return s.Prompt(out, fg)
		},
	)

	return nil
}

type interpreterMode int

const (
	modePrompt interpreterMode = iota
	modeShell
)

type LLMSession struct {
	undo       *LLMSession
	dag        *dagger.Client
	llm        *dagger.Llm
	llmModel   string
	shell      *shellCallHandler
	completer  editline.AutoCompleteFn
	mode       interpreterMode
	syncedVars map[string]string
}

func NewLLMSession(ctx context.Context, dag *dagger.Client, llmModel string) (*LLMSession, error) {
	shellHandler := &shellCallHandler{
		dag:   dag,
		debug: debug,
	}

	shellCompletion := &shellAutoComplete{shellHandler}

	if err := shellHandler.Initialize(ctx); err != nil {
		return nil, err
	}

	initialVars := make(map[string]string)
	// HACK: pretend we synced the initial env, we don't want to just toss the
	// entire os.Environ into the LLM
	for k, v := range shellHandler.runner.Env.Each {
		initialVars[k] = v.String()
	}
	for k, v := range shellHandler.runner.Vars {
		initialVars[k] = v.String()
	}

	s := &LLMSession{
		dag:        dag,
		llmModel:   llmModel,
		shell:      shellHandler,
		completer:  shellCompletion.Do,
		mode:       modePrompt,
		syncedVars: initialVars,
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

//go:embed llm.md
var llmPrompt string

func (s *LLMSession) reset() {
	s.llm = s.dag.
		Llm(dagger.LlmOpts{Model: s.llmModel}).
		WithPrompt(llmPrompt)
}

func (s *LLMSession) Fork() *LLMSession {
	cp := *s
	cp.undo = s
	return &cp
}

var slashCommands = []slashCommand{
	// {
	// 	name:    "/with",
	// 	desc:    "Change the scope of the LLM",
	// 	handler: (*LLMSession).With,
	// },
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

func (s *LLMSession) Interpret(ctx context.Context, input string) (_ *LLMSession, rerr error) {
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

	switch s.mode {
	case modePrompt:
		return s.interpretPrompt(ctx, input)
	case modeShell:
		return s.interpretShell(ctx, input)
	default:
		return s, fmt.Errorf("unknown mode: %d", s.mode)
	}
}

func (s *LLMSession) interpretPrompt(ctx context.Context, input string) (*LLMSession, error) {
	s = s.Fork()

	prompted, err := s.llm.WithPrompt(input).Sync(ctx)
	if err != nil {
		return s, err
	}

	s.llm = prompted

	return s, nil
}

func (s *LLMSession) interpretShell(ctx context.Context, input string) (*LLMSession, error) {
	_, _, err := s.shell.Eval(ctx, input)
	if err != nil {
		return s, err
	}
	// if typeDef == nil {
	// 	dbg.Printf("interpretShell: %s: %+v\n", input, resp)
	// 	return s, nil
	// }
	// if typeDef.AsFunctionProvider() != nil {
	// 	llmId, err := s.llm.ID(ctx)
	// 	if err != nil {
	// 		return s, err
	// 	}
	// 	s = s.Fork()
	// 	if err := s.dag.QueryBuilder().
	// 		Select("loadLlmFromID").
	// 		Arg("id", llmId).
	// 		Select(fmt.Sprintf("set%s", typeDef.Name())).
	// 		Arg("name", "_").
	// 		Arg("value", resp).
	// 		Select("id").
	// 		Bind(&llmId).
	// 		Execute(ctx); err != nil {
	// 		return s, err
	// 	}
	// 	s.llm = s.dag.LoadLlmFromID(llmId)
	// }
	return s.syncEnv(ctx)
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
	// the rest should be picked up from os.Environ
}

func (s *LLMSession) syncEnv(ctx context.Context) (*LLMSession, error) {
	oldVars := s.syncedVars
	s = s.Fork()
	s.syncedVars = make(map[string]string)
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
		if s.syncedVars[name] == value.String() {
			continue
		}
		if skipEnv[name] {
			continue
		}

		dbg.Printf("syncEnv var diff: %s=%s (%+v)\n", name, value.String(), value)
		s.syncedVars[name] = value.String()

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
			dbg.Printf("syncEnv var %q: %T %+v\n", name, v, v)
			if v == nil {
				return s, fmt.Errorf("unexpected nil value for var %q", name)
			}
			if t.AsFunctionProvider() != nil {
				typeName := t.Name()
				syncedLlmQ = syncedLlmQ.
					Select(fmt.Sprintf("set%s", typeName)).
					Arg("name", name).
					Arg("value", v)
			}
		} else {
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
	if input, l, c, ok := stripCommandPrefix("/with ", entireInput, row, col); ok {
		return s.completer(input, l, c)
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
	if input, l, c, ok := stripCommandPrefix("/with ", entireInput, line, col); ok {
		return shellIsComplete(input, l, c)
	}
	return true
}

func (s *LLMSession) Clear(ctx context.Context, _ string) (_ *LLMSession, rerr error) {
	s.llm = s.dag.Llm(dagger.LlmOpts{
		Model: s.llmModel,
	})
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

func (s *LLMSession) ShellMode(ctx context.Context, _ string) (*LLMSession, error) {
	s = s.Fork()
	s.mode = modeShell
	return s, nil
}

func (s *LLMSession) PromptMode(ctx context.Context, _ string) (*LLMSession, error) {
	s = s.Fork()
	s.mode = modePrompt
	return s, nil
}

func (s *LLMSession) Prompt(out idtui.TermOutput, fg termenv.Color) string {
	switch s.mode {
	case modePrompt:
		sb := new(strings.Builder)
		sb.WriteString(out.String(s.llmModel).Bold().Foreground(termenv.ANSICyan).String())
		sb.WriteString(out.String(" ").String())
		sb.WriteString(out.String(idtui.LLMPrompt).Bold().Foreground(fg).String())
		sb.WriteString(out.String(out.String(" ").String()).String())
		return sb.String()
	case modeShell:
		return s.shell.prompt(out, fg)
	default:
		return fmt.Sprintf("unknown mode: %d", s.mode)
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

func stripCommandPrefix(prefix string, entireInput [][]rune, line, col int) ([][]rune, int, int, bool) {
	if len(entireInput) == 0 {
		return entireInput, line, col, false
	}
	firstLine := string(entireInput[0])
	if strings.HasPrefix(firstLine, prefix) {
		strippedLine := strings.TrimSpace(strings.TrimPrefix(firstLine, prefix))
		strippedInput := [][]rune{[]rune(strippedLine)}
		strippedInput = append(strippedInput, entireInput[1:]...)
		if line == 0 {
			col -= len(prefix)
		}
		return strippedInput, line, col, true
	}
	return entireInput, line, col, false
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
