package daggercmd

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/google/uuid"
	"github.com/muesli/termenv"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/trace"
	"mvdan.cc/sh/v3/syntax"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/openrouter"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/dagger/dagger/util/patchpreview"
	telemetry "github.com/dagger/otel-go"
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
	frontend idtui.Frontend

	// undo       *LLMSession
	dag        *dagger.Client
	llm        *dagger.LLM
	models     openrouter.Models
	model      string
	skipEnv    map[string]bool
	syncedVars map[string]digest.Digest
	shell      *shellCallHandler

	// onStep, if set, is invoked after every step of a prompt turn. It is used
	// to auto-save the session so it is preserved even if the process is
	// interrupted mid-turn.
	onStep func(*LLMSession)

	beforeFS     *dagger.Directory
	beforeFSTime time.Time
	afterFS      *dagger.Directory

	plumbingCtx  context.Context
	plumbingSpan trace.Span

	autoCompact  bool
	autoCompactL *sync.Mutex

	// subscriptionLabelCache caches the OAuth subscription label for the status
	// line, resolved lazily on first use.
	subscriptionLabelCache string
}

func NewLLMSession(
	ctx context.Context,
	dag *dagger.Client,
	llmModel string,
	shellHandler *shellCallHandler,
	frontend idtui.Frontend,
) (*LLMSession, error) {
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
		shell:        shellHandler,
		frontend:     frontend,
		autoCompact:  true,
		autoCompactL: new(sync.Mutex),
	}

	// Allocate a span to tuck all the internal plumbing into, so it doesn't
	// clutter the top-level prior to receiving the Revealed spans
	s.plumbingCtx, s.plumbingSpan = Tracer().Start(ctx, "LLM plumbing", telemetry.Internal())
	go func() {
		<-ctx.Done()
		s.plumbingSpan.End()
	}()

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

	// if $agent is set, respect it
	if value, ok := s.shell.runner.Vars[agentVar]; ok {
		if key := GetStateKey(value.String()); key != "" {
			st, err := s.shell.state.Load(key)
			if err != nil {
				return nil, err
			}
			// NB: don't need to use updateLLMAndAgentVar here, since this is coming
			// from the agent var
			s.llm = s.dag.LLM().WithGraphQLQuery(st.QueryBuilder(s.dag))
		}
	} else {
		s.reset()
	}

	// Grab the model to check for a valid config
	s.model, err = s.llm.Model(ctx)
	if err != nil {
		return nil, err
	}

	return s, nil
}

func (s *LLMSession) ShouldAutocompact() bool {
	s.autoCompactL.Lock()
	defer s.autoCompactL.Unlock()
	return s.autoCompact
}

func (s *LLMSession) ToggleAutocompact() {
	s.autoCompactL.Lock()
	s.autoCompact = !s.autoCompact
	s.autoCompactL.Unlock()
}

func (s *LLMSession) reset() {
	s.updateLLMAndAgentVar(
		s.dag.LLM(dagger.LLMOpts{Model: s.model}).
			WithEnv(s.dag.Env(dagger.EnvOpts{
				Privileged: true,
				Writable:   true,
			})))
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

	if err := s.syncVarsToLLM(); err != nil {
		return s, err
	}

	resolvedModel, err := s.llm.Model(s.plumbingCtx)
	if err != nil {
		return nil, err
	}
	s.model = resolvedModel

	// Check if we need to compact before adding the prompt
	compacted, err := s.maybeAutoCompact(ctx)
	if err != nil {
		return s, fmt.Errorf("auto-compact: %w", err)
	}

	prompted := compacted.WithPrompt(input)

	for {
		// update the sidebar after every step, not after the entire loop
		prompted, err = prompted.Step(ctx)
		if err != nil {
			return s, err
		}

		if err := s.updateLLMAndAgentVar(prompted); err != nil {
			return s, err
		}

		if err := s.updateSidebar(prompted); err != nil {
			return s, err
		}

		// Auto-save after every step so sessions are preserved even if the
		// process is interrupted mid-turn.
		if s.onStep != nil {
			s.onStep(s)
		}

		hasMore, err := prompted.HasPrompt(s.plumbingCtx)
		if err != nil {
			return s, err
		}
		var queued string
		if !hasMore {
			// Check if the user queued a message while the LLM was running. If
			// nothing is queued and no prompt is pending, the turn is complete.
			queued = s.shell.DequeueMessage()
			if queued == "" {
				break
			}
		}

		// Check if we need to compact in-between steps
		prompted, err = s.maybeAutoCompact(ctx)
		if err != nil {
			return s, fmt.Errorf("auto-compact: %w", err)
		}

		// Inject any queued message as the next prompt. This must happen after
		// maybeAutoCompact, which returns the session's LLM rather than
		// prompted, and would otherwise discard the injected prompt.
		if queued != "" {
			prompted = prompted.WithPrompt(queued)
		}
	}

	if err := s.syncVarsFromLLM(); err != nil {
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

func (s *LLMSession) updateLLMAndAgentVar(llm *dagger.LLM) error {
	s.llm = llm

	ctx := s.plumbingCtx

	// figure out what the model resolved to
	model, err := s.llm.Model(ctx)
	if err != nil {
		return err
	}
	s.model = model

	if err := s.assignShell(ctx, agentVar, s.llm); err != nil {
		return err
	}
	return nil
}

// subscriptionLabel returns a display label for the OAuth subscription type of
// the currently active default provider, or empty string if not using OAuth.
// Cached after first lookup.
func (s *LLMSession) subscriptionLabel() string {
	if s.subscriptionLabelCache != "" {
		return s.subscriptionLabelCache
	}
	cfg, err := llmconfig.Load()
	if err != nil || cfg == nil {
		return ""
	}
	defaultProvider := cfg.LLM.DefaultProvider
	if defaultProvider == "" {
		return ""
	}
	provider, ok := cfg.LLM.Providers[defaultProvider]
	if !ok || !provider.IsOAuth() || provider.SubscriptionType == "" {
		return ""
	}
	s.subscriptionLabelCache = llmconfig.SubscriptionLabel(provider.SubscriptionType)
	return s.subscriptionLabelCache
}

func (s *LLMSession) updateSidebar(llm *dagger.LLM) error {
	inputTokens, err := llm.TokenUsage().InputTokens(s.plumbingCtx)
	if err != nil {
		return err
	}
	outputTokens, err := llm.TokenUsage().OutputTokens(s.plumbingCtx)
	if err != nil {
		return err
	}
	cacheReads, err := llm.TokenUsage().CachedTokenReads(s.plumbingCtx)
	if err != nil {
		return err
	}
	cacheWrites, err := llm.TokenUsage().CachedTokenWrites(s.plumbingCtx)
	if err != nil {
		return err
	}
	lines := []string{
		termenv.String(s.model).Foreground(termenv.ANSIMagenta).Bold().String(),
	}

	if opts.Verbosity > dagui.ShowInternalVerbosity {
		if inputTokens > 0 {
			lines = append(lines,
				fmt.Sprintf("%s "+termenv.String("%d").Bold().String(),
					"Tokens In: ",
					inputTokens))
		}
		if outputTokens > 0 {
			lines = append(lines,
				fmt.Sprintf("%s "+termenv.String("%d").Bold().String(),
					"Tokens Out:",
					outputTokens))
		}
		if cacheReads > 0 {
			lines = append(lines,
				fmt.Sprintf("%s "+termenv.String("%d").Bold().String(),
					"Cache Reads: ",
					cacheReads))
		}
		if cacheWrites > 0 {
			lines = append(lines,
				fmt.Sprintf("%s "+termenv.String("%d").Bold().String(),
					"Cache Writes:",
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
				fmt.Sprintf("Cost: "+termenv.String("$%0.2f").Bold().String(),
					totalCost),
				fmt.Sprintf("Context: "+contextStyle.String(),
					contextUsage),
			)
		}
	}

	s.frontend.SetSidebarContent(idtui.SidebarSection{
		Title:   "LLM",
		Content: strings.Join(lines, "\n"),
	})

	// Drive the compact status line: session token counts for the display and
	// context %, aggregated cost across all models/sub-agents from the DB.
	statusData := idtui.StatusLineData{
		Model:             s.model,
		SubscriptionLabel: s.subscriptionLabel(),
		InputTokens:       inputTokens,
		OutputTokens:      outputTokens,
		CacheReads:        cacheReads,
		CacheWrites:       cacheWrites,
		ContextPercent:    -1, // unknown by default
		AutoCompact:       s.ShouldAutocompact(),
	}
	if llmMetrics := s.frontend.GetLLMTokenMetrics(); llmMetrics != nil {
		for _, metrics := range llmMetrics.Snapshot() {
			m := s.models.Lookup(metrics.Model)
			if m == nil {
				continue
			}
			statusData.TotalCost += m.Pricing.Prompt.Cost(int(metrics.InputTokens))
			statusData.TotalCost += m.Pricing.Completion.Cost(int(metrics.OutputTokens))
			statusData.TotalCost += m.Pricing.InputCacheRead.Cost(int(metrics.CachedTokenReads))
			statusData.TotalCost += m.Pricing.InputCacheWrite.Cost(int(metrics.CachedTokenWrites))
		}
	}
	if m := s.models.Lookup(s.model); m != nil {
		statusData.ContextWindow = int(m.ContextLength)
		if inputTokens > 0 && m.ContextLength > 0 {
			statusData.ContextPercent = float64(inputTokens) / float64(m.ContextLength) * 100
		}
	}
	s.frontend.SetStatusLine(statusData)

	s.afterFS = llm.Env().Workspace()

	dirDiff := s.afterFS.Changes(s.beforeFS)

	entries, err := idtui.PreviewPatch(s.plumbingCtx, s.dag, dirDiff)
	if err != nil {
		return err
	}

	if len(entries) > 0 {
		s.frontend.SetSidebarContent(idtui.SidebarSection{
			Title: "Changes",
			ContentFunc: func(width int) string {
				var buf strings.Builder
				patchpreview.Summarize(idtui.NewOutput(&buf), entries, width)
				return buf.String()
			},
			KeyMap: []key.Binding{
				key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "save")),
			},
		})
	}

	return err
}

// maybeAutoCompact checks if the context usage exceeds 80% and automatically compacts if so
func (s *LLMSession) maybeAutoCompact(ctx context.Context) (_ *dagger.LLM, rerr error) {
	if !s.ShouldAutocompact() {
		return s.llm, nil
	}

	// Get current token usage
	inputTokens, err := s.llm.TokenUsage().InputTokens(s.plumbingCtx)
	if err != nil {
		return nil, err
	}

	// Check if we know the model's context length
	m := s.models.Lookup(s.model)
	if m == nil {
		// Can't determine context length, skip auto-compact
		return s.llm, nil
	}

	// Calculate context usage percentage
	contextUsage := float64(inputTokens) / float64(m.ContextLength)

	// If we're over 80% context usage, automatically compact
	if contextUsage > 0.80 {
		ctx, span := Tracer().Start(ctx, "auto-compacting LLM history", telemetry.Reveal())
		defer telemetry.EndWithCause(span, &rerr)
		return s.Compact(ctx)
	}

	return s.llm, nil
}

func (s *LLMSession) syncVarsToLLM() error {
	ctx := s.plumbingCtx

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
			s.llm = s.dag.LLM().WithGraphQLQuery(st.QueryBuilder(s.dag))
		}
	}

	if s.beforeFS == nil {
		s.beforeFS = s.llm.Env().Workspace()
		s.beforeFSTime = time.Now()
	}

	syncedEnvQ := s.dag.QueryBuilder().
		Select("node").
		Arg("id", s.llm.Env()).
		InlineFragment("Env")

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

		if s.syncedVars[name] == hashutil.HashStrings(value.String()) {
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
			s.syncedVars[name] = hashutil.HashStrings(value.String())
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
	var envID dagger.ID
	if err := syncedEnvQ.Select("id").Bind(&envID).Execute(ctx); err != nil {
		return err
	}
	s.updateLLMAndAgentVar(s.llm.WithEnv(dagger.Ref[*dagger.Env](s.dag, envID)))
	return nil
}

func (s *LLMSession) syncVarsFromLLM() error {
	ctx := s.plumbingCtx

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
					Select("node").
					Arg("id", bnd).
					InlineFragment("Binding").
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

func (do *dynamicObject) XXX_GraphQLType() string { //nolint:staticcheck
	return do.typeName
}

func (do *dynamicObject) XXX_GraphQLID(ctx context.Context) (string, error) { //nolint:staticcheck
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
				Object:         "Query",
				Name:           "node",
				InlineFragment: typeName,
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

func (s *LLMSession) Clear() *LLMSession {
	s = s.Fork()
	s.reset()
	return s
}

//go:embed llm_compact.md
var compactPrompt string

func (s *LLMSession) Compact(ctx context.Context) (_ *dagger.LLM, rerr error) {
	ctx, span := Tracer().Start(ctx, "compact", telemetry.Internal(), telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)

	compactedPrompt, err := s.llm.
		WithoutSystemPrompts().
		WithSystemPrompt("You are a helpful AI assistant tasked with summarizing conversations.").
		WithPrompt(compactPrompt).
		LastReply(ctx)
	if err != nil {
		return nil, err
	}

	return s.llm.
		WithoutMessageHistory().
		WithPrompt(fmt.Sprintf(
			"This session is being continued from a previous conversation that ran out of context. The conversation is summarized below:\n\n%s",
			compactedPrompt,
		)), nil
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

func (s *LLMSession) Model(model string) (*LLMSession, error) {
	s = s.Fork()
	s.updateLLMAndAgentVar(s.llm.WithModel(model))
	model, err := s.llm.Model(s.plumbingCtx)
	if err != nil {
		return nil, err
	}
	s.model = model
	return s, nil
}

//go:embed llm_branch_summary.md
var branchSummaryPrompt string

// Summarization input budget: assume a conservative context window when the
// model's real one is unknown, and reserve room for the prompt scaffolding
// and the model's output, estimating ~4 chars per token.
const (
	summaryContextWindowTokens = 128000
	summaryReserveTokens       = 16384
	summaryCharsPerToken       = 4
)

// trimConversationForSummary drops the oldest serialized messages so the
// conversation fits the summarization input budget, keeping the newest
// content. SerializeHistory joins messages with blank lines, so trimming
// happens at those boundaries; a notice marks the omission. Without this, a
// near-window-sized history would leave the summarization request little or
// no room to respond.
func trimConversationForSummary(text string) string {
	budgetChars := (summaryContextWindowTokens - summaryReserveTokens) * summaryCharsPerToken
	if len(text) <= budgetChars {
		return text
	}
	const notice = "[Earlier conversation omitted to fit the context window.]"
	parts := strings.Split(text, "\n\n")
	var kept []string
	total := 0
	for i := len(parts) - 1; i >= 0; i-- {
		total += len(parts[i]) + 2
		if total > budgetChars {
			break
		}
		kept = append(kept, parts[i])
	}
	if len(kept) == 0 {
		// A single oversized message (e.g. a huge tool result); keep its tail.
		return notice + "\n\n" + text[len(text)-budgetChars:]
	}
	slices.Reverse(kept)
	return notice + "\n\n" + strings.Join(kept, "\n\n")
}

// BranchSummary generates a summary of the current conversation branch. It is
// used when branching to describe what was explored in the branch being
// abandoned, so the summary can be injected at the branch target.
//
// The conversation is serialized to plain text first (so the model treats it
// as data to summarize, not a conversation to continue), then passed to a
// fresh lightweight LLM call with a small output budget. If customInstructions
// is non-empty it is appended to the default prompt.
func (s *LLMSession) BranchSummary(ctx context.Context, customInstructions string) (_ string, rerr error) {
	ctx, span := Tracer().Start(ctx, "branch summary", telemetry.Internal(), telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)

	conversationText, err := s.llm.SerializeHistory(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to serialize history: %w", err)
	}
	conversationText = trimConversationForSummary(conversationText)

	instructions := branchSummaryPrompt
	if customInstructions != "" {
		instructions += "\n\nAdditional focus: " + customInstructions
	}

	prompt := fmt.Sprintf("<conversation>\n%s\n</conversation>\n\n%s", conversationText, instructions)

	// Use a fresh LLM (no tools, no history) with a small output budget.
	summaryText, err := s.llm.
		WithoutMessageHistory().
		WithoutSystemPrompts().
		WithSystemPrompt("You are a context summarization assistant. Your task is to read a conversation between a user and an AI coding assistant, then produce a structured summary following the exact format specified. Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.").
		WithPrompt(prompt).
		Loop(dagger.LLMLoopOpts{MaxAPICalls: 1, MaxTokens: 2048}).
		LastReply(ctx)
	if err != nil {
		return "", err
	}
	return summaryText, nil
}

// sessionMetadata stores metadata about a saved LLM session.
type sessionMetadata struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	LLMID     string `json:"llm_id"`
	Branch    string `json:"branch,omitempty"`
}

// getSessionDir returns the directory where LLM sessions are stored, creating
// it if necessary.
func getSessionDir() (string, error) {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		stateHome = filepath.Join(homeDir, ".local", "state")
	}

	sessionDir := filepath.Join(stateHome, "dagger", "llm-sessions")
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create session directory: %w", err)
	}
	// Sessions contain prompts and history-bearing LLM IDs; keep the
	// directory private even if an older version created it more openly.
	if err := os.Chmod(sessionDir, 0700); err != nil {
		return "", fmt.Errorf("failed to restrict session directory permissions: %w", err)
	}

	return sessionDir, nil
}

// AutoSaveSession saves the session automatically, named after the initial
// prompt, stored on disk under a UUIDv7 filename for anonymity and time-sorted
// ordering. If existingUUID is non-empty the same file is updated in-place;
// otherwise a new UUIDv7 is generated. Returns the UUID used.
func (s *LLMSession) AutoSaveSession(ctx context.Context, initialPrompt string, existingUUID string) (string, error) {
	if s.llm == nil {
		return existingUUID, nil // nothing to save
	}

	sessionDir, err := getSessionDir()
	if err != nil {
		return existingUUID, err
	}

	// Persist the portable, self-contained (recipe-form) ID rather than the
	// default runtime handle, which is an engine-local reference that cannot be
	// resolved once this session's engine is gone.
	llmID, err := s.llm.GlobalID(ctx)
	if err != nil {
		return existingUUID, fmt.Errorf("failed to get LLM ID: %w", err)
	}

	sessionID := existingUUID
	if sessionID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return "", fmt.Errorf("failed to generate session UUID: %w", err)
		}
		sessionID = id.String()
	}

	metadata := sessionMetadata{
		Name:      initialPrompt,
		Model:     s.model,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		LLMID:     string(llmID),
	}

	jsonData, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return sessionID, fmt.Errorf("failed to marshal session data: %w", err)
	}

	sessionFile := filepath.Join(sessionDir, sessionID+".json")
	if err := os.WriteFile(sessionFile, jsonData, 0600); err != nil {
		return sessionID, fmt.Errorf("failed to write session file: %w", err)
	}
	// WriteFile only applies the mode on creation; fix up files written more
	// openly by an older version.
	if err := os.Chmod(sessionFile, 0600); err != nil {
		return sessionID, fmt.Errorf("failed to restrict session file permissions: %w", err)
	}

	slog.Debug("auto-saved LLM session", "id", sessionID, "name", initialPrompt, "file", sessionFile)
	return sessionID, nil
}

// LoadSession loads an LLM session from disk by UUID.
func (s *LLMSession) LoadSession(ctx context.Context, sessionID string) error {
	sessionDir, err := getSessionDir()
	if err != nil {
		return err
	}

	sessionFile := filepath.Join(sessionDir, sessionID+".json")
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("session %q not found", sessionID)
		}
		return fmt.Errorf("failed to read session file: %w", err)
	}

	var metadata sessionMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return fmt.Errorf("failed to unmarshal session data: %w", err)
	}

	if metadata.LLMID == "" {
		return fmt.Errorf("invalid session data: missing LLM ID")
	}

	loadedLLM := dagger.Ref[*dagger.LLM](s.dag, dagger.ID(metadata.LLMID))

	// Replay the message history to emit telemetry spans so the TUI shows the
	// conversation in its scrollback.
	if _, err := loadedLLM.Replay(ctx); err != nil {
		slog.Warn("failed to replay session history", "error", err)
	}

	if err := s.updateLLMAndAgentVar(loadedLLM); err != nil {
		return err
	}
	return s.updateSidebar(loadedLLM)
}

// ListSessions returns saved sessions sorted by creation time (newest first,
// via UUIDv7 ordering). The returned metadata's LLMID field carries the file
// UUID (for loading), not the full LLM ID.
func ListSessions() ([]sessionMetadata, error) {
	sessionDir, err := getSessionDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read session directory: %w", err)
	}

	var sessions []sessionMetadata
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sessionDir, entry.Name()))
		if err != nil {
			continue
		}
		var meta sessionMetadata
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		sessionID := strings.TrimSuffix(entry.Name(), ".json")
		sessions = append(sessions, sessionMetadata{
			Name:      meta.Name,
			Model:     meta.Model,
			CreatedAt: meta.CreatedAt,
			LLMID:     sessionID, // repurpose LLMID to carry the file UUID for listing
			Branch:    meta.Branch,
		})
	}

	// Reverse so newest (highest UUIDv7) is first.
	slices.Reverse(sessions)

	return sessions, nil
}

func (s *LLMSession) SyncFromLocal(ctx context.Context) (rerr error) {
	if s.llm == nil {
		return fmt.Errorf("no LLM session active")
	}

	if s.beforeFSTime.IsZero() {
		return nil
	}

	ctx, span := Tracer().Start(ctx, "syncing local changes",
		telemetry.Reveal())
	defer telemetry.EndWithCause(span, &rerr)
	slog := slog.SpanLogger(ctx, InstrumentationLibrary)
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)

	var pathsToUpload []string

	// Look for paths modified since the last sync, and only upload those.
	if err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// ignore errors
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				// don't recurse into .git
				return filepath.SkipDir
			}
			// nothing to do for directories
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(s.beforeFSTime) {
			slog.Info(
				"file changed since last sync",
				"path", path,
				"syncTime", s.beforeFSTime,
				"mtime", info.ModTime(),
			)
			pathsToUpload = append(pathsToUpload, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("walk looking for modifications: %w", err)
	}

	// The model's workspace has its own changes since last sync, so if we're
	// syncing from local, we need to revert them
	if s.afterFS != nil && s.beforeFS != nil {
		changes := s.afterFS.Changes(s.beforeFS)
		modified, err := changes.ModifiedPaths(ctx)
		if err != nil {
			return err
		}
		removed, err := changes.RemovedPaths(ctx)
		if err != nil {
			return err
		}
		if len(modified) > 0 {
			slog.Info("reverting LLM-modified files", "modified", modified)
			pathsToUpload = append(pathsToUpload, modified...)
		}
		if len(removed) > 0 {
			slog.Info("restoring LLM-removed files", "removed", removed)
			pathsToUpload = append(pathsToUpload, removed...)
		}
	}

	if len(pathsToUpload) == 0 {
		slog.Warn("no changes detected")
		return nil
	}

	slog.Info("syncing changed files", "paths", pathsToUpload)

	localChanges, err := s.dag.Host().Directory(".", dagger.HostDirectoryOpts{
		Include:   pathsToUpload,
		NoCache:   true,
		Gitignore: true,
	}).Sync(ctx)
	if err != nil {
		return nil
	}

	currentFS := s.afterFS
	if currentFS == nil {
		currentFS = s.beforeFS
	}

	withChanges := currentFS.WithDirectory(".", localChanges)

	newLLM := s.llm.WithEnv(
		s.llm.Env().WithWorkspace(withChanges),
	)

	dirDiff := withChanges.Changes(currentFS)

	// Add an LLM prompt as a cue to the model so it knows what files changed.
	entries, err := idtui.PreviewPatch(s.plumbingCtx, s.dag, dirDiff)
	if err != nil {
		return err
	}

	if len(entries) > 0 {
		const summaryWidth = 80

		newLLM = newLLM.WithPrompt(
			fmt.Sprintf("I have made the following changes:\n\n```\n%s\n```",
				patchpreview.SummarizeString(entries, summaryWidth)),
		)

		// Show colorized summary to user.
		patchpreview.Summarize(idtui.NewOutput(stdio.Stdout), entries, summaryWidth)
	}

	s.updateLLMAndAgentVar(newLLM)

	// reset before/after state
	s.beforeFS = withChanges
	s.beforeFSTime = time.Now()
	s.afterFS = nil

	// Update sidebar to show sync success
	s.frontend.SetSidebarContent(idtui.SidebarSection{
		Title:   "Changes",
		Content: "", // empty content will hide it
	})

	return nil
}

func (s *LLMSession) SyncToLocal(ctx context.Context) error {
	if s.llm == nil {
		return fmt.Errorf("no LLM session active")
	}

	if s.afterFS == nil {
		return fmt.Errorf("nothing to sync")
	}

	if _, err := s.afterFS.Changes(s.beforeFS).Export(ctx, "."); err != nil {
		return err
	}

	s.beforeFS = s.afterFS
	s.beforeFSTime = time.Now()

	// Update sidebar to show sync success
	s.frontend.SetSidebarContent(idtui.SidebarSection{
		Title:   "Changes",
		Content: "", // empty content will hide it
	})

	return nil
}
