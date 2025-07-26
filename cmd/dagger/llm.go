package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"log/slog"
	"maps"
	"os"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/core/openrouter"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/muesli/termenv"
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
	// undo       *LLMSession
	dag        *dagger.Client
	llm        *dagger.LLM
	models     openrouter.Models
	model      string
	skipEnv    map[string]bool
	syncedVars map[string]digest.Digest
	shell      *shellCallHandler
	initialFS  *dagger.Directory
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

	// TODO: cache this
	models, err := openrouter.FetchModels(ctx)
	if err != nil {
		return nil, err
	}
	s.models = models

	// don't sync the initial env vars
	for k := range shellHandler.runner.Env.Each {
		s.skipEnv[k] = true
	}

	s.reset(ctx)

	return s, nil
}

func (s *LLMSession) reset(ctx context.Context) {
	s.updateLLMAndAgentVar(ctx, s.dag.LLM(dagger.LLMOpts{Model: s.model}).
		WithEnv(s.dag.Env(dagger.EnvOpts{
			Privileged: true,
			Writable:   true,
		})).
		WithSystemPrompt(`You are an interactive coding assistant.`).
		WithSystemPrompt(`When the user's query contains a variable like $foo, determine if the request is asking you to save a value. If so, declare the output binding.`).
		WithSystemPrompt(`Use the Directory.search and File.search methods to grep for text in files.`).
		WithSystemPrompt(`Use the File.withReplaced method for efficient text editing, rather than rewriting entire files.`))
}

func (s *LLMSession) Fork() *LLMSession {
	// FIXME: this was a half-baked feature, currently does more harm than good
	// because we lose partial progress on interrupt
	//
	// see https://github.com/dagger/dagger/pull/10765
	return s
	// cp := *s
	// cp.undo = s
	// return &cp
}

func (s *LLMSession) WithPrompt(ctx context.Context, input string) (*LLMSession, error) {
	s = s.Fork()

	if err := s.syncVarsToLLM(ctx); err != nil {
		return s, err
	}

	resolvedModel, err := s.llm.Model(ctx)
	if err != nil {
		return nil, err
	}
	s.model = resolvedModel

	prompted := s.llm.WithPrompt(input)

	for {
		prompted = prompted.Step()
		s.updateLLMAndAgentVar(ctx, prompted)

		prompted, err := s.llm.Sync(ctx)
		if err != nil {
			return s, err
		}
		// NB: this is currently redundant since Sync updates LLM state in-place, but
		// safest option is to respect the return value anyway in case it changes
		s.updateLLMAndAgentVar(ctx, prompted)

		after := prompted.Env().Hostfs()
		changed, err := s.initialFS.Diff(after).Glob(ctx, "**/*")
		if err != nil {
			return s, err
		}

		if len(changed) > 0 {
			diff := ""
			for _, fp := range changed {
				if strings.HasSuffix(fp, "/") {
					continue
				}
				diff += fmt.Sprintf("- %s\n", termenv.String(fp).Bold())
			}
			Frontend.SetSidebarContent(idtui.SidebarSection{
				Title:   "Changes",
				Content: diff,
			})
		}

		hasMore, err := prompted.HasPrompt(ctx)
		if err != nil {
			return s, err
		}
		if !hasMore {
			break
		}
	}

	if err := s.syncVarsFromLLM(ctx); err != nil {
		return s, err
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

const (
	agentVar     = "agent"
	lastValueVar = "_"
)

func (s *LLMSession) updateLLMAndAgentVar(ctx context.Context, llm *dagger.LLM) error {
	s.llm = llm

	// figure out what the model resolved to
	model, err := s.llm.Model(ctx)
	if err != nil {
		return err
	}
	s.model = model

	inputTokens, err := llm.TokenUsage().InputTokens(ctx)
	if err != nil {
		return err
	}
	outputTokens, err := llm.TokenUsage().OutputTokens(ctx)
	if err != nil {
		return err
	}
	cacheReads, err := llm.TokenUsage().CachedTokenReads(ctx)
	if err != nil {
		return err
	}
	cacheWrites, err := llm.TokenUsage().CachedTokenWrites(ctx)
	if err != nil {
		return err
	}
	lines := []string{
		idtui.DotFilled + " " + termenv.String(s.model).Bold().String(),
	}

	if opts.Verbosity > dagui.ShowInternalVerbosity {
		if inputTokens > 0 {
			lines = append(lines,
				fmt.Sprintf("%s "+termenv.String("%d").Bold().String(),
					"  Tokens In: ",
					inputTokens))
		}
		if outputTokens > 0 {
			lines = append(lines,
				fmt.Sprintf("%s "+termenv.String("%d").Bold().String(),
					"  Tokens Out:",
					outputTokens))
		}
		if cacheReads > 0 {
			lines = append(lines,
				fmt.Sprintf("%s "+termenv.String("%d").Bold().String(),
					"  Cache Reads: ",
					cacheReads))
		}
		if cacheWrites > 0 {
			lines = append(lines,
				fmt.Sprintf("%s "+termenv.String("%d").Bold().String(),
					"  Cache Writes:",
					cacheWrites))
		}
	}

	if m := s.models.Lookup(s.model); m != nil {
		inputCost := m.Pricing.Prompt.Cost(inputTokens)
		outputCost := m.Pricing.Completion.Cost(outputTokens)
		cacheReadCost := m.Pricing.InputCacheRead.Cost(cacheReads)
		cacheWriteCost := m.Pricing.InputCacheWrite.Cost(cacheWrites)
		totalCost := inputCost + outputCost + cacheReadCost + cacheWriteCost
		if totalCost > 0 {
			contextUsage := int(float64(inputTokens) / float64(m.ContextLength) * 100)
			contextStyle := termenv.String("%d%%").Bold()
			if contextUsage > 80 {
				contextStyle = contextStyle.Foreground(termenv.ANSIYellow)
			}
			if contextUsage > 90 {
				contextStyle = contextStyle.Foreground(termenv.ANSIRed)
			}
			if contextUsage > 100 {
				contextStyle = contextStyle.Foreground(termenv.ANSIBrightRed)
			}
			lines = append(lines,
				fmt.Sprintf("  Cost: "+termenv.String("$%0.2f").Bold().String(),
					totalCost),
				fmt.Sprintf("  Context: "+contextStyle.String(),
					contextUsage),
			)
		}
	}

	Frontend.SetSidebarContent(idtui.SidebarSection{
		Title:   "LLM",
		Content: strings.Join(lines, "\n"),
	})
	if err := s.assignShell(ctx, agentVar, s.llm); err != nil {
		return err
	}
	return nil
}

func (s *LLMSession) syncVarsToLLM(ctx context.Context) error {
	// TODO: overlay? bad scaling characteristics. maybe overkill anyway
	oldVars := s.syncedVars
	s.syncedVars = make(map[string]digest.Digest)
	maps.Copy(s.syncedVars, oldVars)

	if value, ok := s.shell.runner.Vars[agentVar]; ok {
		if key := GetStateKey(value.String()); key != "" {
			st, err := s.shell.state.Load(key)
			if err != nil {
				return err
			}
			// NB: don't need to use updateLLMAndAgentVar here, since this is coming
			// from the agent var
			s.llm = s.llm.WithGraphQLQuery(st.QueryBuilder(s.dag))

			// sync the initial FS from the agent
			s.initialFS, err = s.llm.Env().Hostfs().Sync(ctx)
			if err != nil {
				return err
			}
		}
	}

	syncedEnvQ := s.dag.QueryBuilder().
		Select("loadEnvFromID").
		Arg("id", s.llm.Env())

	var changed bool
	for name, value := range s.shell.runner.Vars {
		if name == agentVar {
			// handled separately
			continue
		}
		if name == lastValueVar {
			// don't sync the auto-last-value var back to the LLM
			continue
		}
		if s.skipEnv[name] {
			continue
		}

		if s.syncedVars[name] == dagql.HashFrom(value.String()) {
			continue
		}

		dbg.Printf("syncing var %q => llm env\n", name)

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
				syncedEnvQ = syncedEnvQ.
					Select(fmt.Sprintf("with%sInput", typeName)).
					Arg("name", name).
					Arg("description", ""). // TODO
					Arg("value", id)
				s.syncedVars[name] = digest
			}
		} else {
			s.syncedVars[name] = dagql.HashFrom(value.String())
			syncedEnvQ = syncedEnvQ.
				Select("withStringInput").
				Arg("name", name).
				Arg("description", ""). // TODO
				Arg("value", value.String())
		}
	}
	if !changed {
		return nil
	}
	var envID dagger.EnvID
	if err := syncedEnvQ.Select("id").Bind(&envID).Execute(ctx); err != nil {
		return err
	}
	s.updateLLMAndAgentVar(ctx, s.llm.WithEnv(s.dag.LoadEnvFromID(envID)))
	return nil
}

func (s *LLMSession) syncVarsFromLLM(ctx context.Context) error {
	outputs, err := s.llm.Env().Outputs(ctx)
	if err != nil {
		return err
	}

	assign := func(bnd *dagger.Binding) error {
		name, err := bnd.Name(ctx)
		if err != nil {
			return err
		}
		typeName, err := bnd.TypeName(ctx)
		if err != nil {
			return err
		}
		isNull, err := bnd.IsNull(ctx)
		if err != nil {
			return err
		}
		if isNull {
			return nil
		}
		switch typeName {
		case "", "Query", "Void":
			return nil
		case "String":
			str, err := bnd.AsString(ctx)
			if err != nil {
				return err
			}
			s.assignShellString(ctx, name, str)
			return nil
		default:
			var objID string
			if err :=
				s.dag.QueryBuilder().
					Select("loadBindingFromID").
					Arg("id", bnd).
					Select("as" + typeName).
					Select("id").
					Bind(&objID).
					Execute(ctx); err != nil {
				return err
			}
			return s.assignShell(ctx, name, &dynamicObject{objID, typeName})
		}
	}

	// assign all outputs
	for _, output := range outputs {
		if err := assign(&output); err != nil {
			return err
		}
	}

	// assign last value
	return assign(s.llm.BindResult(lastValueVar))
}

type dagqlObject interface {
	XXX_GraphQLType() string
	XXX_GraphQLID(context.Context) (string, error)
}

type dynamicObject struct {
	id       string
	typeName string
}

func (do *dynamicObject) XXX_GraphQLType() string { //nolint:stylecheck
	return do.typeName
}

func (do *dynamicObject) XXX_GraphQLID(ctx context.Context) (string, error) { //nolint:stylecheck
	return do.id, nil
}

func (s *LLMSession) assignShell(ctx context.Context, name string, idable dagqlObject) error {
	val, err := s.toShell(ctx, idable)
	if err != nil {
		return err
	}
	s.assignShellString(ctx, name, val)
	return nil
}

func (s *LLMSession) assignShellString(ctx context.Context, name string, val string) {
	if len(val) > 100 {
		slog.Debug("value is too long", "name", name, "value", val)
		return
	}
	quot, err := syntax.Quote(val, syntax.LangBash)
	if err != nil {
		slog.Error("failed to quote value", "name", name, "value", val, "error", err.Error())
		return
	}
	if err := s.shell.Eval(ctx, fmt.Sprintf("%s=%s", name, quot)); err != nil {
		slog.Error("failed to assign value", "name", name, "quoted", quot, "error", err.Error())
	}
	return
}

func (s *LLMSession) toShell(ctx context.Context, idable dagqlObject) (string, error) {
	typeName := idable.XXX_GraphQLType()
	objID, err := idable.XXX_GraphQLID(ctx)
	if err != nil {
		return "", err
	}
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

func (s *LLMSession) Clear(ctx context.Context) *LLMSession {
	s = s.Fork()
	s.reset(ctx)
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
	s.updateLLMAndAgentVar(ctx, compacted)
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
	s.updateLLMAndAgentVar(ctx, s.llm.WithModel(model))
	model, err := s.llm.Model(ctx)
	if err != nil {
		return nil, err
	}
	s.model = model
	return s, nil
}
