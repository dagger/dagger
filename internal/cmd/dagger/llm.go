package daggercmd

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/modelcatalog"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
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
	dag   *dagger.Client
	llm   *dagger.LLM
	model string
	shell *shellCallHandler

	// onStep, if set, is invoked after every step of a prompt turn. It is used
	// to auto-save the session so it is preserved even if the process is
	// interrupted mid-turn.
	onStep func(*LLMSession)

	plumbingCtx  context.Context
	plumbingSpan trace.Span

	autoCompact  bool
	autoCompactL *sync.Mutex

	// initialLLM is the base LLM to reset to on .clear, e.g. the workspace's
	// composed agent group as selected on startup (`dagger agent`). When nil,
	// .clear resets to a plain workspace-bound LLM.
	initialLLM *dagger.LLM

	// subscriptionLabelCache caches the OAuth subscription label for the status
	// line, resolved lazily on first use.
	subscriptionLabelCache string

	// prevContextTokens is the cumulative prompt-token total (input + cache
	// reads + cache writes) observed after the previous step, and prevStepContext
	// is that step's own prompt size. Together they drive the per-step context
	// growth shown in --debug mode (see reportContextUsage).
	prevContextTokens int
	prevStepContext   int

	// references tracks the host paths the user has attached with @ this
	// session (see attachReferences). They are mounted read-only in the LLM's
	// workspace, shown in the "References" sidebar, and dropped on .clear.
	references []referenceInfo
}

func NewLLMSession(
	ctx context.Context,
	dag *dagger.Client,
	llmModel string,
	shellHandler *shellCallHandler,
	frontend idtui.Frontend,
) (*LLMSession, error) {
	s := &LLMSession{
		dag:          dag,
		model:        llmModel,
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

	// Register a pricing function so the frontend can cost the live metric
	// rollup (all models + sub-agents) at render time, keeping the status line
	// current between turns instead of the per-step snapshot. Pricing comes
	// from the embedded catwalk catalog (modelcatalog), the single source of
	// truth shared with the engine.
	if sink, ok := frontend.(interface {
		SetLLMCostFunc(idtui.LLMCostFunc)
	}); ok {
		sink.SetLLMCostFunc(func(provider, model string, input, output, cacheReads, cacheWrites int64) float64 {
			return modelcatalog.Cost(provider, model, input, output, cacheReads, cacheWrites)
		})
	}

	s.reset()

	// Grab the model to check for a valid config
	model, err := s.llm.Model(ctx)
	if err != nil {
		return nil, err
	}
	s.model = model

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
	// Reset to the initially selected agent group (e.g. `dagger agent`), if any,
	// so .clear returns to those agents rather than a blank LLM. Preserve the
	// currently selected model.
	if s.initialLLM != nil {
		llm := s.initialLLM
		if s.model != "" {
			llm = llm.WithModel(s.model)
		}
		s.updateLLM(llm)
		return
	}
	// The LLM binds the current workspace by default (see core.NewLLM), so its
	// schema and file-editing surface derive from the user's workspace.
	s.updateLLM(s.dag.LLM(dagger.LLMOpts{Model: s.model}))
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

	// Resolve any @-path references in the prompt, mounting them read-only in
	// the workspace and annotating the prompt with their workspace locations.
	input = s.attachReferences(ctx, input)

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
		// update the sidebar after every step, not after the entire loop; step
		// is lazy, so sync to force it and re-root on the materialized state
		prompted, err = prompted.Step().Sync(ctx)
		if err != nil {
			return s, err
		}

		if err := s.updateLLM(prompted); err != nil {
			return s, err
		}

		if err := s.updateStatusLine(prompted); err != nil {
			return s, err
		}

		// In --debug, surface how much this step grew the context, so spikes
		// (e.g. a tool dumping a huge result) are visible between steps.
		s.reportContextUsage(ctx, prompted)

		// Auto-save after every step so sessions are preserved even if the
		// process is interrupted mid-turn.
		if s.onStep != nil {
			s.onStep(s)
		}

		hasMore, err := prompted.HasPending(s.plumbingCtx)
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

	return s, nil
}

func (s *LLMSession) updateLLM(llm *dagger.LLM) error {
	s.llm = llm

	// figure out what the model resolved to
	model, err := s.llm.Model(s.plumbingCtx)
	if err != nil {
		return err
	}
	s.model = model
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

// reportContextUsage emits a --debug span showing this step's context size (the
// full prompt sent to the model) and how much it grew since the previous step,
// so context spikes (e.g. a tool dumping a huge result) are visible between
// steps. LLM.TokenUsage is cumulative over the message history, so its change
// since the previous step is this step's own prompt (each step adds one
// assistant message). Compaction resets the history (WithoutMessageHistory),
// dropping the cumulative total; a drop is treated as a fresh baseline rather
// than negative growth.
func (s *LLMSession) reportContextUsage(ctx context.Context, llm *dagger.LLM) {
	if !debugFlag {
		return
	}
	usage := llm.TokenUsage()
	input, err := usage.InputTokens(s.plumbingCtx)
	if err != nil {
		return
	}
	cacheReads, err := usage.CachedTokenReads(s.plumbingCtx)
	if err != nil {
		return
	}
	cacheWrites, err := usage.CachedTokenWrites(s.plumbingCtx)
	if err != nil {
		return
	}

	cumulative := input + cacheReads + cacheWrites
	stepContext := cumulative - s.prevContextTokens
	if stepContext < 0 {
		// Compaction reset the cumulative total; this step is the new baseline.
		stepContext = cumulative
	}
	growth := stepContext - s.prevStepContext
	s.prevContextTokens = cumulative
	s.prevStepContext = stepContext

	_, span := Tracer().Start(ctx, fmt.Sprintf("context %s tokens (%s)",
		fmtTokenCount(stepContext), fmtTokenGrowth(growth)),
		telemetry.Reveal())
	span.End()
}

func fmtTokenCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func fmtTokenGrowth(n int) string {
	switch {
	case n > 0:
		return "▲ +" + fmtTokenCount(n)
	case n < 0:
		return "▼ -" + fmtTokenCount(-n)
	default:
		return "no change"
	}
}

// updateStatusLine refreshes the compact status line. During a live turn the
// frontend recomputes the token rollup and cost from live metrics (all models +
// sub-agents) at render time, so they stay current between turns; here we supply
// the model, subscription label, auto-compact state, context occupancy, and a
// token/cost snapshot read from the LLM object itself. That snapshot is the
// fallback the frontend renders before any metrics arrive — most visibly on
// load/resume, where the conversation has usage but no live metrics yet.
func (s *LLMSession) updateStatusLine(llm *dagger.LLM) error {
	contextTokens, err := llm.ContextTokens(s.plumbingCtx)
	if err != nil {
		return err
	}

	statusData := idtui.StatusLineData{
		Model:             s.model,
		SubscriptionLabel: s.subscriptionLabel(),
		ContextPercent:    -1, // unknown by default
		AutoCompact:       s.ShouldAutocompact(),
	}

	// Seed the cumulative token rollup and cost straight from the LLM object so
	// the status line is populated immediately on load/resume, before any new
	// metrics arrive. During a live turn the frontend overrides these with the
	// live metric rollup (all models + sub-agents); this is the fallback that
	// keeps a resumed conversation from rendering an empty bar. Best-effort:
	// stats aren't worth failing a turn over.
	usage := llm.TokenUsage()
	statusData.InputTokens, _ = usage.InputTokens(s.plumbingCtx)
	statusData.OutputTokens, _ = usage.OutputTokens(s.plumbingCtx)
	statusData.CacheReads, _ = usage.CachedTokenReads(s.plumbingCtx)
	statusData.CacheWrites, _ = usage.CachedTokenWrites(s.plumbingCtx)
	if provider, err := llm.Provider(s.plumbingCtx); err == nil {
		statusData.TotalCost = modelcatalog.Cost(provider, s.model,
			int64(statusData.InputTokens), int64(statusData.OutputTokens),
			int64(statusData.CacheReads), int64(statusData.CacheWrites))
	}

	// The engine is the source of truth for the context window (backed by the
	// shared catwalk catalog); it reports 0 for uncatalogued/local models or an
	// older engine without the field.
	contextWindow, err := llm.ContextWindow(s.plumbingCtx)
	if err != nil {
		contextWindow = 0
	}
	if contextWindow > 0 {
		statusData.ContextWindow = contextWindow
		if contextTokens > 0 {
			statusData.ContextPercent = float64(contextTokens) / float64(contextWindow) * 100
		}
	}
	s.frontend.SetStatusLine(statusData)

	// Best-effort: refresh the "Changes" preview from the workspace overlay diff.
	// Never fail a turn on a preview error (e.g. an unbound/rootless workspace).
	if err := s.updateChangesPreview(llm); err != nil {
		slog.Debug("could not refresh changes preview", "error", err)
	}

	return nil
}

// updateChangesPreview refreshes the "Changes" notification bubble with a summary
// of the workspace's pending overlay edits (Workspace.changes). Pressing ctrl+s
// exports them to the local Git workspace (see ExportChanges). When there are no
// pending edits the bubble is cleared (an empty body renders nothing).
func (s *LLMSession) updateChangesPreview(llm *dagger.LLM) error {
	entries, err := idtui.PreviewPatch(s.plumbingCtx, s.dag, llm.Workspace().Changes())
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		s.frontend.SetSidebarContent(idtui.SidebarSection{Title: "Changes"})
		return nil
	}
	s.frontend.SetSidebarContent(idtui.SidebarSection{
		Title: "Changes",
		ContentFunc: func(width int) string {
			var buf strings.Builder
			patchpreview.Summarize(idtui.NewOutput(&buf), entries, width)
			return buf.String()
		},
		KeyMap: []key.Binding{
			key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "save")),
			key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "reset")),
		},
	})
	return nil
}

// ExportChanges writes the workspace's pending overlay edits to its local Git
// workspace (Workspace.export), then refreshes the changes preview. It is the
// ctrl+s action; export fails clearly when the workspace cannot persist (a
// remote ref, a synthetic workspace, or a local dir with no Git root).
func (s *LLMSession) ExportChanges(ctx context.Context) error {
	if s.llm == nil {
		return fmt.Errorf("no LLM session active")
	}
	if err := s.llm.Workspace().Export(ctx); err != nil {
		return err
	}
	// The exported edits now live on disk, so reset the LLM's workspace
	// binding to its base: the persisted recipe (globalID) must stop replaying
	// the applied overlays, since re-deriving them against the updated files on
	// a later session load fails (withReplaced no longer finds its search
	// text) or silently re-applies them. Sync eagerly so a failed reset
	// surfaces here rather than corrupting later saves.
	reset, err := s.llm.WithResetWorkspace().Sync(ctx)
	if err != nil {
		return fmt.Errorf("reset workspace after export: %w", err)
	}
	if err := s.updateLLM(reset); err != nil {
		return err
	}
	if s.onStep != nil {
		s.onStep(s)
	}
	return s.updateChangesPreview(s.llm)
}

// ResetWorkspace discards the workspace's pending overlay edits, re-binding the
// LLM to the live workspace base (WithResetWorkspace) without exporting first.
// It is the ctrl+u action: conceptually the opposite direction of ctrl+s, it
// "uploads" the host's current state to the agent by throwing away the agent's
// accumulated changes rather than writing them out. Sync eagerly so a failed
// reset surfaces here rather than corrupting later saves.
func (s *LLMSession) ResetWorkspace(ctx context.Context) error {
	if s.llm == nil {
		return fmt.Errorf("no LLM session active")
	}
	reset, err := s.llm.WithResetWorkspace().Sync(ctx)
	if err != nil {
		return fmt.Errorf("reset workspace: %w", err)
	}
	if err := s.updateLLM(reset); err != nil {
		return err
	}
	if s.onStep != nil {
		s.onStep(s)
	}
	return s.updateChangesPreview(s.llm)
}

const autoCompactReserveTokens = 16_384

// maybeAutoCompact checks whether the current context is inside the response
// reserve and automatically compacts if so.
func (s *LLMSession) maybeAutoCompact(ctx context.Context) (_ *dagger.LLM, rerr error) {
	if !s.ShouldAutocompact() {
		return s.llm, nil
	}

	contextTokens, err := s.llm.ContextTokens(s.plumbingCtx)
	if err != nil {
		return nil, err
	}

	// The engine reports the model's context window (shared catwalk catalog);
	// 0 means uncatalogued/local, so we can't determine a threshold — skip.
	contextWindow, err := s.llm.ContextWindow(s.plumbingCtx)
	if err != nil || contextWindow <= 0 {
		return s.llm, nil
	}

	threshold := contextWindow - autoCompactReserveTokens
	if threshold <= 0 {
		threshold = int(float64(contextWindow) * 0.80)
	}

	if contextTokens > threshold {
		ctx, span := Tracer().Start(ctx, "auto-compacting LLM history", telemetry.Reveal())
		defer telemetry.EndWithCause(span, &rerr)
		return s.Compact(ctx)
	}

	return s.llm, nil
}

func (s *LLMSession) Clear() *LLMSession {
	s = s.Fork()
	s.reset()
	s.references = nil
	s.updateReferencesPreview()
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
	transcript, err := s.llm.Transcript(ctx)
	if err != nil {
		return s, err
	}
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	fmt.Fprintln(stdio.Stdout, transcript)
	return s, nil
}

func (s *LLMSession) Model(model string) (*LLMSession, error) {
	s = s.Fork()
	s.updateLLM(s.llm.WithModel(model))
	model, err := s.llm.Model(s.plumbingCtx)
	if err != nil {
		return nil, err
	}
	s.model = model
	return s, nil
}

//go:embed llm_branch_summary.md
var branchSummaryPrompt string

// Summarization input budget: fall back to a conservative context window
// when the model's real one is unknown, and reserve room for the prompt
// scaffolding and the model's output, estimating ~4 chars per token.
const (
	summaryFallbackWindowTokens = 128000
	summaryReserveTokens        = 16384
	summaryCharsPerToken        = 4
)

// trimConversationForSummary drops the oldest serialized messages so the
// conversation fits the summarization input budget within the model's
// context window (tokens; 0 or negative uses a conservative fallback),
// keeping the newest content. The transcript joins messages with blank
// lines, so trimming happens at those boundaries; a notice marks the
// omission. Without this, a near-window-sized history would leave the
// summarization request little or no room to respond.
func trimConversationForSummary(text string, contextWindow int) string {
	if contextWindow <= 0 {
		contextWindow = summaryFallbackWindowTokens
	}
	budgetChars := (contextWindow - summaryReserveTokens) * summaryCharsPerToken
	if budgetChars < summaryReserveTokens*summaryCharsPerToken {
		// Tiny or reserve-sized windows: keep at least a minimal budget so
		// the summary sees some conversation.
		budgetChars = summaryReserveTokens * summaryCharsPerToken
	}
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

	conversationText, err := s.llm.Transcript(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to serialize history: %w", err)
	}
	// Budget the input to the model's actual context window; unknown models
	// (e.g. local endpoints) report null (decoded as 0) and get a
	// conservative fallback.
	contextWindow, err := s.llm.ContextWindow(ctx)
	if err != nil {
		contextWindow = 0
	}
	conversationText = trimConversationForSummary(conversationText, contextWindow)

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
		Loop(dagger.LLMLoopOpts{MaxSteps: 1, MaxTokens: 2048}).
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
	llmID, err := s.llm.PortableID(ctx)
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

// LoadSession loads an LLM session from disk by UUID. The message history is
// replayed for telemetry against replayCtx (not ctx), so callers can surface
// the replayed conversation at the conversation's top level rather than nested
// under the command span that triggered the load. Pass ctx for replayCtx to
// replay in place.
func (s *LLMSession) LoadSession(ctx, replayCtx context.Context, sessionID string) error {
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
	// conversation in its scrollback. Replay against replayCtx so the spans nest
	// where the caller wants the conversation to appear (e.g. the top level for
	// .resume) rather than under the triggering command span.
	if _, err := loadedLLM.Replay(replayCtx); err != nil {
		slog.Warn("failed to replay session history", "error", err)
	}

	// Restoring a session replays any un-flushed workspace edits as recorded
	// patches; hunks that no longer fit the live files degrade to conflict
	// markers (onConflict: LEAVE_CONFLICT_MARKERS). The model's history
	// describes a workspace that is now partially fiction, so tell it what
	// needs resolving rather than letting it stumble over the markers.
	if cue := conflictMarkerCue(ctx, loadedLLM); cue != "" {
		loadedLLM = loadedLLM.WithSystemPrompt(cue)
	}

	if err := s.updateLLM(loadedLLM); err != nil {
		return err
	}
	return s.updateStatusLine(loadedLLM)
}

// conflictMarkerCue reports whether restoring the session left conflict
// markers in the workspace overlay, returning a system-prompt cue listing the
// affected files, or "" when restoration was clean.
//
// Only files touched by the overlay can carry restore-time markers (they are
// produced by replaying the recorded patches), so the search is scoped to the
// overlay changeset's added and modified paths — which also makes this free
// for sessions that flushed their changes before saving: the changeset is
// empty and nothing is searched. Best-effort throughout; a failed check must
// not block loading the session.
func conflictMarkerCue(ctx context.Context, llm *dagger.LLM) string {
	changes := llm.Workspace().Changes()
	added, err := changes.AddedPaths(ctx)
	if err != nil {
		slog.Debug("skipping conflict-marker check", "error", err)
		return ""
	}
	modified, err := changes.ModifiedPaths(ctx)
	if err != nil {
		slog.Debug("skipping conflict-marker check", "error", err)
		return ""
	}
	paths := slices.Concat(added, modified)
	if len(paths) == 0 {
		return ""
	}
	results, err := changes.After().Search(ctx, "<<<<<<< workspace", dagger.DirectorySearchOpts{
		Literal:   true,
		FilesOnly: true,
		Paths:     paths,
	})
	if err != nil {
		slog.Debug("skipping conflict-marker check", "error", err)
		return ""
	}
	files := make([]string, 0, len(results))
	seen := map[string]bool{}
	for _, res := range results {
		fp, err := res.FilePath(ctx)
		if err != nil || seen[fp] {
			continue
		}
		seen[fp] = true
		files = append(files, fp)
	}
	if len(files) == 0 {
		return ""
	}
	sort.Strings(files)
	return fmt.Sprintf(
		"While restoring this session, some of your earlier edits no longer applied cleanly to the "+
			"workspace and were left as conflict markers (\"<<<<<<< workspace\" ... \">>>>>>> patch\") in: %s. "+
			"The workspace content may differ from what the conversation above describes. "+
			"Review these files and resolve the markers before continuing.",
		strings.Join(files, ", "))
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
