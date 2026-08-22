package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/modelcatalog"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/patchpreview"
	telemetry "github.com/dagger/otel-go"
)

// One conversation of an interactive session (hack/designs/async-agents.md
// §5.1).
//
// A session used to BE a conversation: one LLM value, one agent runtime, and
// every submit path inferring its destination from whichever turn happened to
// be in flight. Focus outlaws that inference -- a message belongs to the agent
// the user is looking at, never to the one that happens to be busy -- so the
// conversation is its own type here, and LLMSession (llm.go) becomes the owner
// that routes to it. Everything describing ONE conversation lives below;
// everything session-wide stays there.

// agentRuntime is the slice of the engine's Agent API a conversation drives.
//
// It exists so the two policies that decide correctness -- who a submitted
// message routes to, and whose business it is to stop a runtime -- are
// testable without an engine: a fake runtime records the verbs it was asked
// to perform, and the tests assert on the policy rather than on GraphQL.
type agentRuntime interface {
	// SendMessage enqueues a message, returning a handle on the turn that
	// consumes it. Never blocks on the turn itself.
	SendMessage(ctx context.Context, msg string) (agentMessage, error)
	// Resume un-parks a paused (or failed) runtime; a no-op otherwise.
	Resume(ctx context.Context) error
	// Interrupt preempts the in-flight step, keeping the completed prefix,
	// and parks the runtime PAUSED.
	Interrupt(ctx context.Context) error
	// WaitFor blocks until the runtime reaches the given state.
	WaitFor(ctx context.Context, state dagger.AgentState) error
	// State is the runtime's projected lifecycle state.
	State(ctx context.Context) (dagger.AgentState, error)
	// SnapshotID is the ID of the runtime's last committed conversation --
	// the honest chain the client re-roots on.
	SnapshotID(ctx context.Context) (dagger.ID, error)
	// Stop releases the runtime, leaving a readable tombstone.
	Stop(ctx context.Context) error
	// Reseed replaces the runtime's committed conversation in place,
	// keeping the instance -- the continuity path for wholesale LLM
	// replacements (compaction, workspace rebind, model change).
	Reseed(ctx context.Context, llm *dagger.LLM) error
}

// agentMessage is a handle on one enqueued message: its delivery evidence,
// and the reply of the turn that consumed it.
type agentMessage interface {
	Delivery(ctx context.Context) (dagger.AgentMessageDelivery, error)
	Await(ctx context.Context) (string, error)
}

// liveAgent binds a real engine Agent to the agentRuntime interface.
type liveAgent struct {
	dag   *dagger.Client
	agent *dagger.Agent
}

var _ agentRuntime = liveAgent{}

func (l liveAgent) SendMessage(ctx context.Context, msg string) (agentMessage, error) {
	// Send executes eagerly (it returns an ID scalar) and the returned ID is
	// pinned to the replayable `…agent(id:…)!message(id:…)` chain, so an
	// await on it can be canceled without losing the handle.
	msgID, err := l.agent.Send(ctx, msg)
	if err != nil {
		return nil, err
	}
	return dagger.Ref[*dagger.AgentMessage](l.dag, msgID), nil
}

func (l liveAgent) Resume(ctx context.Context) error {
	_, err := l.agent.Resume(ctx)
	return err
}

func (l liveAgent) Interrupt(ctx context.Context) error {
	_, err := l.agent.Interrupt(ctx)
	return err
}

func (l liveAgent) WaitFor(ctx context.Context, state dagger.AgentState) error {
	_, err := l.agent.WaitFor(ctx, dagger.AgentWaitForOpts{State: state})
	return err
}

func (l liveAgent) State(ctx context.Context) (dagger.AgentState, error) {
	return l.agent.State(ctx)
}

func (l liveAgent) SnapshotID(ctx context.Context) (dagger.ID, error) {
	return l.agent.Snapshot().ID(ctx)
}

func (l liveAgent) Stop(ctx context.Context) error {
	_, err := l.agent.Stop(ctx)
	return err
}

func (l liveAgent) Reseed(ctx context.Context, llm *dagger.LLM) error {
	_, err := l.agent.Reseed(ctx, llm)
	return err
}

// sessionAgent is one conversation: its LLM value, the agent runtime backing
// its turns, and the per-conversation state the UI describes (model,
// references, auto-compact, context baselines).
//
// The session-wide plumbing it needs -- the dagger client, the frontend, the
// plumbing span -- lives on its owner.
type sessionAgent struct {
	session *LLMSession

	// llm is the conversation as an immutable value: the last committed
	// snapshot while a runtime drives it, and the seed before one exists.
	llm   *dagger.LLM
	model string

	// name is the display label the roster shows. It carries no identity.
	name string

	// instanceID is the runtime's spawn-minted instance ID, learned when a
	// handle is bound. It is the roster's grouping key, so it is also how a
	// focus request coming back from the roster resolves to a conversation --
	// including the session's own, which must not be attached to twice.
	instanceID string

	// agent is the runtime handle backing turns: created lazily on the first
	// prompt submit (spawned from llm), or bound by attaching to an agent
	// this session did not spawn. agentL guards the handle and the identity
	// beside it, and is held only for field access -- spawnL serializes the
	// engine round-trips that mint one.
	agent  agentRuntime
	agentL sync.Mutex
	spawnL sync.Mutex

	// owned records whether this session spawned the runtime, and is the
	// only thing that licenses stopping it. Set where a handle enters the
	// conversation, read where one leaves (detachAgent), so clearing a
	// conversation you merely attached to can never kill somebody else's
	// worker.
	owned bool

	// attachedID is the encoded handle an attached conversation was rebuilt
	// from, kept so a re-attach recognises the same roster entry.
	attachedID string

	// turnCancel cancels the in-flight turn's await, non-nil only while
	// WithPrompt runs. It is per-conversation on purpose: Ctrl-C interrupts
	// the FOCUSED agent, which is not necessarily the one holding a turn.
	// pending buffers messages submitted while the turn was still opening,
	// before there was a runtime to send them to.
	turnCancel context.CancelCauseFunc
	turnDone   chan struct{}
	pending    []string
	turnL      sync.Mutex

	// refreshInFlight/refreshQueued coalesce UI refreshes triggered by step
	// boundaries (scheduleUIRefresh): a refresh makes several engine
	// round-trips, and an agent stepping faster than they complete must not
	// pile up goroutines -- at most one refresh runs, with at most one more
	// queued behind it. Guarded by refreshL.
	refreshInFlight bool
	refreshQueued   bool
	refreshL        sync.Mutex

	// stepWG tracks this conversation's asynchronous auto-saves. Rewind waits
	// for the canceled turn's save before exposing the edit, otherwise that older
	// save can finish after the reworded turn and overwrite its truncated history.
	stepWG sync.WaitGroup

	autoCompact  bool
	autoCompactL sync.Mutex

	// initialLLM is the base LLM to reset to on .clear, e.g. the workspace's
	// composed agent group as selected on startup (`dagger agent`). When nil,
	// .clear resets to a plain workspace-bound LLM. Its original workspace is
	// replaced with lastSyncedWorkspace on reset, preserving the composition
	// without resurrecting a stale checkpoint.
	initialLLM *dagger.LLM

	// lastSyncedWorkspace is the immutable workspace checkpoint this
	// conversation last explicitly synchronized with the host. Explicit
	// export/reset advances it, and reset/clear/session persistence reuse it.
	// UI refreshes run asynchronously, so every access is guarded by
	// lastSyncedWorkspaceL.
	lastSyncedWorkspace  *dagger.Workspace
	lastSyncedWorkspaceL sync.RWMutex

	// prevContextTokens is the cumulative prompt-token total (input + cache
	// reads + cache writes) observed after the previous turn, and
	// prevStepContext is that turn's own prompt size. Together they drive the
	// per-turn context growth shown in --debug mode (see reportContextUsage).
	prevContextTokens int
	prevStepContext   int

	// references tracks the host paths the user has attached with @ this
	// conversation (see attachReferences). They are mounted read-only in the
	// LLM's workspace, shown in the "References" sidebar, and dropped on
	// .clear. Paths already inside the workspace are not tracked here: they
	// are rewritten to workspace-relative paths instead of being mounted.
	references []referenceInfo

	// tunnels tracks the URLs the user has attached with @ this conversation
	// (see attachTunnel). Each runs as a container-to-host tunnel on the
	// session network, with the prompt token rewritten to its engine-side
	// address; they share the "References" sidebar and are likewise dropped
	// on .clear. The services themselves keep running: they are cached
	// per-session, so a sibling conversation may be relying on the same one.
	tunnels []tunnelInfo
}

// errAgentInterrupted is the cancellation cause a Ctrl-C on this conversation
// uses, distinguishing "the user preempted this turn" from a canceled session.
var errAgentInterrupted = errors.New("interrupted")

// errAgentRewound cancels the client-side await when inline editing abandons a
// turn. The shell suppresses it: unlike Ctrl-C, rewind is a successful control
// operation whose replacement text is about to appear in the input.
var errAgentRewound = errors.New("rewound for editing")

// agentInterruptGrace bounds how long an interrupt waits for the preempted
// step to land (the agent parks PAUSED once it does) before giving up and
// snapshotting whatever prefix is committed so far.
const (
	agentInterruptGrace = 15 * time.Second
	sessionTitleTimeout = 15 * time.Second
)

// GenerateSessionTitle asks a stripped, lightweight copy of the conversation's
// LLM to name the session from its first request. It deliberately excludes the
// conversation's history and system prompts, caps the loop to one short step,
// and never mutates the real conversation.
func (a *sessionAgent) GenerateSessionTitle(ctx context.Context, initialPrompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionTitleTimeout)
	defer cancel()

	titleLLM := a.llm.
		WithoutMessageHistory().
		WithoutSystemPrompts().
		WithoutDefaultSystemPrompt().
		WithSmallModel()

	prompt := fmt.Sprintf(`Create a concise title describing this Dagger agent session.
Use 3-7 words and no more than 60 characters. Return only the plain-text title,
with no quotation marks, label, explanation, or punctuation. Do not call tools.

<user-request>
%s
</user-request>`, initialPrompt)
	return titleLLM.
		WithSystemPrompt("You name coding-agent sessions. Follow the requested output format exactly.").
		WithPrompt(prompt).
		Loop(dagger.LLMLoopOpts{MaxSteps: 1, MaxTokens: 32}).
		LastReply(ctx)
}

func (s *LLMSession) newAgent(name string) *sessionAgent {
	return &sessionAgent{
		session:     s,
		name:        name,
		autoCompact: true,
	}
}

// Name is the conversation's display label.
func (a *sessionAgent) Name() string { return a.name }

// isTarget reports whether this conversation is the one the session's prompt
// currently addresses. The UI surfaces that describe a conversation -- status
// line, changes preview, references -- follow the target, so they refresh only
// from here.
func (a *sessionAgent) isTarget() bool {
	return a.session != nil && a.session.Target() == a
}

// uiActive reports whether this conversation should paint the
// conversation-scoped surfaces: it must be the target, and there must be a
// frontend to paint on (the routing policy is exercised without one).
func (a *sessionAgent) uiActive() bool {
	return a.isTarget() && a.session.frontend != nil
}

func (a *sessionAgent) ShouldAutocompact() bool {
	a.autoCompactL.Lock()
	defer a.autoCompactL.Unlock()
	return a.autoCompact
}

func (a *sessionAgent) ToggleAutocompact() {
	a.autoCompactL.Lock()
	a.autoCompact = !a.autoCompact
	a.autoCompactL.Unlock()
	// Refresh the status line so its "(auto)" tag reflects the new state.
	// Done after releasing autoCompactL, since updateStatusLine reads it back
	// via ShouldAutocompact.
	if a.llm != nil {
		if err := a.updateStatusLine(a.llm); err != nil {
			slog.Error("failed to update status line after toggling auto-compact", "error", err)
		}
	}
}

func (a *sessionAgent) lastSynced(fallback *dagger.Workspace) *dagger.Workspace {
	a.lastSyncedWorkspaceL.RLock()
	workspace := a.lastSyncedWorkspace
	a.lastSyncedWorkspaceL.RUnlock()
	if workspace != nil {
		return workspace
	}
	return fallback
}

func (a *sessionAgent) setLastSynced(workspace *dagger.Workspace) {
	a.lastSyncedWorkspaceL.Lock()
	a.lastSyncedWorkspace = workspace
	a.lastSyncedWorkspaceL.Unlock()
}

// setInitialLLM installs the composition selected when prompt mode starts and
// records its workspace as the conversation's first synchronization baseline.
// NewLLMSession first creates a temporary currentWorkspace-bound LLM; keeping
// this explicit prevents that temporary value from becoming an agent
// conversation's baseline.
func (a *sessionAgent) setInitialLLM(llm *dagger.LLM) error {
	a.initialLLM = llm
	workspace := llm.Workspace()
	if _, err := workspace.ID(a.session.plumbingCtx); err != nil {
		// Trace restore deliberately starts from an unbound base. It is still the
		// reset composition, but it cannot serve as a comparison baseline; the
		// restored snapshot (or a later explicit sync) supplies one instead.
		slog.Debug("starting LLM has no workspace synchronization baseline", "error", err)
		workspace = nil
	}
	return a.updateSyncedLLM(llm, workspace)
}

// updateSyncedLLM replaces the conversation and advances its synchronization
// baseline under the same lock used by asynchronous UI refreshes. The lock is
// held across updateLLM because it schedules a refresh before returning; that
// refresh must not race the baseline pointer update.
func (a *sessionAgent) updateSyncedLLM(llm *dagger.LLM, workspace *dagger.Workspace) error {
	a.lastSyncedWorkspaceL.Lock()
	defer a.lastSyncedWorkspaceL.Unlock()
	if err := a.updateLLM(llm); err != nil {
		return err
	}
	a.lastSyncedWorkspace = workspace
	return nil
}

func (a *sessionAgent) reset() {
	// Reset to the initially selected agent group (e.g. `dagger agent`), if
	// any, so .clear returns to those agents rather than a blank LLM. Preserve
	// the currently selected model, but bind the original composition to the
	// latest synchronized checkpoint rather than its original workspace.
	dag := a.session.dag
	baseline := a.lastSynced(nil)
	if baseline == nil && a.llm != nil {
		// Attached and trace-restored conversations may not carry their original
		// synchronization checkpoint. Their current portable workspace is the
		// safest fallback when it is bound: it cannot compare unlike host roots.
		candidate := a.llm.Workspace()
		if _, err := candidate.ID(a.session.plumbingCtx); err != nil {
			slog.Debug("current conversation has no workspace reset baseline", "error", err)
		} else {
			baseline = candidate
		}
	}
	if baseline == nil {
		// A truly unbound trace anchor has no checkpoint to recover. Keep .clear
		// usable by binding the destination workspace; the next explicit reset
		// replaces it with a portable checkpoint.
		baseline = dag.CurrentWorkspace()
	}
	var llm *dagger.LLM
	if a.initialLLM != nil {
		llm = a.initialLLM.WithWorkspace(baseline)
		if a.model != "" {
			llm = llm.WithModel(a.model)
		}
	} else {
		llm = dag.LLM(dagger.LLMOpts{Model: a.model}).
			WithWorkspace(baseline)
	}
	a.updateLLM(llm) //nolint:errcheck
}

// currentAgent returns the runtime backing this conversation's turns,
// spawning one from its LLM on first use. Spawn mints a unique instance per
// call -- the engine's guarantee that a fresh spawn can never resolve to an
// earlier agent's runtime (e.g. two .clear'd conversations with identical
// history) -- and returns the pinned instance ID, so later verbs re-load a
// compact reference instead of replaying the whole conversation chain.
//
// The engine round-trips run under spawnL rather than agentL: agentL is taken
// by the render path (the roster reads the target's instance ID on every
// frame), so holding it across a spawn would stall the UI for as long as the
// spawn takes.
func (a *sessionAgent) currentAgent(ctx context.Context) (agentRuntime, error) {
	a.spawnL.Lock()
	defer a.spawnL.Unlock()
	if rt := a.runtime(); rt != nil {
		return rt, nil
	}
	agentID, err := a.llm.Spawn(ctx, dagger.LLMSpawnOpts{Name: a.name})
	if err != nil {
		return nil, err
	}
	handle := dagger.Ref[*dagger.Agent](a.session.dag, agentID)
	// Learn the spawn-minted instance ID: it is what the roster keys on, so
	// without it a focus request naming this very agent would attach a second
	// conversation to the runtime this one already drives. Best-effort: an
	// engine older than the field still spawns and prompts perfectly well, it
	// just cannot be correlated with its roster entry, so this must never
	// fail a turn.
	instanceID, err := handle.InstanceID(ctx)
	if err != nil {
		slog.Debug("agent instance ID unavailable; roster correlation disabled", "error", err)
		instanceID = ""
	}
	rt := liveAgent{dag: a.session.dag, agent: handle}
	a.bindRuntime(rt, instanceID, "", true)
	return rt, nil
}

// runtime returns the conversation's runtime handle, or nil when it has none
// yet (never prompted, or dropped by a wholesale LLM replacement).
func (a *sessionAgent) runtime() agentRuntime {
	a.agentL.Lock()
	defer a.agentL.Unlock()
	return a.agent
}

// bindRuntime binds an already-existing runtime to this conversation, e.g. one
// attached to from the roster. owned records whether stopping it is this
// session's business.
func (a *sessionAgent) bindRuntime(rt agentRuntime, instanceID, attachedID string, owned bool) {
	a.agentL.Lock()
	defer a.agentL.Unlock()
	a.agent = rt
	a.instanceID = instanceID
	a.attachedID = attachedID
	a.owned = owned
}

// detachAgent unbinds the conversation's runtime, returning it only when this
// session spawned it. An attached runtime is somebody else's: forgetting it is
// the most this session may do.
func (a *sessionAgent) detachAgent() agentRuntime {
	a.agentL.Lock()
	defer a.agentL.Unlock()
	rt, owned := a.agent, a.owned
	a.agent = nil
	a.owned = false
	a.instanceID = ""
	a.attachedID = ""
	if !owned {
		return nil
	}
	return rt
}

// dropAgent detaches the conversation from its runtime, stopping it in the
// background when the session owns it (best-effort; the tombstone stays
// readable). This is the FALLBACK for a wholesale LLM replacement, not the
// rule: updateLLM reseeds a live owned runtime in place, and drops only when
// the swap is refused -- an attached runtime (somebody else's to reseed), a
// suspended turn, or an engine older than the verb. After a drop the next
// prompt submit spawns a fresh agent from the new value.
func (a *sessionAgent) dropAgent() {
	rt := a.detachAgent()
	if rt == nil {
		return
	}
	go func() {
		if err := rt.Stop(a.session.plumbingCtx); err != nil {
			slog.Debug("failed to stop replaced agent", "error", err)
		}
	}()
}

// beginTurn publishes the cancel func of the turn now in flight, making this
// conversation interruptible and its turn able to absorb submitted messages.
//
// It is called at the START of a submit, not when the await opens: sending
// the prompt takes engine round-trips (compaction, spawn, send), and a message
// typed during that window must join THIS turn rather than open a rival one --
// a rival submit would re-run attachReferences and auto-compaction, either of
// which can replace the LLM wholesale and so stop the runtime mid-turn.
func (a *sessionAgent) beginTurn(cancel context.CancelCauseFunc) {
	a.turnL.Lock()
	a.turnCancel = cancel
	a.turnDone = make(chan struct{})
	a.turnL.Unlock()
}

func (a *sessionAgent) endTurn() {
	a.turnL.Lock()
	a.turnCancel = nil
	if a.turnDone != nil {
		close(a.turnDone)
		a.turnDone = nil
	}
	pending := a.pending
	a.pending = nil
	a.turnL.Unlock()
	// Anything still buffered was accepted for a turn that never got to send
	// it (an error before the runtime existed). Report rather than drop: the
	// user was told it landed.
	for _, msg := range pending {
		slog.Warn("message could not be delivered to the agent", "message", msg)
	}
}

// Submit hands a message to this conversation's in-flight turn, reporting
// whether there was one. The send is fire-and-forget: the engine records it
// immediately (absorbing it into the running turn at the next step boundary --
// STEERED -- or queuing it behind a pause), and its reply arrives within the
// same turn's await, so there is nothing further to wait on here.
//
// A turn that is still opening has no runtime to send to yet, so the message
// is buffered and flushed by the submit that opened it -- accepted either
// way, since the alternative is opening a rival turn on the same conversation.
func (a *sessionAgent) Submit(msg string) bool {
	a.turnL.Lock()
	if a.turnCancel == nil {
		a.turnL.Unlock()
		return false
	}
	rt := a.runtime()
	if rt == nil {
		a.pending = append(a.pending, msg)
		a.turnL.Unlock()
		return true
	}
	a.turnL.Unlock()
	go a.send(rt, msg)
	return true
}

// send enqueues one message and logs how it landed.
func (a *sessionAgent) send(rt agentRuntime, msg string) {
	handle, err := rt.SendMessage(a.session.plumbingCtx, msg)
	if err != nil {
		slog.Error("failed to submit message to agent", "error", err)
		return
	}
	delivery, err := handle.Delivery(a.session.plumbingCtx)
	if err != nil {
		slog.Error("failed to read submitted message delivery", "error", err)
		return
	}
	slog.Debug("submitted mid-turn message", "delivery", delivery)
}

// flushPending sends the messages that arrived while the turn was still
// opening, in the order they were submitted and after the prompt that opened
// the turn.
func (a *sessionAgent) flushPending(rt agentRuntime) {
	a.turnL.Lock()
	pending := a.pending
	a.pending = nil
	a.turnL.Unlock()
	for _, msg := range pending {
		a.send(rt, msg)
	}
}

// Interrupt preempts this conversation, reporting whether there was anything
// to preempt. It is what Ctrl-C means with a roster: the FOCUSED agent stops,
// which is not necessarily the agent holding a turn.
//
// With a turn in flight, canceling its await is enough -- WithPrompt then
// issues the server-side interrupt and re-roots on the kept prefix. Without
// one the runtime may still be working on its own (an attached agent), so it
// is interrupted directly -- but only if it is actually busy: interrupt on an
// idle agent is equivalent to pause, and Ctrl-C to clear a half-typed line
// must not park somebody's worker.
func (a *sessionAgent) Interrupt() bool {
	a.turnL.Lock()
	cancel := a.turnCancel
	if cancel != nil {
		// Messages accepted while the turn was still spawning have not reached
		// the runtime. Abandon them with the turn instead of letting a racing
		// flush send stale input after Ctrl-C (or warning from endTurn as though
		// delivery failed unexpectedly).
		a.pending = nil
	}
	a.turnL.Unlock()
	if cancel != nil {
		cancel(errAgentInterrupted)
		return true
	}
	rt := a.runtime()
	if rt == nil {
		return false
	}
	go a.interruptIfBusy(rt)
	return true
}

// Rewind abandons this conversation's active branch and replaces it with the
// immutable LLM state immediately before the prompt being edited. A client turn
// is canceled and allowed to finish its normal interrupt/snapshot cleanup first;
// an attached runtime working independently is interrupted directly. An owned
// parked runtime is reseeded in place, preserving its agent instance; an
// attached runtime is detached rather than mutating somebody else's agent.
// Submitting the edited text therefore cannot join the old turn, and no
// background conversation is touched.
func (a *sessionAgent) Rewind(ctx context.Context, base *dagger.LLM) error {
	if err := a.rewindRuntime(ctx, base); err != nil {
		return err
	}
	return a.setLLM(base)
}

// rewindRuntime synchronizes with the client turn and swaps the engine runtime;
// split from Rewind so routing and turn-ordering policy can be tested without a
// live dagger client.
func (a *sessionAgent) rewindRuntime(ctx context.Context, base *dagger.LLM) error {
	a.turnL.Lock()
	cancel := a.turnCancel
	done := a.turnDone
	if cancel != nil {
		a.pending = nil
	}
	a.turnL.Unlock()

	a.agentL.Lock()
	rt, owned := a.agent, a.owned
	a.agentL.Unlock()
	if cancel != nil {
		cancel(errAgentRewound)
		if done != nil {
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-done:
			}
		}
	} else if rt != nil {
		state, err := rt.State(ctx)
		if err != nil {
			return err
		}
		switch state {
		case dagger.AgentStateRunning, dagger.AgentStateWaitingInput:
			if err := rt.Interrupt(ctx); err != nil {
				return err
			}
			if err := rt.WaitFor(ctx, dagger.AgentStatePaused); err != nil {
				return err
			}
		}
	}

	// WithPrompt schedules its auto-save before endTurn closes done. Waiting
	// here therefore drains every save from the abandoned branch before the
	// replacement prompt is exposed and can produce a newer save.
	a.stepWG.Wait()

	if rt != nil {
		if !owned {
			// Rewind creates a local branch from an attached agent; replacing the
			// attached runtime's conversation would mutate somebody else's worker.
			a.detachAgent()
			return nil
		}
		if err := rt.Reseed(ctx, base); err != nil {
			return err
		}
	}
	return nil
}

// interruptIfBusy preempts a runtime this session is not currently driving,
// after checking that it has work to preempt.
func (a *sessionAgent) interruptIfBusy(rt agentRuntime) {
	ctx, cancel := context.WithTimeout(
		context.WithoutCancel(a.session.plumbingCtx), agentInterruptGrace)
	defer cancel()
	state, err := rt.State(ctx)
	if err != nil {
		slog.Debug("could not read agent state for interrupt", "error", err)
		return
	}
	switch state {
	case dagger.AgentStateRunning, dagger.AgentStateWaitingInput:
		a.interruptAgent(rt)
	default:
		slog.Debug("nothing to interrupt", "state", state)
	}
}

// interruptAgent preempts the agent's in-flight step server-side. It runs on a
// fresh context (the turn's own context is already canceled by the time this
// is called from WithPrompt): Interrupt keeps every completed step and parks
// the agent PAUSED with the turn suspended -- the next prompt submit resumes
// it. The wait for PAUSED lets the canceled step land so the caller's snapshot
// reflects the final kept prefix (state projects RUNNING until then).
func (a *sessionAgent) interruptAgent(rt agentRuntime) {
	ctx, cancel := context.WithTimeout(
		context.WithoutCancel(a.session.plumbingCtx), agentInterruptGrace)
	defer cancel()
	if err := rt.Interrupt(ctx); err != nil {
		slog.Warn("failed to interrupt agent", "error", err)
		return
	}
	if err := rt.WaitFor(ctx, dagger.AgentStatePaused); err != nil {
		slog.Debug("interrupted agent did not park in time", "error", err)
	}
}

// syncFromAgent re-roots the conversation's LLM on the agent's last committed
// snapshot -- the honest, immutable conversation chain -- pinned by ID so the
// value stays put as the runtime advances. Unlike updateLLM this keeps the
// runtime handle: the snapshot is the agent's own progression, not a wholesale
// replacement.
func (a *sessionAgent) syncFromAgent(rt agentRuntime) error {
	snapID, err := rt.SnapshotID(a.session.plumbingCtx)
	if err != nil {
		return err
	}
	return a.setLLM(dagger.Ref[*dagger.LLM](a.session.dag, snapID))
}

// WithPrompt submits one prompt-mode message and blocks until the turn it
// opens (or joins) ends. The turn itself runs server-side in the Agent
// runtime: submit = send the message, resume the agent (a no-op unless a prior
// Ctrl-C parked it), then await the message's reply. Mid-turn submissions
// (Submit) and interrupts (Interrupt) act on that same runtime; when the turn
// ends the conversation's LLM is re-rooted on the agent's committed snapshot
// so history, /commands, and session saving keep operating on the honest
// chain.
func (a *sessionAgent) WithPrompt(ctx context.Context, input string) error {
	// The turn's context is this conversation's own, so an interrupt aimed at
	// this agent stops this turn and no other. It is published before any of
	// the work below: from here on the conversation is busy, so a message
	// typed while the prompt is still being sent joins this turn instead of
	// opening a rival one.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	a.beginTurn(cancel)
	defer a.endTurn()

	// Resolve any @-references in the prompt: paths already inside the
	// workspace are rewritten to workspace-relative paths, other paths are
	// mounted read-only in the workspace, and URLs are remapped to
	// container-to-host tunnels — with the prompt annotated with the
	// resulting workspace locations and addresses.
	input = a.attachReferences(a.session.plumbingCtx, input)

	resolvedModel, err := a.llm.Model(a.session.plumbingCtx)
	if err != nil {
		return err
	}
	a.model = resolvedModel

	// Check if we need to compact before adding the prompt.
	compacted, err := a.maybeAutoCompact(ctx)
	if err != nil {
		return fmt.Errorf("auto-compact: %w", err)
	}
	if compacted != a.llm {
		// Compaction rebuilt the conversation: that is a wholesale
		// replacement (different value digest), so rebase the conversation --
		// dropping any existing agent -- before packaging it as one below.
		if err := a.updateLLM(compacted); err != nil {
			return err
		}
	}

	rt, err := a.currentAgent(ctx)
	if err != nil {
		return err
	}

	// Enqueue the prompt on the record.
	msg, err := rt.SendMessage(ctx, input)
	if err != nil {
		return err
	}

	// Anything submitted while this turn was opening goes on the record
	// behind the prompt, in the order it was typed.
	a.flushPending(rt)

	// Un-park the agent: a no-op unless it is paused (a prior Ctrl-C
	// interrupt) or failed (resume retries), in which case the suspended turn
	// continues from its last committed step and the just-sent prompt drains
	// into it.
	if err := rt.Resume(a.session.plumbingCtx); err != nil {
		return err
	}

	// Block until the turn that consumed the prompt ends.
	_, awaitErr := msg.Await(ctx)

	if awaitErr != nil && ctx.Err() != nil {
		// The await was canceled -- by Ctrl-C on this agent, or by the
		// session going away. Interrupt the agent server-side, keeping the
		// completed prefix. The turn stays open with the prompt pending; the
		// next submit's Resume continues it.
		a.interruptAgent(rt)
		// Report the cancellation as what it was, not as the transport's
		// canceled POST: a Ctrl-C surfaces as "interrupted" above the
		// prompt, and a canceled session stays context.Canceled so the turn
		// span is marked canceled rather than failed.
		if cause := context.Cause(ctx); cause != nil {
			awaitErr = cause
		}
	}

	// Whether the turn ended with a reply, failed (the snapshot holds the
	// completed prefix; the next submit's Resume retries), or was
	// interrupted, re-root on the agent's committed snapshot so the rest of
	// the session reflects everything that actually happened.
	if err := a.syncFromAgent(rt); err != nil {
		if awaitErr != nil {
			slog.Warn("failed to sync LLM from agent snapshot", "error", err)
			return awaitErr
		}
		return err
	}

	// In --debug, surface how much this turn grew the context.
	a.reportContextUsage(ctx, a.llm)

	// Auto-save so the session is preserved even across interrupted turns.
	a.session.stepped(a)

	return awaitErr
}

// updateLLM replaces the conversation's LLM wholesale -- prompt-turn snapshots
// go through setLLM instead. The conversation remains THE SAME agent: a live
// owned runtime adopts the new conversation in place (Agent.reseed), keeping
// its identity, roster entry, and mailbox -- where stopping it and spawning a
// successor minted a STOPPED tombstone per replacement, splitting one
// conversation across identically-named roster entries. Dropping the runtime
// survives as the fallback when the swap is refused; the next prompt submit
// then packages the new value as a fresh agent.
func (a *sessionAgent) updateLLM(llm *dagger.LLM) error {
	if err := a.reseedAgent(llm); err != nil {
		slog.Debug("could not reseed the agent; dropping the runtime instead", "error", err)
		a.dropAgent()
	}
	return a.setLLM(llm)
}

// reseedAgent pushes the replacement conversation into the runtime backing
// this conversation, keeping the instance -- same identity, same roster
// entry, same transcript. Returns an error when the swap did not happen; the
// caller decides the fallback (updateLLM drops the runtime, restoring the
// old stop-and-respawn behavior). A conversation with no runtime reports
// success with nothing to do: the next prompt submit packages the new value
// as a fresh agent anyway.
//
// An attached runtime is refused here rather than reseeded: it is somebody
// else's agent (a module's worker, say), and replacing its conversation is
// not this session's call -- detaching is the most this session may do, the
// same ownership rule detachAgent applies to stopping.
func (a *sessionAgent) reseedAgent(llm *dagger.LLM) error {
	a.agentL.Lock()
	rt, owned := a.agent, a.owned
	a.agentL.Unlock()
	if rt == nil {
		return nil
	}
	if !owned {
		return fmt.Errorf("an attached runtime is somebody else's to reseed")
	}
	return rt.Reseed(a.session.plumbingCtx, llm)
}

func (a *sessionAgent) setLLM(llm *dagger.LLM) error {
	a.llm = llm

	// figure out what the model resolved to
	model, err := a.llm.Model(a.session.plumbingCtx)
	if err != nil {
		return err
	}
	a.model = model

	// Refresh the status line (and changes preview) so its token/cost/context
	// stats stay in sync with the LLM. Routing this through setLLM means
	// every operation that swaps the conversation's LLM -- prompt turns,
	// .clear, .compact, .model, branching, resuming -- keeps the status line
	// current without each call site having to remember to refresh it. The
	// repaint runs on the coalescing refresh goroutine: it costs a dozen
	// engine round-trips (the changes preview above all), and a turn that
	// paid for them synchronously kept the client's "working" indicator lit
	// well after the reply had landed.
	a.scheduleUIRefresh()
	return nil
}

// reportContextUsage emits a --debug span showing this turn's context size (the
// full prompt sent to the model) and how much it grew since the previous turn,
// so context spikes (e.g. a tool dumping a huge result) are visible between
// turns. LLM.TokenUsage is cumulative over the message history, so its change
// since the previous turn is this turn's own prompt growth. Compaction resets
// the history (WithoutMessageHistory), dropping the cumulative total; a drop is
// treated as a fresh baseline rather than negative growth.
func (a *sessionAgent) reportContextUsage(ctx context.Context, llm *dagger.LLM) {
	if !debugFlag {
		return
	}
	plumbing := a.session.plumbingCtx
	usage := llm.TokenUsage()
	input, err := usage.InputTokens(plumbing)
	if err != nil {
		return
	}
	cacheReads, err := usage.CachedTokenReads(plumbing)
	if err != nil {
		return
	}
	cacheWrites, err := usage.CachedTokenWrites(plumbing)
	if err != nil {
		return
	}

	cumulative := input + cacheReads + cacheWrites
	stepContext := cumulative - a.prevContextTokens
	if stepContext < 0 {
		// Compaction reset the cumulative total; this step is the new baseline.
		stepContext = cumulative
	}
	growth := stepContext - a.prevStepContext
	a.prevContextTokens = cumulative
	a.prevStepContext = stepContext

	_, span := Tracer().Start(ctx, fmt.Sprintf("context %s tokens (%s)",
		fmtTokenCount(stepContext), fmtTokenGrowth(growth)),
		telemetry.Reveal())
	span.End()
}

// updateStatusLine refreshes the compact status line. During a live turn the
// frontend recomputes the token rollup and cost from live metrics (all models +
// sub-agents) at render time, so they stay current between turns; here we supply
// the model, subscription label, auto-compact state, context occupancy, and a
// token/cost snapshot read from the LLM object itself. That snapshot is the
// fallback the frontend renders before any metrics arrive — most visibly on
// load/resume, where the conversation has usage but no live metrics yet.
//
// It describes ONE conversation, so it no-ops unless this conversation is the
// one the prompt is pointed at: a status line following a background agent
// would be a status line lying about what the user is typing into.
func (a *sessionAgent) updateStatusLine(llm *dagger.LLM) error {
	if !a.uiActive() {
		return nil
	}
	plumbing := a.session.plumbingCtx
	contextTokens, err := llm.ContextTokens(plumbing)
	if err != nil {
		return err
	}

	statusData := idtui.StatusLineData{
		Model:             a.model,
		SubscriptionLabel: a.session.subscriptionLabel(),
		ContextPercent:    -1, // unknown by default
		AutoCompact:       a.ShouldAutocompact(),
	}

	// Seed the cumulative token rollup and cost straight from the LLM object so
	// the status line is populated immediately on load/resume, before any new
	// metrics arrive. During a live turn the frontend overrides these with the
	// live metric rollup (all models + sub-agents); this is the fallback that
	// keeps a resumed conversation from rendering an empty bar. Best-effort:
	// stats aren't worth failing a turn over.
	usage := llm.TokenUsage()
	statusData.InputTokens, _ = usage.InputTokens(plumbing)
	statusData.OutputTokens, _ = usage.OutputTokens(plumbing)
	statusData.CacheReads, _ = usage.CachedTokenReads(plumbing)
	statusData.CacheWrites, _ = usage.CachedTokenWrites(plumbing)
	if provider, err := llm.Provider(plumbing); err == nil {
		statusData.TotalCost = modelcatalog.Cost(provider, a.model,
			int64(statusData.InputTokens), int64(statusData.OutputTokens),
			int64(statusData.CacheReads), int64(statusData.CacheWrites))
	}

	// The engine is the source of truth for the context window (backed by the
	// shared catwalk catalog); it reports 0 for uncatalogued/local models or an
	// older engine without the field.
	contextWindow, err := llm.ContextWindow(plumbing)
	if err != nil {
		contextWindow = 0
	}
	if contextWindow > 0 {
		statusData.ContextWindow = contextWindow
		if contextTokens > 0 {
			statusData.ContextPercent = float64(contextTokens) / float64(contextWindow) * 100
		}
	}
	a.session.frontend.SetStatusLine(statusData)

	// Best-effort: refresh the "Changes" preview from the workspace overlay diff.
	// Never fail a turn on a preview error (e.g. an unbound/rootless workspace).
	if err := a.updateChangesPreview(llm); err != nil {
		slog.Debug("could not refresh changes preview", "error", err)
	}

	return nil
}

// updateChangesPreview refreshes the "Changes" notification bubble with the
// workspace's Git-visible uncommitted changes, pending edits Git cannot see,
// and commits staged engine-side but not yet saved (newest first). Pressing
// ctrl+s exports all three to the local Git workspace (see ExportChanges). When
// there is nothing to show the bubble is cleared (an empty body renders nothing).
func (a *sessionAgent) updateChangesPreview(llm *dagger.LLM) error {
	if !a.uiActive() || llm == nil {
		return nil
	}
	workspace := llm.Workspace()
	// A genuinely unbound trace restore has no workspace state to preview. The
	// synchronization checkpoint is also our established bound/unbound marker;
	// skip the query rather than issuing one guaranteed to fail.
	if a.lastSynced(nil) == nil {
		a.session.frontend.SetSidebarContent(idtui.SidebarSection{Title: "Changes"})
		return nil
	}
	changes, err := idtui.PreviewWorkspaceChanges(a.session.plumbingCtx, a.session.dag, workspace)
	if err != nil {
		return err
	}
	if changes.Empty() {
		a.session.frontend.SetSidebarContent(idtui.SidebarSection{Title: "Changes"})
		return nil
	}
	a.session.frontend.SetSidebarContent(idtui.SidebarSection{
		Title: "Changes",
		ContentFunc: func(width int) string {
			var buf strings.Builder
			patchpreview.SummarizeChanges(idtui.NewOutput(&buf), changes.Uncommitted, changes.StagedCommits, width)
			return buf.String()
		},
		KeyMap: []key.Binding{
			key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "save")),
			key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "reset")),
		},
	})
	return nil
}

// refreshUI re-renders the conversation-scoped surfaces for a conversation
// that has just become the target: the status line, changes preview and
// reference list all describe one conversation, so they follow focus.
//
// The status line and changes preview are painted from the runtime's LATEST
// committed snapshot (scheduleUIRefresh), not from a.llm: a.llm is re-rooted
// at turn boundaries, so for an agent mid-turn it describes the world as of
// the last turn -- and a focus switch must show what the agent has done since,
// not wait for the user to interact with it.
func (a *sessionAgent) refreshUI() error {
	a.updateReferencesPreview()
	a.scheduleUIRefresh()
	return nil
}

// scheduleUIRefresh repaints the status line and changes preview from the
// agent's last committed snapshot, in the background. It is called on every
// step boundary the trace reports for this agent (LLMSession.AgentStepped) and
// on focus, so the surfaces track the conversation step by step instead of
// turn by turn.
//
// Refreshes coalesce: one runs at a time, and however many step boundaries
// land while it runs collapse into a single follow-up pass, which reads the
// newest snapshot anyway.
func (a *sessionAgent) scheduleUIRefresh() {
	if !a.uiActive() {
		return
	}
	a.refreshL.Lock()
	if a.refreshInFlight {
		a.refreshQueued = true
		a.refreshL.Unlock()
		return
	}
	a.refreshInFlight = true
	a.refreshL.Unlock()
	go func() {
		for {
			a.refreshUIFromRuntime()
			a.refreshL.Lock()
			if !a.refreshQueued {
				a.refreshInFlight = false
				a.refreshL.Unlock()
				return
			}
			a.refreshQueued = false
			a.refreshL.Unlock()
		}
	}()
}

// refreshUIFromRuntime paints the status line (and changes preview) from the
// runtime's last committed snapshot, falling back to the conversation's own
// LLM value when no runtime exists yet. It deliberately does NOT re-root
// a.llm: the conversation value moves at turn boundaries (WithPrompt's
// syncFromAgent) and on focus/attach, where its single-writer discipline
// holds; this path only feeds the UI.
func (a *sessionAgent) refreshUIFromRuntime() {
	if !a.uiActive() {
		return
	}
	llm := a.llm
	if rt := a.runtime(); rt != nil {
		snapID, err := rt.SnapshotID(a.session.plumbingCtx)
		if err != nil {
			slog.Debug("could not read agent snapshot for UI refresh", "error", err)
		} else {
			llm = dagger.Ref[*dagger.LLM](a.session.dag, snapID)
		}
	}
	if llm == nil {
		return
	}
	if err := a.updateStatusLine(llm); err != nil {
		slog.Debug("could not refresh status line", "error", err)
	}
}

// busy reports whether this conversation has a turn in flight. Operations
// that replace the LLM wholesale (export, reset) refuse while busy: replacing
// the value drops -- and stops -- the runtime mid-turn, which kills the very
// work the user is watching.
func (a *sessionAgent) busy() bool {
	a.turnL.Lock()
	defer a.turnL.Unlock()
	return a.turnCancel != nil
}

// ExportChanges writes the frozen workspace's pending overlay edits to the
// current client-local Git workspace by passing it as Workspace.export's
// explicit target, then refreshes the changes preview. It is the ctrl+s action;
// export fails clearly when the current workspace cannot persist (a remote ref,
// a synthetic workspace, or a local dir with no Git root).
func (a *sessionAgent) ExportChanges(ctx context.Context) error {
	if a.llm == nil {
		return fmt.Errorf("no LLM session active")
	}
	if a.busy() {
		return fmt.Errorf("agent is mid-turn; wait for it to finish (or interrupt with ctrl+c) before saving")
	}
	if err := a.llm.Workspace().Export(ctx, dagger.WorkspaceExportOpts{
		To: a.session.dag.CurrentWorkspace(),
	}); err != nil {
		return err
	}
	// The exported edits now live on disk, so rebind a workspace freshly frozen
	// from it: the overlay the agent accumulated is now redundant with the files
	// themselves, and carrying it forward would re-diff already-saved content
	// as pending changes. Rebinding also drops it from the next save —
	// portableID emits only the current binding. The rebinding is another
	// checkpoint rather than the live checkout so the conversation stays
	// portable across the save: the agent keeps working against a frozen tree,
	// and a restored trace does not reach for whatever is on the destination's
	// disk. Sync eagerly so a failure surfaces here rather than corrupting
	// later saves.
	frozen, err := checkpointWorkspace(ctx, a.session.dag)
	if err != nil {
		return fmt.Errorf("freeze workspace after export: %w", err)
	}
	rebound, err := a.llm.WithWorkspace(frozen).Sync(ctx)
	if err != nil {
		return fmt.Errorf("rebind workspace after export: %w", err)
	}
	if err := a.updateSyncedLLM(rebound, frozen); err != nil {
		return err
	}
	a.session.stepped(a)
	return a.updateChangesPreview(a.llm)
}

// ResetWorkspace discards the workspace's pending overlay edits, re-binding the
// LLM to a workspace freshly frozen from the host without exporting first.
// It is the ctrl+u action: conceptually the opposite direction of ctrl+s, it
// "uploads" the host's current state to the agent by throwing away the agent's
// accumulated changes rather than writing them out. Capture reads the checkout's
// live git state, so the new binding is whatever is on disk now — no cached host
// read from earlier in the session survives it, and the agent lands on a frozen
// tree rather than the live checkout. Sync eagerly so a failure surfaces here
// rather than corrupting later saves.
func (a *sessionAgent) ResetWorkspace(ctx context.Context) error {
	if a.llm == nil {
		return fmt.Errorf("no LLM session active")
	}
	if a.busy() {
		return fmt.Errorf("agent is mid-turn; wait for it to finish (or interrupt with ctrl+c) before resetting")
	}
	frozen, err := checkpointWorkspace(ctx, a.session.dag)
	if err != nil {
		return fmt.Errorf("freeze workspace: %w", err)
	}
	reset, err := a.llm.WithWorkspace(frozen).Sync(ctx)
	if err != nil {
		return fmt.Errorf("reset workspace: %w", err)
	}
	if err := a.updateSyncedLLM(reset, frozen); err != nil {
		return err
	}
	a.session.stepped(a)
	return a.updateChangesPreview(a.llm)
}

const autoCompactReserveTokens = 16_384

// maybeAutoCompact checks whether the current context is inside the response
// reserve and automatically compacts if so.
func (a *sessionAgent) maybeAutoCompact(ctx context.Context) (_ *dagger.LLM, rerr error) {
	if !a.ShouldAutocompact() {
		return a.llm, nil
	}

	contextTokens, err := a.llm.ContextTokens(a.session.plumbingCtx)
	if err != nil {
		return nil, err
	}

	// The engine reports the model's context window (shared catwalk catalog);
	// 0 means uncatalogued/local, so we can't determine a threshold — skip.
	contextWindow, err := a.llm.ContextWindow(a.session.plumbingCtx)
	if err != nil || contextWindow <= 0 {
		return a.llm, nil
	}

	threshold := contextWindow - autoCompactReserveTokens
	if threshold <= 0 {
		threshold = int(float64(contextWindow) * 0.80)
	}

	if contextTokens > threshold {
		ctx, span := Tracer().Start(ctx, "auto-compacting LLM history", telemetry.Reveal())
		defer telemetry.EndWithCause(span, &rerr)
		return a.Compact(ctx)
	}

	return a.llm, nil
}

// Clear resets the conversation to its base LLM, dropping history and
// references.
//
// Undo remains disabled, but not for the original reason: interrupts used to
// be client-side context cancels that threw away a turn's partial progress
// (see https://github.com/dagger/dagger/pull/10765), which made a
// forked-session undo stack do more harm than good. Interrupts now happen
// server-side (Agent.interrupt) and preserve the completed prefix, so that
// rationale is gone -- re-enabling undo is a deliberate follow-up, not blocked
// on interrupt semantics anymore.
func (a *sessionAgent) Clear() {
	a.reset()
	a.references = nil
	a.tunnels = nil
	a.updateReferencesPreview()
}

func (a *sessionAgent) Compact(ctx context.Context) (_ *dagger.LLM, rerr error) {
	ctx, span := Tracer().Start(ctx, "compact", telemetry.Internal(), telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)

	compactedPrompt, err := a.llm.
		WithoutSystemPrompts().
		WithSystemPrompt("You are a helpful AI assistant tasked with summarizing conversations.").
		WithPrompt(compactPrompt).
		LastReply(ctx)
	if err != nil {
		return nil, err
	}

	return a.llm.
		WithoutMessageHistory().
		WithPrompt(fmt.Sprintf(
			"This session is being continued from a previous conversation that ran out of context. The conversation is summarized below:\n\n%s",
			compactedPrompt,
		)), nil
}

func (a *sessionAgent) History(ctx context.Context) error {
	transcript, err := a.llm.Transcript(ctx)
	if err != nil {
		return err
	}
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	fmt.Fprintln(stdio.Stdout, transcript)
	return nil
}

func (a *sessionAgent) Model(model string) error {
	if err := a.updateLLM(a.llm.WithModel(model)); err != nil {
		return err
	}
	resolved, err := a.llm.Model(a.session.plumbingCtx)
	if err != nil {
		return err
	}
	a.model = resolved
	return nil
}

// Effort changes the reasoning effort for the rest of the conversation,
// overriding any provider-configured default. "none" disables reasoning.
func (a *sessionAgent) Effort(effort string) error {
	if err := a.updateLLM(a.llm.WithReasoningEffort(effort)); err != nil {
		return err
	}
	// Resolve the endpoint eagerly so a configuration problem surfaces now
	// rather than on the next prompt, mirroring Model above.
	if _, err := a.llm.ReasoningEffort(a.session.plumbingCtx); err != nil {
		return err
	}
	return nil
}

// BranchSummary generates a summary of the current conversation branch. It is
// used when branching to describe what was explored in the branch being
// abandoned, so the summary can be injected at the branch target.
//
// The conversation is serialized to plain text first (so the model treats it
// as data to summarize, not a conversation to continue), then passed to a
// fresh lightweight LLM call with a small output budget. If customInstructions
// is non-empty it is appended to the default prompt.
func (a *sessionAgent) BranchSummary(ctx context.Context, customInstructions string) (_ string, rerr error) {
	ctx, span := Tracer().Start(ctx, "branch summary", telemetry.Internal(), telemetry.Encapsulate())
	defer telemetry.EndWithCause(span, &rerr)

	conversationText, err := a.llm.Transcript(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to serialize history: %w", err)
	}
	// Budget the input to the model's actual context window; unknown models
	// (e.g. local endpoints) report null (decoded as 0) and get a
	// conservative fallback.
	contextWindow, err := a.llm.ContextWindow(ctx)
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
	summaryText, err := a.llm.
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
