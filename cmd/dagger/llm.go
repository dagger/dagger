package main

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
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

	// give ourselves a blank slate by zooming into a passthrough span
	shellCtx, shellSpan := Tracer().Start(ctx, "llm", telemetry.Passthrough())
	defer telemetry.End(shellSpan, func() error { return nil })
	Frontend.SetPrimary(dagui.SpanID{SpanID: shellSpan.SpanContext().SpanID()})

	// start a new LLM session, which we'll reassign throughout
	s, err := NewLLMSession(ctx, dag, dag.Llm(dagger.LlmOpts{
		Model: llmModel,
	}))
	if err != nil {
		return err
	}

	// TODO: initialize LLM with current module, matching shell behavior?

	// start the shell loop
	Frontend.Shell(shellCtx,
		func(ctx context.Context, line string) (rerr error) {
			if line == "exit" {
				cancel()
				return nil
			}

			var err error
			s, err = s.Interpret(ctx, line)
			if err != nil {
				return err
			}

			return nil
		},
		s.Complete,
		s.IsComplete,
		func(out idtui.TermOutput, fg termenv.Color) string {
			return out.String(idtui.PromptSymbol + " ").Foreground(fg).String()
		},
	)

	return nil
}

type LLMSession struct {
	undo      *LLMSession
	dag       *dagger.Client
	llm       *dagger.Llm
	llmId     dagger.LlmID
	shell     *shellCallHandler
	completer editline.AutoCompleteFn
}

func NewLLMSession(ctx context.Context, dag *dagger.Client, llm *dagger.Llm) (*LLMSession, error) {
	id, err := llm.ID(ctx)
	if err != nil {
		return nil, err
	}

	shellHandler := &shellCallHandler{
		dag:   dag,
		debug: debug,
	}

	shellCompletion := &shellAutoComplete{shellHandler}

	if err := shellHandler.Initialize(ctx); err != nil {
		return nil, err
	}

	return &LLMSession{
		dag:       dag,
		llm:       llm,
		llmId:     id,
		shell:     shellHandler,
		completer: shellCompletion.Do,
	}, nil
}

func (s *LLMSession) Fork() *LLMSession {
	cp := *s
	cp.undo = s
	return &cp
}

var slashCommands = []slashCommand{
	{
		name:    "/with",
		desc:    "Change the scope of the LLM",
		handler: (*LLMSession).With,
	},
	{
		name:    "/undo",
		desc:    "Undo the last command",
		handler: (*LLMSession).Undo,
	},
	// TODO: /history, /compact, /shell, ???
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

	s = s.Fork()

	prompted, err := s.llm.WithPrompt(input).Sync(ctx)
	if err != nil {
		return s, err
	}

	s.llm = prompted

	return s, nil
}

func (s *LLMSession) Undo(ctx context.Context, _ string) (*LLMSession, error) {
	return s.undo, nil
}

func (s *LLMSession) With(ctx context.Context, script string) (*LLMSession, error) {
	resp, typeDef, err := s.shell.Eval(ctx, script)
	if err != nil {
		return s, err
	}
	if typeDef.AsFunctionProvider() != nil {
		s = s.Fork()
		if err := s.dag.QueryBuilder().
			Select("loadLlmFromID").
			Arg("id", s.llmId).
			Select(fmt.Sprintf("with%s", typeDef.Name())).
			Arg("value", resp).
			Select("id").
			Bind(&s.llmId).
			Execute(ctx); err != nil {
			return s, err
		}
		s.llm = s.dag.LoadLlmFromID(s.llmId)
		return s, nil
	}
	return s, fmt.Errorf("cannot change scope to %s - script must return an Object type", typeDef.Name())
}

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
