package daggercmd

import (
	"context"
	_ "embed"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/modelcatalog"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/internal/cmd/dagger/llmconfig"
	telemetry "github.com/dagger/otel-go"
)

const agentVar = "agent"

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

// LLMSession owns an interactive session's conversations
// (hack/designs/async-agents.md §5.1). It holds the session-wide plumbing --
// the dagger client, the shell handler, the frontend, the plumbing span --
// plus the conversations themselves and which one the prompt addresses.
//
// Routing resolves in exactly one place: Target. Nothing else may infer a
// destination from what happens to be running, because with a roster the
// busy agent and the focused agent are routinely different agents.
type LLMSession struct {
	frontend idtui.Frontend
	dag      *dagger.Client
	shell    *shellCallHandler

	// agents are the session's conversations, in the order they joined, and
	// target is the one prompts go to. Guarded by mu: a focus keypress and a
	// running turn touch them from different goroutines.
	agents []*sessionAgent
	target *sessionAgent
	mu     sync.Mutex

	// onStep runs presentation work (title generation) after a prompt turn.
	onStep func(*sessionAgent)
	// onStepL keeps simultaneous conversation callbacks serialized.
	onStepL sync.Mutex

	plumbingCtx  context.Context
	plumbingSpan trace.Span

	// primaryCtx carries the interactive command's root span. The generated
	// title is emitted and applied there, while the model call that derives it
	// stays beneath plumbingCtx. Title generation is attempted once per title
	// identity; resetTitle starts a fresh identity after branch/resume.
	primaryCtx      context.Context
	title           string
	titleAttempted  bool
	titleGeneration uint64
	titleL          sync.Mutex
	titleGenerator  func(context.Context, *sessionAgent, string) (string, error)

	// subscriptionLabelCache caches the OAuth subscription label for the status
	// line, resolved lazily on first use.
	subscriptionLabelCache string

	// workspaceHostRoot/workspaceHostCwd are the host filesystem paths of the
	// workspace root and cwd, resolved lazily on the first @-path and cached
	// for the session (workspaceHostResolved records that the lookup ran, since
	// a non-local workspace legitimately resolves to empty). Used to detect
	// @-paths that already live in the workspace.
	workspaceHostResolved bool
	workspaceHostRoot     string
	workspaceHostCwd      string

	// contextVizURL is the context visualizer's URL once its local web
	// server has started (see context_viz.go); contextVizL guards the
	// start-once dance.
	contextVizURL string
	contextVizL   sync.Mutex
}

func NewLLMSession(
	ctx context.Context,
	dag *dagger.Client,
	llmModel string,
	shellHandler *shellCallHandler,
	frontend idtui.Frontend,
	initialLLM *dagger.LLM,
) (*LLMSession, error) {
	return newLLMSession(ctx, dag, llmModel, shellHandler, frontend, initialLLM, false)
}

// newRestoringLLMSession creates client plumbing without evaluating an LLM or
// consulting a destination workspace/provider. Only traced anchors may supply
// those capabilities during restore.
func newRestoringLLMSession(ctx context.Context, dag *dagger.Client, shellHandler *shellCallHandler, frontend idtui.Frontend) (*LLMSession, error) {
	return newLLMSession(ctx, dag, "", shellHandler, frontend, nil, true)
}

func newLLMSession(ctx context.Context, dag *dagger.Client, llmModel string, shellHandler *shellCallHandler, frontend idtui.Frontend, initialLLM *dagger.LLM, restoring bool) (*LLMSession, error) {
	s := &LLMSession{
		dag:        dag,
		shell:      shellHandler,
		frontend:   frontend,
		primaryCtx: ctx,
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
		sink.SetLLMCostFunc(modelcatalog.Cost)
	}

	// The session always has one conversation: its own, spawned as an agent
	// on the first prompt submit and targeted from the start.
	own := s.newAgent(defaultAgentName)
	own.model = llmModel
	s.agents = []*sessionAgent{own}
	s.target = own
	// Install the selected composition before status reads so its frozen
	// checkpoint is captured only once and belongs to this conversation.
	if !restoring {
		if initialLLM == nil {
			workspace, err := snapshotWorkspace(ctx, dag)
			if err != nil {
				return nil, err
			}
			initialLLM = dag.LLM(dagger.LLMOpts{Model: llmModel}).WithWorkspace(workspace)
		}
		if err := own.setInitialLLM(initialLLM); err != nil {
			return nil, err
		}
	}

	if sink, ok := frontend.(interface {
		SetLLMToolsProvider(idtui.LLMToolsProvider)
	}); ok {
		sink.SetLLMToolsProvider(s.tools)
	}

	return s, nil
}

// tools resolves focus and the runtime snapshot at request time, so background
// conversations cannot replace the toolset shown for the selected agent.
func (s *LLMSession) tools(ctx context.Context) (string, error) {
	a := s.Target()
	if a == nil {
		return "", fmt.Errorf("no LLM session active")
	}
	a.llmL.RLock()
	llm := a.llm
	a.llmL.RUnlock()
	if rt := a.runtime(); rt != nil {
		id, err := rt.SnapshotID(ctx)
		if err != nil {
			return "", err
		}
		llm = dagger.Ref[*dagger.LLM](s.dag, id)
	}
	if llm == nil {
		return "", fmt.Errorf("no LLM session active")
	}
	return llm.Tools(ctx)
}

// defaultAgentName is the display label the session's own conversation spawns
// under. It is a label only: instance uniqueness is minted by spawn, so
// nothing here needs to be unique.
const defaultAgentName = "agent"

// Target is the conversation the prompt addresses -- the single place message
// routing resolves. It is never nil: the session's own conversation exists
// from construction.
func (s *LLMSession) Target() *sessionAgent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.target
}

// SetTarget points the prompt at a conversation the session already holds.
// Focus moves only by keypress, so nothing calls this from an event path.
func (s *LLMSession) SetTarget(a *sessionAgent) {
	s.mu.Lock()
	if a != nil {
		s.target = a
	}
	s.mu.Unlock()
}

// TargetAgentID is the runtime handle of the runtime the prompt currently
// addresses, or "" when the target has not spawned (or attached to) one yet.
// The roster marks its entry with it.
func (s *LLMSession) TargetAgentID() string {
	target := s.Target()
	if target == nil {
		return ""
	}
	target.agentL.Lock()
	defer target.agentL.Unlock()
	return target.agentHandle
}

// agentByHandle finds the conversation driving the given runtime, or nil.
func (s *LLMSession) agentByHandle(agentHandle string) *sessionAgent {
	if agentHandle == "" {
		return nil
	}
	s.mu.Lock()
	agents := slices.Clone(s.agents)
	s.mu.Unlock()
	for _, a := range agents {
		a.agentL.Lock()
		match := a.agentHandle == agentHandle
		a.agentL.Unlock()
		if match {
			return a
		}
	}
	return nil
}

// Resumable reports whether the session holds an agent runtime -- spawned by
// a prompt, attached, or restored -- so its trace carries something for
// `dagger agent --resume` to restore. A session nobody spoke to has none.
func (s *LLMSession) Resumable() bool {
	s.mu.Lock()
	agents := slices.Clone(s.agents)
	s.mu.Unlock()
	for _, a := range agents {
		a.agentL.Lock()
		bound := a.agentHandle != ""
		a.agentL.Unlock()
		if bound {
			return true
		}
	}
	return false
}

// Focus points the prompt at the agent with the given runtime handle, attaching
// to it first when the session is not already driving it. encodedID is a
// handle the client rebuilt from the trace (design §9: telemetry is the
// directory); it is only consulted when attaching.
//
// A failed attach leaves focus where it was: an agent the client cannot
// address is one it can watch, not one it can talk to.
func (s *LLMSession) Focus(ctx context.Context, agentHandle, name, encodedID string) error {
	target := s.agentByHandle(agentHandle)
	if target == nil {
		attached, err := s.Attach(ctx, agentHandle, name, encodedID)
		if err != nil {
			return err
		}
		target = attached
	}
	s.SetTarget(target)
	return target.refreshUI()
}

// Attach adopts an agent this session did not spawn as a conversation of its
// own, rooted on the runtime's last committed snapshot -- the honest chain,
// pinned by ID. The runtime is not owned: this session may prompt and
// interrupt it, but clearing the conversation must never stop somebody else's
// worker.
//
// Re-attaching to the same agent returns the existing conversation rather than
// forking a second view of one runtime.
func (s *LLMSession) Attach(ctx context.Context, agentHandle, name, encodedID string) (*sessionAgent, error) {
	return s.attach(ctx, agentHandle, name, encodedID, false)
}

// AttachRestored adopts an agent this session RE-HYDRATED from a trace
// (hack/designs/resume-from-trace.md §5.3). Same adoption, opposite ownership:
// a restored agent has no other driver -- the session that published it is
// gone -- so this session is the one whose business it is to stop it, and
// .clear stopping the runtime is right.
func (s *LLMSession) AttachRestored(ctx context.Context, agentHandle, name, encodedID string) (*sessionAgent, error) {
	return s.attach(ctx, agentHandle, name, encodedID, true)
}

func (s *LLMSession) attach(ctx context.Context, agentHandle, name, encodedID string, owned bool) (*sessionAgent, error) {
	if existing := s.agentByHandle(agentHandle); existing != nil {
		return existing, nil
	}
	if encodedID == "" {
		return nil, fmt.Errorf("agent %q is not addressable: no handle could be rebuilt from the trace", name)
	}
	rt := liveAgent{
		dag:   s.dag,
		agent: dagger.Ref[*dagger.Agent](s.dag, dagger.ID(encodedID)),
	}
	snapID, err := rt.SnapshotID(ctx)
	if err != nil {
		return nil, fmt.Errorf("attach to agent %q: %w", name, err)
	}
	if name == "" {
		name = "agent"
	}
	attached := s.newAgent(name)
	attached.bindRuntime(rt, agentHandle, encodedID, owned)
	snapshot := dagger.Ref[*dagger.LLM](s.dag, snapID)
	// Reset remains trace-authoritative even after export advances its separate
	// comparison baseline, or an explicit Ctrl+U imports a new client workspace.
	attached.tracedReset = snapshot.WithoutMessageHistory()
	// An attached/trace-restored conversation does not carry the checkpoint it
	// originally synchronized from. Its current snapshot workspace is the safe
	// best-effort baseline: it is portable with the snapshot and cannot trigger
	// an unlike-host-root comparison. A later explicit save/reset advances it.
	if workspace := snapshot.Workspace(); workspace != nil {
		if _, err := workspace.ID(ctx); err != nil {
			slog.Debug("attached agent snapshot has no workspace synchronization baseline", "error", err)
		} else {
			attached.setLastSynced(workspace)
		}
	}
	if err := attached.setLLM(snapshot); err != nil {
		return nil, fmt.Errorf("attach to agent %q: %w", name, err)
	}
	s.mu.Lock()
	s.agents = append(s.agents, attached)
	s.mu.Unlock()
	return attached, nil
}

// SubmitToTarget offers a message to the target conversation's in-flight turn,
// reporting whether there was one to absorb it. Nothing else routes messages:
// a message submitted while some OTHER agent is mid-turn must not be delivered
// to that agent just because it happens to be the busy one.
func (s *LLMSession) SubmitToTarget(msg string) bool {
	return s.SubmitPromptToTarget(idtui.PromptInput{Text: msg})
}

func (s *LLMSession) SubmitPromptToTarget(input idtui.PromptInput) bool {
	target := s.Target()
	if target == nil {
		return false
	}
	return target.SubmitPrompt(input)
}

// InterruptTarget preempts the target conversation, reporting whether there
// was anything to preempt. This is Ctrl-C with a roster: it acts on the
// focused agent's runtime, not on whichever turn holds the client.
func (s *LLMSession) InterruptTarget() bool {
	target := s.Target()
	if target == nil {
		return false
	}
	return target.Interrupt()
}

// stepped schedules presentation work without delaying the completed turn.
func (s *LLMSession) stepped(a *sessionAgent) {
	if s.onStep == nil {
		return
	}
	a.stepWG.Add(1)
	go func() {
		defer a.stepWG.Done()
		s.onStepL.Lock()
		defer s.onStepL.Unlock()
		s.onStep(a)
	}()
}

// ensureTitle derives and publishes a title once for the current branch.
func (s *LLMSession) ensureTitle(a *sessionAgent, initialPrompt string) string {
	if strings.TrimSpace(initialPrompt) == "" {
		return ""
	}

	s.titleL.Lock()
	if s.titleAttempted {
		title := s.title
		s.titleL.Unlock()
		return title
	}
	s.titleAttempted = true
	generation := s.titleGeneration
	s.titleL.Unlock()

	ctx := s.plumbingCtx
	if ctx == nil {
		ctx = context.Background()
	}
	generate := s.titleGenerator
	if generate == nil {
		generate = func(ctx context.Context, a *sessionAgent, prompt string) (string, error) {
			return a.GenerateSessionTitle(ctx, prompt)
		}
	}
	// Internal hides the wrapper itself; Encapsulate also keeps the generator's
	// user/reply message spans out of the interactive conversation.
	generationCtx, generationSpan := Tracer().Start(
		ctx, "generate session title", telemetry.Internal(), telemetry.Encapsulate())
	generated, err := generate(generationCtx, a, initialPrompt)
	telemetry.EndWithCause(generationSpan, &err)
	if err != nil {
		slog.Debug("failed to generate session title; using initial prompt", "error", err)
	}
	title := normalizeSessionTitle(generated)
	if title == "" {
		title = normalizeSessionTitle(initialPrompt)
	}
	if title == "" {
		title = "Dagger agent session"
	}

	// A branch or resume may have reset the identity while the lightweight
	// request was in flight. Do not let the old request rename the new session.
	s.titleL.Lock()
	if generation != s.titleGeneration {
		title = s.title
		s.titleL.Unlock()
		return title
	}
	s.title = title
	s.titleL.Unlock()

	emitSessionTitle(s.primaryCtx, title)
	return title
}

// resetTitle makes the next completed turn title a newly branched or resumed
// title identity. An in-flight request from the old identity is discarded.
func (s *LLMSession) resetTitle() {
	s.titleL.Lock()
	s.title = ""
	s.titleAttempted = false
	s.titleGeneration++
	s.titleL.Unlock()
}

const maxSessionTitleRunes = 60

// normalizeSessionTitle turns either model output or the initial-prompt
// fallback into a single concise line suitable for span and session names.
func normalizeSessionTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	if line, _, ok := strings.Cut(title, "\n"); ok {
		title = line
	}
	title = strings.Join(strings.Fields(title), " ")
	if len(title) >= len("title:") && strings.EqualFold(title[:len("title:")], "title:") {
		title = strings.TrimSpace(title[len("title:"):])
	}
	if len(title) >= 2 {
		first, last := title[0], title[len(title)-1]
		if first == last && (first == '\'' || first == '"' || first == '`') {
			title = strings.TrimSpace(title[1 : len(title)-1])
		}
	}
	title = strings.TrimSuffix(title, ".")

	runes := []rune(title)
	if len(runes) <= maxSessionTitleRunes {
		return title
	}
	const ellipsis = "…"
	cut := maxSessionTitleRunes - len([]rune(ellipsis))
	for cut > 0 && runes[cut] != ' ' {
		cut--
	}
	if cut < maxSessionTitleRunes/2 {
		cut = maxSessionTitleRunes - len([]rune(ellipsis))
	}
	return strings.TrimSpace(string(runes[:cut])) + ellipsis
}

// emitSessionTitle attaches the title to the primary span in both mutable live
// span state and durable OTLP log form. The role attribute lets downstream
// consumers recognize this record without interpreting ordinary log bodies.
func emitSessionTitle(ctx context.Context, title string) {
	if ctx == nil || title == "" {
		return
	}
	trace.SpanFromContext(ctx).SetName(title)
	rec := log.Record{}
	rec.SetTimestamp(time.Now())
	rec.SetBody(log.StringValue(title))
	rec.AddAttributes(log.String(telemetryattrs.LogRoleAttr, telemetryattrs.LogRoleSpanName))
	telemetry.Logger(ctx, InstrumentationLibrary).Emit(ctx, rec)
}

// AgentStepped notifies the session that the trace reported a step boundary
// (a conversation commit) for the runtime with the given runtime handle. When
// that runtime backs the TARGET conversation, the conversation-scoped
// surfaces -- status line, changes preview -- are refreshed from its latest
// snapshot, so they track the agent step by step instead of turn by turn.
//
// Cheap by design: it is invoked from the frontend's telemetry ingestion, so
// everything that talks to the engine happens on the refresh goroutine
// (scheduleUIRefresh), never here.
func (s *LLMSession) AgentStepped(agentHandle string) {
	a := s.agentByHandle(agentHandle)
	if a == nil || !a.isTarget() {
		return
	}
	a.scheduleUIRefresh()
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

//go:embed llm_compact.md
var compactPrompt string

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
