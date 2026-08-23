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

	"github.com/google/uuid"
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

	// onStep, if set, is invoked after every prompt turn (including an
	// interrupted one). It is used to auto-save the session so it is
	// preserved across interruptions.
	onStep func(*sessionAgent)
	// onStepL serializes onStep runs: stepped fires them on background
	// goroutines (a save serializes the whole conversation, which for a long
	// session takes far too long to spend inside the turn), and two saves
	// interleaving on one file would corrupt it.
	onStepL sync.Mutex

	plumbingCtx  context.Context
	plumbingSpan trace.Span

	// primaryCtx carries the interactive command's root span. The generated
	// title is emitted and applied there, while the model call that derives it
	// stays beneath plumbingCtx. Title generation is attempted once per save
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
) (*LLMSession, error) {
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
	own.reset()
	// This plain prompt-mode LLM is the real starting value when no composed
	// agent replaces it. startInteractivePromptMode explicitly replaces this
	// baseline together with the composed LLM before entering the prompt.
	own.setLastSynced(own.llm.Workspace())

	// Grab the model to check for a valid config
	model, err := own.llm.Model(ctx)
	if err != nil {
		return nil, err
	}
	own.model = model

	return s, nil
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

// TargetAgentID is the instance ID of the runtime the prompt currently
// addresses, or "" when the target has not spawned (or attached to) one yet.
// The roster marks its entry with it.
func (s *LLMSession) TargetAgentID() string {
	target := s.Target()
	if target == nil {
		return ""
	}
	target.agentL.Lock()
	defer target.agentL.Unlock()
	return target.instanceID
}

// agentByInstance finds the conversation driving the given runtime, or nil.
func (s *LLMSession) agentByInstance(instanceID string) *sessionAgent {
	if instanceID == "" {
		return nil
	}
	s.mu.Lock()
	agents := slices.Clone(s.agents)
	s.mu.Unlock()
	for _, a := range agents {
		a.agentL.Lock()
		match := a.instanceID == instanceID
		a.agentL.Unlock()
		if match {
			return a
		}
	}
	return nil
}

// Focus points the prompt at the agent with the given instance ID, attaching
// to it first when the session is not already driving it. encodedID is a
// handle the client rebuilt from the trace (design §9: telemetry is the
// directory); it is only consulted when attaching.
//
// A failed attach leaves focus where it was: an agent the client cannot
// address is one it can watch, not one it can talk to.
func (s *LLMSession) Focus(ctx context.Context, instanceID, name, encodedID string) error {
	target := s.agentByInstance(instanceID)
	if target == nil {
		attached, err := s.Attach(ctx, instanceID, name, encodedID)
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
func (s *LLMSession) Attach(ctx context.Context, instanceID, name, encodedID string) (*sessionAgent, error) {
	return s.attach(ctx, instanceID, name, encodedID, false)
}

// AttachRestored adopts an agent this session RE-HYDRATED from a trace
// (hack/designs/resume-from-trace.md §5.3). Same adoption, opposite ownership:
// a restored agent has no other driver -- the session that published it is
// gone -- so this session is the one whose business it is to stop it, and
// .clear stopping the runtime is right.
func (s *LLMSession) AttachRestored(ctx context.Context, instanceID, name, encodedID string) (*sessionAgent, error) {
	return s.attach(ctx, instanceID, name, encodedID, true)
}

func (s *LLMSession) attach(ctx context.Context, instanceID, name, encodedID string, owned bool) (*sessionAgent, error) {
	if existing := s.agentByInstance(instanceID); existing != nil {
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
	attached.bindRuntime(rt, instanceID, encodedID, owned)
	snapshot := dagger.Ref[*dagger.LLM](s.dag, snapID)
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
	target := s.Target()
	if target == nil {
		return false
	}
	return target.Submit(msg)
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

// stepped notifies the session that a conversation reached a step boundary, so
// it can auto-save. The save runs in the background: serializing a long
// conversation's portable ID is the most expensive call in the client, and a
// turn that paid for it synchronously kept the "working" indicator lit long
// after the reply had landed. Saves serialize behind onStepL so concurrent
// steps cannot interleave writes to the session file.
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

// ensureTitle derives and publishes the title for the current save identity.
// It is called at the first completed turn, before that turn is auto-saved,
// rather than from AutoSaveSession itself so later saves do not spend another
// model call or emit duplicate title records.
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
// save identity. An in-flight request from the old identity is discarded.
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
// (a conversation commit) for the runtime with the given instance ID. When
// that runtime backs the TARGET conversation, the conversation-scoped
// surfaces -- status line, changes preview -- are refreshed from its latest
// snapshot, so they track the agent step by step instead of turn by turn.
//
// Cheap by design: it is invoked from the frontend's telemetry ingestion, so
// everything that talks to the engine happens on the refresh goroutine
// (scheduleUIRefresh), never here.
func (s *LLMSession) AgentStepped(instanceID string) {
	a := s.agentByInstance(instanceID)
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

// sessionMetadata stores metadata about a saved LLM session.
type sessionMetadata struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	LLMID     string `json:"llm_id"`
	Branch    string `json:"branch,omitempty"`

	// WorkspaceBaselineID is the portable ID of an otherwise-empty LLM bound
	// to the conversation's last-synced workspace. Workspace.id itself is an
	// engine-local handle and Workspace has no portableID field, so the small
	// LLM wrapper carries the workspace recipe across engine sessions.
	WorkspaceBaselineID string `json:"workspace_baseline_id,omitempty"`
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

// AutoSaveSession saves the conversation automatically under name, stored on
// disk under a UUIDv7 filename for anonymity and time-sorted ordering. If
// existingUUID is non-empty the same file is updated in-place; otherwise a new
// UUIDv7 is generated. Returns the UUID used.
//
// NOTE: the save identity (name/sessionUUID) is still session-wide while this
// saves ONE conversation, so with several conversations in a session the last
// to step wins the file. Per-conversation save identity is the follow-up
// (hack/designs/async-agents.md §5.1).
func (a *sessionAgent) AutoSaveSession(ctx context.Context, name string, existingUUID string) (string, error) {
	if a.llm == nil {
		return existingUUID, nil // nothing to save
	}

	sessionDir, err := getSessionDir()
	if err != nil {
		return existingUUID, err
	}

	// Persist the portable, self-contained (recipe-form) ID rather than the
	// default runtime handle, which is an engine-local reference that cannot be
	// resolved once this session's engine is gone.
	llmID, err := a.llm.PortableID(ctx)
	if err != nil {
		return existingUUID, fmt.Errorf("failed to get LLM ID: %w", err)
	}
	a.session.shell.assignAgent(llmID)

	// Workspace IDs are engine-local handles, so persist the baseline through a
	// minimal portable LLM recipe that binds it. Best-effort for attached or
	// trace-restored conversations whose snapshot may be unbound: the
	// conversation remains saveable, and LoadSession safely falls back to its
	// restored workspace when this field is absent.
	var workspaceBaselineID string
	if baseline := a.lastSynced(nil); baseline != nil {
		portable, err := a.session.dag.LLM().WithWorkspace(baseline).PortableID(ctx)
		if err != nil {
			slog.Debug("could not persist workspace synchronization baseline", "error", err)
		} else {
			workspaceBaselineID = string(portable)
		}
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
		Name:                name,
		Model:               a.model,
		CreatedAt:           time.Now().UTC().Format(time.RFC3339),
		LLMID:               string(llmID),
		WorkspaceBaselineID: workspaceBaselineID,
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

	slog.Debug("auto-saved LLM session", "id", sessionID, "name", name, "file", sessionFile)
	return sessionID, nil
}

// LoadSession loads an LLM session from disk by UUID, replacing this
// conversation. The message history is replayed for telemetry against
// replayCtx (not ctx), so callers can surface the replayed conversation at the
// conversation's top level rather than nested under the command span that
// triggered the load. Pass ctx for replayCtx to replay in place.
func (a *sessionAgent) LoadSession(ctx, replayCtx context.Context, sessionID string) error {
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

	loadedLLM := dagger.Ref[*dagger.LLM](a.session.dag, dagger.ID(metadata.LLMID))
	var baseline *dagger.Workspace
	fallback := loadedLLM.Workspace()
	if _, err := fallback.ID(ctx); err != nil {
		slog.Debug("restored conversation has no workspace synchronization baseline", "error", err)
	} else {
		baseline = fallback
	}
	if metadata.WorkspaceBaselineID != "" {
		// Workspace.id is not portable, so the metadata ID addresses the minimal
		// LLM wrapper written by AutoSaveSession. Resolve its bound workspace now;
		// if an old/corrupt save cannot rebuild it, retain the safe same-workspace
		// fallback instead of failing resume or comparing unrelated host roots.
		portable := dagger.Ref[*dagger.LLM](a.session.dag, dagger.ID(metadata.WorkspaceBaselineID))
		candidate := portable.Workspace()
		if _, err := candidate.ID(ctx); err != nil {
			slog.Debug("could not restore workspace synchronization baseline", "error", err)
		} else {
			baseline = candidate
		}
	}

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
	if cue := conflictMarkerCue(ctx, loadedLLM, baseline); cue != "" {
		loadedLLM = loadedLLM.WithSystemPrompt(cue)
	}

	// Restore the baseline together with the conversation so any asynchronous
	// status refresh sees a consistent pair.
	return a.updateSyncedLLM(loadedLLM, baseline)
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
func conflictMarkerCue(ctx context.Context, llm *dagger.LLM, before *dagger.Workspace) string {
	if llm == nil || before == nil {
		return ""
	}
	changes := llm.Workspace().Changes(dagger.WorkspaceChangesOpts{From: before})
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
