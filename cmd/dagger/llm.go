package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/trace"
	"mvdan.cc/sh/v3/syntax"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/llmconfig"
	"github.com/dagger/dagger/core/openrouter"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
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

	plumbingCtx  context.Context
	plumbingSpan trace.Span

	autoCompact  bool
	autoCompactL *sync.Mutex

	onStep func(s *LLMSession) // called after every step in the prompt loop

	subscriptionLabelCache string // cached OAuth subscription label
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

		// Auto-save after every step so sessions are preserved even if
		// the process is interrupted mid-turn.
		if s.onStep != nil {
			s.onStep(s)
		}

		hasMore, err := prompted.HasPrompt(s.plumbingCtx)
		if err != nil {
			return s, err
		}
		if !hasMore {
			// Check if the user queued a message while the LLM was
			// running. If so, inject it as the next prompt and keep
			// iterating instead of returning to the shell.
			if queued := s.shell.DequeueMessage(); queued != "" {
				prompted = prompted.WithPrompt(queued)
			} else {
				break
			}
		}

		// Check if we need to compact in-between steps
		prompted, err = s.maybeAutoCompact(ctx)
		if err != nil {
			return s, fmt.Errorf("auto-compact: %w", err)
		}
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

// subscriptionLabel returns a display label for the OAuth subscription type
// of the currently active provider, or empty string if not using OAuth.
// Cached after first lookup.
func (s *LLMSession) subscriptionLabel() string {
	if s.subscriptionLabelCache != "" {
		return s.subscriptionLabelCache
	}
	cfg, err := llmconfig.Load()
	if err != nil || cfg == nil {
		return ""
	}
	// Only show subscription label for the default provider
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
	// Get current session token usage from API (for this thread only)
	sessionInputTokens, err := llm.TokenUsage().InputTokens(s.plumbingCtx)
	if err != nil {
		return err
	}
	sessionOutputTokens, err := llm.TokenUsage().OutputTokens(s.plumbingCtx)
	if err != nil {
		return err
	}
	sessionCacheReads, err := llm.TokenUsage().CachedTokenReads(s.plumbingCtx)
	if err != nil {
		return err
	}
	sessionCacheWrites, err := llm.TokenUsage().CachedTokenWrites(s.plumbingCtx)
	if err != nil {
		return err
	}

	// Get aggregated token metrics from DB (includes all spans/sub-agents)
	llmMetrics := s.frontend.GetLLMTokenMetrics()

	// Calculate total cost across all models
	var totalCost float64
	for model, metrics := range llmMetrics.ByModel {
		if m := s.models.Lookup(model); m != nil {
			inputCost := m.Pricing.Prompt.Cost(int(metrics.InputTokens))
			outputCost := m.Pricing.Completion.Cost(int(metrics.OutputTokens))
			cacheReadCost := m.Pricing.InputCacheRead.Cost(int(metrics.CachedTokenReads))
			cacheWriteCost := m.Pricing.InputCacheWrite.Cost(int(metrics.CachedTokenWrites))
			totalCost += inputCost + outputCost + cacheReadCost + cacheWriteCost
		}
	}

	data := idtui.StatusLineData{
		Model:             s.model,
		SubscriptionLabel: s.subscriptionLabel(),
		InputTokens:       sessionInputTokens,
		OutputTokens:      sessionOutputTokens,
		CacheReads:        sessionCacheReads,
		CacheWrites:       sessionCacheWrites,
		TotalCost:         totalCost,
		ContextPercent:    -1, // unknown by default
		AutoCompact:       s.ShouldAutocompact(),
	}

	if m := s.models.Lookup(s.model); m != nil {
		data.ContextWindow = int(m.ContextLength)
		if sessionInputTokens > 0 && m.ContextLength > 0 {
			data.ContextPercent = float64(sessionInputTokens) / float64(m.ContextLength) * 100
		}
	}

	s.frontend.SetStatusLine(data)

	return nil
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

func (s *LLMSession) Clear() *LLMSession {
	s = s.Fork()
	s.reset()
	return s
}

//go:embed llm_compact.md
var compactPrompt string

//go:embed llm_branch_summary.md
var branchSummaryPrompt string

// BranchSummary generates a summary of the current conversation branch.
// This is used when branching to describe what was explored in the branch
// being abandoned, so the summary can be injected at the branch target.
//
// Like pi, we serialize the conversation to text first (preventing the
// model from treating it as a conversation to continue), then pass it to
// a fresh lightweight LLM call with maxTokens=2048.
// If customInstructions is non-empty, it is appended to the default prompt.
func (s *LLMSession) BranchSummary(ctx context.Context, customInstructions string) (_ string, rerr error) {
	ctx, span := Tracer().Start(ctx, "branch summary", telemetry.Internal(), telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)

	// Serialize the conversation to plain text so the summarizer sees
	// it as data to summarize, not a conversation to continue.
	conversationText, err := s.llm.SerializeHistory(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to serialize history: %w", err)
	}

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
		WithMaxTokens(2048).
		WithPrompt(prompt).
		Loop(dagger.LLMLoopOpts{MaxAPICalls: 1}).
		LastReply(ctx)
	if err != nil {
		return "", err
	}
	return summaryText, nil
}

func (s *LLMSession) Compact(ctx context.Context) (_ *dagger.LLM, rerr error) {
	ctx, span := Tracer().Start(ctx, "compact", telemetry.Internal(), telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)

	compactedPrompt, err := s.llm.
		WithoutSystemPrompts().
		WithSystemPrompt("You are a helpful AI assistant tasked with summarizing conversations.").
		WithPrompt(compactPrompt).
		Loop().
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

// sessionMetadata stores metadata about a saved LLM session
type sessionMetadata struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	LLMID     string `json:"llm_id"`
	Branch    string `json:"branch,omitempty"`
}

// getSessionDir returns the directory where LLM sessions are stored, creating it if necessary
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
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create session directory: %w", err)
	}

	return sessionDir, nil
}

// AutoSaveSession saves the session automatically, named after the initial prompt,
// stored on disk under a UUIDv7 filename for anonymity and time-sorted ordering.
// If existingUUID is non-empty the same file is updated in-place; otherwise a
// new UUIDv7 is generated. Returns the UUID used.
func (s *LLMSession) AutoSaveSession(ctx context.Context, initialPrompt string, existingUUID string) (string, error) {
	if s.llm == nil {
		return existingUUID, nil // nothing to save
	}

	sessionDir, err := getSessionDir()
	if err != nil {
		return existingUUID, err
	}

	llmID, err := s.llm.ID(ctx)
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

	// Capture current git branch from the workspace
	branch, err := s.llm.Env().Workspace().Branch(ctx)
	if err != nil {
		slog.Debug("failed to get workspace branch", "error", err)
	}

	metadata := sessionMetadata{
		Name:      initialPrompt,
		Model:     s.model,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		LLMID:     string(llmID),
		Branch:    branch,
	}

	jsonData, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return sessionID, fmt.Errorf("failed to marshal session data: %w", err)
	}

	sessionFile := filepath.Join(sessionDir, sessionID+".json")
	if err := os.WriteFile(sessionFile, jsonData, 0644); err != nil {
		return sessionID, fmt.Errorf("failed to write session file: %w", err)
	}

	slog.Debug("auto-saved LLM session", "id", sessionID, "name", initialPrompt, "file", sessionFile)
	return sessionID, nil
}

// LoadSession loads an LLM session from disk by UUID
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

	loadedLLM := s.dag.LoadLLMFromID(dagger.LLMID(metadata.LLMID))

	// Replay the message history to emit telemetry spans so the TUI
	// shows the conversation in its scrollback.
	if _, err := loadedLLM.Replay(ctx); err != nil {
		slog.Warn("failed to replay session history", "error", err)
	}

	if err := s.updateLLMAndAgentVar(loadedLLM); err != nil {
		return err
	}
	return s.updateSidebar(loadedLLM)
}

// ListSessions returns saved sessions sorted by creation time (newest first, via UUIDv7 ordering)
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
		// Store the UUID (filename without extension) in the Name field isn't ideal,
		// so let's use a separate approach: we'll include the UUID in the list
		sessionID := strings.TrimSuffix(entry.Name(), ".json")
		sessions = append(sessions, sessionMetadata{
			Name:      meta.Name,
			Model:     meta.Model,
			CreatedAt: meta.CreatedAt,
			LLMID:     sessionID, // repurpose LLMID field to carry the file UUID for listing
			Branch:    meta.Branch,
		})
	}

	// Reverse so newest (highest UUIDv7) is first
	slices.Reverse(sessions)

	return sessions, nil
}
