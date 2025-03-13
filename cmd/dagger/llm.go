package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/muesli/termenv"
	"github.com/opencontainers/go-digest"
	"github.com/vito/bubbline/complete"
	"github.com/vito/bubbline/editline"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

type interpreterMode int

const (
	modeUnset interpreterMode = iota
	modePrompt
	modeShell
)

func (m interpreterMode) String() string {
	switch m {
	case modeUnset:
		return "unset"
	case modePrompt:
		return "prompt"
	case modeShell:
		return "shell"
	default:
		return fmt.Sprintf("unknown(%d)", m)
	}
}

type LLMSession struct {
	undo       *LLMSession
	dag        *dagger.Client
	llm        *dagger.Llm
	model      string
	syncedVars map[string]digest.Digest
	shell      *shellCallHandler
}

func NewLLMSession(ctx context.Context, dag *dagger.Client, llmModel string, shellHandler *shellCallHandler) (*LLMSession, error) {
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
		dag:        dag,
		model:      llmModel,
		syncedVars: initialVars,
		shell:      shellHandler,
	}
	s.reset()

	// figure out what the model resolved to
	model, err := s.llm.Model(ctx)
	if err != nil {
		return nil, err
	}
	s.model = model

	return s, nil
}

func (s *LLMSession) reset() {
	s.llm = s.dag.Llm(dagger.LlmOpts{Model: s.model})
}

func (s *LLMSession) Fork() *LLMSession {
	cp := *s
	cp.undo = s
	return &cp
}

// var slashCommands = []slashCommand{
// 	{
// 		name:    "/shell",
// 		desc:    "Switch into shell mode",
// 		handler: (*shellCallHandler).ShellMode,
// 	},
// 	{
// 		name:    "/prompt",
// 		desc:    "Switch into prompt mode",
// 		handler: (*shellCallHandler).PromptMode,
// 	},
// 	{
// 		name:    "/clear",
// 		desc:    "Clear the LLM history",
// 		handler: (*shellCallHandler).Clear,
// 	},
// 	{
// 		name:    "/compact",
// 		desc:    "Compact the LLM history",
// 		handler: (*shellCallHandler).Compact,
// 	},
// 	{
// 		name:    "/history",
// 		desc:    "Show the LLM history",
// 		handler: (*shellCallHandler).History,
// 	},
// 	{
// 		name:    "/model",
// 		desc:    "Swap out the LLM model",
// 		handler: (*LLMSession).Model,
// 	},
// }

func (s *LLMSession) WithPrompt(ctx context.Context, input string) (*LLMSession, error) {
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
			var objId string
			if err := s.dag.QueryBuilder().
				Select("loadLlmFromID").
				Arg("id", s.llm).
				Select(fmt.Sprintf("get%s", typeName)).
				Arg("name", name).
				Select("id").
				Bind(&objId).
				Execute(ctx); err != nil {
				return s, err
			}
			dbg.Println("syncing object", name)
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
			key := s.shell.state.Store(st)
			quoted, err := syntax.Quote(newStateToken(key), syntax.LangBash)
			if err != nil {
				return s, err
			}
			if err := s.shell.Eval(ctx, fmt.Sprintf("%s=%s", name, quoted)); err != nil {
				return s, err
			}
		}
		s.syncedVars[name] = digest
	}
	return s, nil
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

func (s *LLMSession) syncVarsToLLM(ctx context.Context) (*LLMSession, error) {
	s = s.Fork()

	// TODO: overlay? bad scaling characteristics. maybe overkill anyway
	oldVars := s.syncedVars
	s.syncedVars = make(map[string]digest.Digest)
	maps.Copy(s.syncedVars, oldVars)

	syncedLlmQ := s.dag.QueryBuilder().
		Select("loadLlmFromID").
		Arg("id", s.llm)

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

		if key := GetStateKey(value.String()); key != "" {
			st, err := s.shell.state.Load(key)
			if err != nil {
				return s, err
			}
			q := st.QueryBuilder(s.dag)
			modDef := s.shell.GetDef(st)
			typeDef, err := st.GetTypeDef(modDef)
			if err != nil {
				return s, err
			}
			if typeDef.AsFunctionProvider() != nil {
				var id string
				if err := q.Select("id").Bind(&id).Execute(ctx); err != nil {
					return s, err
				}
				digest, err := idDigest(id)
				if err != nil {
					return s, err
				}
				typeName := typeDef.Name()
				syncedLlmQ = syncedLlmQ.
					Select(fmt.Sprintf("set%s", typeName)).
					Arg("name", name).
					Arg("value", id)
				s.syncedVars[name] = digest
			}
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
	var llmId dagger.LlmID
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
		Model: s.model,
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

func (s *LLMSession) Prompt(out idtui.TermOutput, fg termenv.Color) string {
	sb := new(strings.Builder)
	sb.WriteString(termenv.CSI + termenv.ResetSeq + "m") // clear background
	sb.WriteString(out.String(s.model).Bold().Foreground(termenv.ANSICyan).String())
	sb.WriteString(out.String(" ").String())
	sb.WriteString(out.String(idtui.LLMPrompt).Bold().Foreground(fg).String())
	sb.WriteString(out.String(out.String(" ").String()).String())
	return sb.String()
}

func (s *LLMSession) Model(ctx context.Context, model string) (*LLMSession, error) {
	s = s.Fork()
	s.llm = s.llm.WithModel(model)
	model, err := s.llm.Model(ctx)
	if err != nil {
		return nil, err
	}
	s.model = model
	return s, nil
}

type slashCommand struct {
	name    string
	desc    string
	handler func(s *shellCallHandler, ctx context.Context, script string) error
}

type varCompletions struct {
	groups             []slashCommandGroup
	cursor, start, end int
}

type slashCommandGroup struct {
	name     string
	commands []slashCommand
}

var _ editline.Completions = (*varCompletions)(nil)

func (c *varCompletions) NumCategories() int {
	return len(c.groups)
}

func (c *varCompletions) CategoryTitle(catIdx int) string {
	return c.groups[catIdx].name
}

func (c *varCompletions) NumEntries(catIdx int) int {
	return len(c.groups[catIdx].commands)
}

func (c *varCompletions) Entry(catIdx, entryIdx int) complete.Entry {
	return &slashCompletion{c, &c.groups[catIdx].commands[entryIdx]}
}

func (c *varCompletions) Candidate(e complete.Entry) editline.Candidate {
	return e.(*slashCompletion)
}

type slashCompletion struct {
	s   *varCompletions
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
