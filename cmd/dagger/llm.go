package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"maps"
	"os"

	"dagger.io/dagger"
	"dagger.io/dagger/querybuilder"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql"
	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
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

func (m interpreterMode) ContentType() string {
	switch m {
	case modeShell:
		return "text/x-shellscript"
	case modePrompt:
		return "text/markdown"
	default:
		return "text/plain"
	}
}

type LLMSession struct {
	undo       *LLMSession
	dag        *dagger.Client
	llm        *dagger.LLM
	model      string
	skipEnv    map[string]bool
	syncedVars map[string]digest.Digest
	shell      *shellCallHandler
}

func NewLLMSession(ctx context.Context, dag *dagger.Client, llmModel string, shellHandler *shellCallHandler) (*LLMSession, error) {
	s := &LLMSession{
		dag:        dag,
		model:      llmModel,
		syncedVars: map[string]digest.Digest{},
		skipEnv: map[string]bool{
			// these vars are set by the sh package
			"GID":    true,
			"UID":    true,
			"EUID":   true,
			"OPTIND": true,
			"IFS":    true,
			// the rest should be filtered out already by skipping the first batch
			// (sourced from os.Environ)
		},
		shell: shellHandler,
	}

	// don't sync the initial env vars
	for k := range shellHandler.runner.Env.Each {
		s.skipEnv[k] = true
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
	s.llm = s.dag.LLM(dagger.LLMOpts{Model: s.model}).WithQuery()
}

func (s *LLMSession) Fork() *LLMSession {
	cp := *s
	cp.undo = s
	return &cp
}

func (s *LLMSession) WithPrompt(ctx context.Context, input string) (*LLMSession, error) {
	s = s.Fork()

	if err := s.syncVarsToLLM(ctx); err != nil {
		return s, err
	}

	prompted, err := s.llm.WithPrompt(input).Sync(ctx)
	if err != nil {
		return s, err
	}

	s.llm = prompted

	if err := s.syncVarsFromLLM(ctx); err != nil {
		return s, err
	}

	return s, nil
}

func (s *LLMSession) WithState(ctx context.Context, typeName, id string) (*LLMSession, error) {
	s = s.Fork()

	var llmID dagger.LLMID
	updatedLLM := s.dag.QueryBuilder().
		Select("loadLLMFromID").
		Arg("id", s.llm).
		Select(fmt.Sprintf("with%s", typeName)).
		Arg("value", id).
		Select("id").
		Bind(&llmID)

	if err := updatedLLM.Execute(ctx); err != nil {
		return s, err
	}

	s.llm = s.dag.LoadLLMFromID(llmID)
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

func (s *LLMSession) syncVarsToLLM(ctx context.Context) error {
	// TODO: overlay? bad scaling characteristics. maybe overkill anyway
	oldVars := s.syncedVars
	s.syncedVars = make(map[string]digest.Digest)
	maps.Copy(s.syncedVars, oldVars)

	syncedLLMQ := s.dag.QueryBuilder().
		Select("loadLLMFromID").
		Arg("id", s.llm)

	var changed bool
	for name, value := range s.shell.runner.Vars {
		if s.skipEnv[name] {
			continue
		}

		if s.syncedVars[name] == dagql.HashFrom(value.String()) {
			continue
		}

		dbg.Printf("syncing var %q => llm\n", name)

		changed = true

		if key := GetStateKey(value.String()); key != "" {
			st, err := s.shell.state.Load(key)
			if err != nil {
				return err
			}
			q := st.QueryBuilder(s.dag)
			modDef := s.shell.GetDef(st)
			typeDef, err := st.GetTypeDef(modDef)
			if err != nil {
				return err
			}
			if typeDef.AsFunctionProvider() != nil {
				var id string
				if err := q.Select("id").Bind(&id).Execute(ctx); err != nil {
					return err
				}
				digest, err := idDigest(id)
				if err != nil {
					return err
				}
				typeName := typeDef.Name()
				syncedLLMQ = syncedLLMQ.
					Select(fmt.Sprintf("set%s", typeName)).
					Arg("name", name).
					Arg("value", id)
				s.syncedVars[name] = digest
			}
		} else {
			s.syncedVars[name] = dagql.HashFrom(value.String())
			syncedLLMQ = syncedLLMQ.
				Select("setString").
				Arg("name", name).
				Arg("value", value.String())
		}
	}
	if !changed {
		return nil
	}
	var llmID dagger.LLMID
	if err := syncedLLMQ.Select("id").Bind(&llmID).Execute(ctx); err != nil {
		return err
	}
	s.llm = s.dag.LoadLLMFromID(llmID)
	return nil
}

func (s *LLMSession) syncVarsFromLLM(ctx context.Context) error {
	vars, err := s.llm.Variables(ctx)
	if err != nil {
		return err
	}

	for _, v := range vars {
		name, err := v.Name(ctx)
		if err != nil {
			dbg.Println("error getting var name", err)
			return err
		}
		typeName, err := v.TypeName(ctx)
		if err != nil {
			dbg.Println("error getting var type name", err)
			return err
		}
		hash, err := v.Hash(ctx)
		if err != nil {
			dbg.Println("error getting var hash", err)
			return err
		}
		digest := digest.Digest(hash)

		if s.syncedVars[name] == digest {
			// already synced
			continue
		}

		dbg.Println("syncing llm => var", name, typeName, hash)

		val, err := s.ShellValue(ctx, name, typeName, func(q *querybuilder.Selection) *querybuilder.Selection {
			return q.Select(fmt.Sprintf("get%s", typeName)).
				Arg("name", name)
		})
		if err != nil {
			return err
		}
		quot, err := syntax.Quote(val, syntax.LangBash)
		if err != nil {
			return err
		}
		if err := s.shell.Eval(ctx, fmt.Sprintf("%s=%s", name, quot)); err != nil {
			return err
		}
		s.syncedVars[name] = digest
	}

	typeName, err := s.llm.CurrentType(ctx)
	if err != nil {
		return err
	}
	if typeName == "" || typeName == "Query" {
		return nil
	}

	val, err := s.ShellValue(ctx, "_", typeName, func(q *querybuilder.Selection) *querybuilder.Selection {
		return q.Select(strcase.ToLowerCamel(typeName))
	})
	if err != nil {
		return err
	}
	quot, err := syntax.Quote(val, syntax.LangBash)
	if err != nil {
		return err
	}
	if err := s.shell.Eval(ctx, fmt.Sprintf("%s=%s", "_", quot)); err != nil {
		return err
	}

	return nil
}

func (s *LLMSession) ShellValue(
	ctx context.Context,
	name, typeName string,
	getter func(*querybuilder.Selection) *querybuilder.Selection,
) (string, error) {
	switch typeName {
	case "String":
		return s.llm.GetString(ctx, name)
	default:
		var objID string
		if err := getter(
			s.dag.QueryBuilder().
				Select("loadLLMFromID").
				Arg("id", s.llm),
		).
			Select("id").
			Bind(&objID).
			Execute(ctx); err != nil {
			return "", err
		}
		dbg.Println("syncing object", name)
		st := ShellState{
			Calls: []FunctionCall{
				{
					Object: "Query",
					Name:   "load" + typeName + "FromID",
					Arguments: map[string]any{
						"id": objID,
					},
					ReturnObject: typeName,
				},
			},
		}
		key := s.shell.state.Store(st)
		return newStateToken(key), nil
	}
}

func (s *LLMSession) Clear() *LLMSession {
	s = s.Fork()
	s.reset()
	return s
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

func (s *LLMSession) Compact(ctx context.Context) (_ *LLMSession, rerr error) {
	ctx, span := Tracer().Start(ctx, "compact", telemetry.Internal(), telemetry.Encapsulate())
	defer telemetry.End(span, func() error { return rerr })
	summary, err := s.llm.WithPrompt(compact).LastReply(ctx)
	if err != nil {
		return s, err
	}
	fresh := s.dag.LLM(dagger.LLMOpts{
		Model: s.model,
	})
	compacted, err := fresh.WithPrompt(summary).Sync(ctx)
	if err != nil {
		return s, err
	}
	s = s.Fork()
	s.llm = compacted
	return s, nil
}

func (s *LLMSession) History(ctx context.Context) (*LLMSession, error) {
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
