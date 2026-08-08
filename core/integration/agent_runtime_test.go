package core

// These tests cover the async Agent API (hack/designs/async-agents.md): the
// evaluation loop as an addressable, long-lived entity within the session.
// They exercise the lifecycle verbs (start/pause/resume/interrupt/stop), the
// mailbox (send/await, delivery evidence, queueing behind a pause), failure
// and retry semantics, and runtime dedupe by agent value digest.
//
// Like llm_test.go, every conversation is canned: recordings are constructed
// through the LLM API itself and replayed via a replay/ model (see
// cannedReplayModel), so no LLM API keys are needed and the replayer runs the
// REAL tools for recorded tool calls. The mid-turn tests exploit that: a
// recorded tool call to a slow container exec makes the turn genuinely dwell
// in a step, and a shared cache volume gives the test a deterministic signal
// that the tool is in flight (no sleeps as synchronization).
//
// One deliberate infrastructure note: agents are spawned instances —
// LLM.spawn is DoNotCache and mints a unique instance ID per call, pinning
// it by re-exec through the pure LLM.agent(id:) lookup (design §9), so the
// returned ID is an honest, replayable chain `…llm!agent(id:"…")` denoting
// exactly one instance. The helpers here spawn once and re-address the
// handle via node(id:) for every subsequent query; two spawns of an
// identical composition are two distinct agents (TestSpawnInstances).
// Agent.send follows the same pattern one level down: it pins each
// enqueued message through Agent.message(id:) — the chain
// `…agent(id:…)!message(id:"…")` — which is what makes the
// cancel-and-re-await contract hold across requests (TestMessageIdentity).
// Like every imperative verb (start, pause, resume, interrupt, waitFor,
// stop), spawn and send are ID-returning, sync-style: lazy clients force
// the side effect at the call site, and re-hydrating the returned ID
// replays the lookup, not the spawn/send.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"golang.org/x/sync/errgroup"
)

type AgentRuntimeSuite struct{}

func TestAgentRuntime(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(AgentRuntimeSuite{})
}

// emptyReplayModel is a replay/ model with an empty recording: any model call
// fails immediately ("no more messages"), so it seeds agents that must never
// call the model at all.
var emptyReplayModel = "replay/" + base64.StdEncoding.EncodeToString([]byte("[]"))

// agentHandle drives one spawned agent instance through raw GraphQL queries.
// Raw queries are used (rather than the typed SDK) because the interesting
// assertions select several fields off a single loaded node in one query —
// e.g. { delivery await }, or the two aliased awaits of the idempotency
// test. The handle holds the pinned agent ID a spawn returned; every query
// re-addresses the same instance via node(id:), which replays the pure
// …llm!agent(id:…) lookup — never the spawn.
type agentHandle struct {
	c *dagger.Client
	// agentID is the pinned instance ID returned by spawn.
	agentID string
}

// spawnOpts configures the composition an agent is spawned from.
type spawnOpts struct {
	model string
	// name is the display label passed to spawn (optional).
	name string
	// toolIDs optionally binds objects' methods as tools (one llm.withTools
	// per object, in order), for the recordings that contain tool calls.
	toolIDs []dagger.ID
	// wsID optionally binds a workspace (llm.withWorkspace) ahead of the
	// tool bindings, so tools that auto-inject a Workspace! argument resolve
	// it from the LLM instead of the session's ambient currentWorkspace —
	// which under the test harness is whatever the session inherited, not
	// something the test controls.
	wsID dagger.ID
}

// trySpawnAgent evaluates llm[.withWorkspace][.withTools…].spawn(name:) once
// and returns the pinned instance ID. Error-returning so tests can spawn
// from helper goroutines; most call spawnAgent.
func trySpawnAgent(ctx context.Context, c *dagger.Client, opts spawnOpts) (string, error) {
	vars := map[string]any{
		"model": opts.model,
	}
	decls := []string{"$model: String!"}
	inner := `spawn`
	if opts.name != "" {
		inner = `spawn(name: $name)`
		decls = append(decls, "$name: String!")
		vars["name"] = opts.name
	}
	path := "spawn"
	for i := len(opts.toolIDs) - 1; i >= 0; i-- {
		v := fmt.Sprintf("tool%d", i)
		inner = fmt.Sprintf(`withTools(object: $%s) { %s }`, v, inner)
		decls = append(decls, fmt.Sprintf("$%s: ID!", v))
		vars[v] = string(opts.toolIDs[i])
		path = "withTools." + path
	}
	if opts.wsID != "" {
		inner = fmt.Sprintf(`withWorkspace(workspace: $ws) { %s }`, inner)
		decls = append(decls, "$ws: ID!")
		vars["ws"] = string(opts.wsID)
		path = "withWorkspace." + path
	}
	root := "llm." + path
	query := fmt.Sprintf(`query(%s) { llm(model: $model) { %s } }`,
		strings.Join(decls, ", "), inner)
	res := map[string]any{}
	if err := c.Do(ctx,
		&dagger.Request{Query: query, Variables: vars},
		&dagger.Response{Data: &res},
	); err != nil {
		return "", err
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return "", err
	}
	out := gjson.Get(string(raw), root)
	if !out.Exists() || out.String() == "" {
		return "", fmt.Errorf("spawned agent ID missing in response: %s", raw)
	}
	return out.String(), nil
}

// spawnAgent spawns one agent instance and returns its handle.
func spawnAgent(ctx context.Context, t *testctx.T, c *dagger.Client, opts spawnOpts) *agentHandle {
	t.Helper()
	id, err := trySpawnAgent(ctx, c, opts)
	require.NoError(t, err)
	return &agentHandle{c: c, agentID: id}
}

// run executes a query with the given selection on the spawned agent —
// re-addressed via node(id:) — and returns the JSON subtree rooted at the
// node. Error-returning (no require) so it is safe to call from helper
// goroutines.
func (h *agentHandle) run(ctx context.Context, t *testctx.T, selection string) (gjson.Result, error) {
	t.Helper()
	res := map[string]any{}
	if err := h.c.Do(ctx,
		&dagger.Request{
			Query: fmt.Sprintf(
				`query($id: ID!) { node(id: $id) { ... on Agent { %s } } }`,
				selection),
			Variables: map[string]any{"id": h.agentID},
		},
		&dagger.Response{Data: &res},
	); err != nil {
		return gjson.Result{}, err
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return gjson.Result{}, err
	}
	out := gjson.Get(string(raw), "node")
	if !out.Exists() {
		return gjson.Result{}, fmt.Errorf("agent selection missing in response: %s", raw)
	}
	return out, nil
}

func (h *agentHandle) mustRun(ctx context.Context, t *testctx.T, selection string) gjson.Result {
	t.Helper()
	out, err := h.run(ctx, t, selection)
	require.NoError(t, err)
	return out
}

// msgRun loads a pinned message handle by its ID — node(id:) replays the
// …agent(id:…)!message(id:…) chain — and runs the given selection on it,
// returning the JSON subtree rooted at the node.
func (h *agentHandle) msgRun(ctx context.Context, t *testctx.T, msgID, selection string) (gjson.Result, error) {
	t.Helper()
	res := map[string]any{}
	if err := h.c.Do(ctx,
		&dagger.Request{
			Query: fmt.Sprintf(
				`query($id: ID!) { node(id: $id) { ... on AgentMessage { %s } } }`,
				selection),
			Variables: map[string]any{"id": msgID},
		},
		&dagger.Response{Data: &res},
	); err != nil {
		return gjson.Result{}, err
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return gjson.Result{}, err
	}
	out := gjson.Get(string(raw), "node")
	if !out.Exists() {
		return gjson.Result{}, fmt.Errorf("message selection missing in response: %s", raw)
	}
	return out, nil
}

// sendID enqueues a message and returns the pinned message ID. send is
// ID-returning (sync-style): the enqueue happens here, exactly once, and the
// returned ID replays the message(id:) lookup — not the send — when loaded.
func (h *agentHandle) sendID(ctx context.Context, t *testctx.T, message string) (string, error) {
	t.Helper()
	out, err := h.run(ctx, t, fmt.Sprintf(`send(message: %q)`, message))
	if err != nil {
		return "", err
	}
	return out.Get("send").String(), nil
}

// sendAndWait enqueues a message and blocks until the turn that consumed it
// ends, returning the enqueue-time delivery evidence and the turn's reply.
// Both are selected off the loaded message handle, in one query.
func (h *agentHandle) sendAndWait(ctx context.Context, t *testctx.T, message string) (delivery, reply string, _ error) {
	t.Helper()
	msgID, err := h.sendID(ctx, t, message)
	if err != nil {
		return "", "", err
	}
	out, err := h.msgRun(ctx, t, msgID, `delivery await`)
	if err != nil {
		return "", "", err
	}
	return out.Get("delivery").String(), out.Get("await").String(), nil
}

// sendNoWait enqueues a message and returns only its delivery evidence —
// send never blocks, so this returns immediately whatever state the agent is
// in.
func (h *agentHandle) sendNoWait(ctx context.Context, t *testctx.T, message string) (string, error) {
	t.Helper()
	msgID, err := h.sendID(ctx, t, message)
	if err != nil {
		return "", err
	}
	out, err := h.msgRun(ctx, t, msgID, `delivery`)
	if err != nil {
		return "", err
	}
	return out.Get("delivery").String(), nil
}

func (h *agentHandle) stateErr(ctx context.Context, t *testctx.T) (string, error) {
	t.Helper()
	out, err := h.run(ctx, t, `state`)
	if err != nil {
		return "", err
	}
	return out.Get("state").String(), nil
}

func (h *agentHandle) state(ctx context.Context, t *testctx.T) string {
	t.Helper()
	state, err := h.stateErr(ctx, t)
	require.NoError(t, err)
	return state
}

// verb runs a lifecycle verb (start, pause, resume, interrupt, stop) and
// returns the agent's state read just after it. The verbs are ID-returning
// (sync-style) leaves, so the state is a follow-up query rather than a
// sub-selection — safe because each verb returns only once its transition
// has landed.
func (h *agentHandle) verb(ctx context.Context, t *testctx.T, verb string) (string, error) {
	t.Helper()
	if _, err := h.run(ctx, t, verb); err != nil {
		return "", err
	}
	return h.stateErr(ctx, t)
}

func (h *agentHandle) mustVerb(ctx context.Context, t *testctx.T, verb string) string {
	t.Helper()
	state, err := h.verb(ctx, t, verb)
	require.NoError(t, err)
	return state
}

// waitFor blocks until the agent reaches the given state, returning the
// state read just after (or an error if the state became unreachable).
func (h *agentHandle) waitFor(ctx context.Context, t *testctx.T, state string) (string, error) {
	t.Helper()
	if _, err := h.run(ctx, t, fmt.Sprintf(`waitFor(state: %s)`, state)); err != nil {
		return "", err
	}
	return h.stateErr(ctx, t)
}

// snapshot returns the last committed conversation's transcript and last
// reply.
func (h *agentHandle) snapshot(ctx context.Context, t *testctx.T) (transcript, lastReply string) {
	t.Helper()
	out := h.mustRun(ctx, t, `snapshot { transcript lastReply }`)
	return out.Get("snapshot.transcript").String(), out.Get("snapshot.lastReply").String()
}

// slowToolContainer returns a container whose stdout tool dwells: dispatching
// it touches a marker on the shared cache volume (the test's synchronization
// signal that the turn is inside the tool call), sleeps, then prints. The
// cache buster keeps the exec out of the cache so the dwell really happens on
// every dispatch — in particular a canceled exec is not cached, so an
// interrupted run re-dwells when resume re-steps it.
func slowToolContainer(c *dagger.Client, vol *dagger.CacheVolume, sleepSecs int) *dagger.Container {
	return c.Container().From(alpineImage).
		WithMountedCache("/sync", vol).
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		WithExec([]string{"sh", "-c",
			fmt.Sprintf("touch /sync/started && sleep %d && echo TOOL-DONE", sleepSecs)})
}

// waitForSlowTool blocks until the slow tool's marker file appears on the
// shared cache volume: from then on, and until the tool's sleep elapses, the
// agent's turn is provably dwelling inside the tool call.
func waitForSlowTool(ctx context.Context, t *testctx.T, c *dagger.Client, vol *dagger.CacheVolume) {
	t.Helper()
	_, err := c.Container().From(alpineImage).
		WithMountedCache("/sync", vol).
		WithEnvVariable("CACHEBUSTER", identity.NewID()).
		WithExec([]string{"sh", "-c",
			"for i in $(seq 1 600); do [ -f /sync/started ] && exit 0; sleep 0.1; done; echo 'slow tool never started'; exit 1"}).
		Sync(ctx)
	require.NoError(t, err)
}

// slowToolRecording is the canned conversation for the mid-turn tests: one
// prompt, a recorded tool call to the bound container's stdout (which the
// replayer really dispatches, so the turn dwells in the exec), and — after
// optional extra exchanges — a closing reply.
const (
	slowToolPrompt = "start the slow work"
	slowToolReply  = "the turn is done"
	steerPrompt    = "steer the running turn"
)

func slowToolConversation(c *dagger.Client, withSteer bool) *dagger.LLM {
	llm := c.LLM().
		WithPrompt(slowToolPrompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Dispatching the slow tool."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "stdout"},
		}).
		// Placeholder result: the real exec runs during replay and its live
		// output flows through (tool results are excluded from the
		// replayer's history matching).
		WithToolResult("call_1", "", false)
	if withSteer {
		llm = llm.WithPrompt(steerPrompt)
	}
	return llm.WithResponse([]dagger.LLMContentBlockInput{
		{Kind: dagger.LLMContentBlockKindText, Text: slowToolReply},
	})
}

// TestLifecycle covers the value/runtime split: spawn naming, idempotent
// start, an empty seed idling without any model call, stop tombstones, and
// waitFor erroring (rather than hanging) once the wanted state is
// unreachable.
func (AgentRuntimeSuite) TestLifecycle(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// spawn with an explicit display name, and with the name derived from
	// the seed conversation's recipe digest. The typed SDK path: spawn
	// executes eagerly (ID-returning), and the ID re-hydrates into the
	// agent handle.
	namedID, err := c.LLM(dagger.LLMOpts{Model: emptyReplayModel}).
		Spawn(ctx, dagger.LLMSpawnOpts{Name: "bob"})
	require.NoError(t, err)
	name, err := dagger.Ref[*dagger.Agent](c, namedID).Name(ctx)
	require.NoError(t, err)
	require.Equal(t, "bob", name)

	derivedID, err := c.LLM(dagger.LLMOpts{Model: emptyReplayModel}).Spawn(ctx)
	require.NoError(t, err)
	derived, err := dagger.Ref[*dagger.Agent](c, derivedID).Name(ctx)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(derived, "agent-"), "derived name %q", derived)

	h := spawnAgent(ctx, t, c, spawnOpts{model: emptyReplayModel, name: "lifecycle"})

	// start is idempotent, and an empty seed (no pending prompt) idles.
	require.Equal(t, "IDLE", h.mustVerb(ctx, t, "start"))
	require.Equal(t, "IDLE", h.mustVerb(ctx, t, "start"))

	// waitFor(IDLE) returns immediately when already there.
	state, err := h.waitFor(ctx, t, "IDLE")
	require.NoError(t, err)
	require.Equal(t, "IDLE", state)

	// Bounded observation window (not synchronization): the replay model's
	// recording is empty, so any model call would fail the loop within
	// milliseconds. Observing IDLE throughout proves the empty-seed agent
	// never called the model (it would otherwise project RUNNING and then
	// FAILED).
	deadline := time.Now().Add(1200 * time.Millisecond)
	for time.Now().Before(deadline) {
		require.Equal(t, "IDLE", h.state(ctx, t))
		time.Sleep(100 * time.Millisecond)
	}

	// The snapshot is still the seed: no reply was ever produced.
	_, lastReply := h.snapshot(ctx, t)
	require.Equal(t, "(no reply)", lastReply)

	// stop tombstones the runtime; waitFor(STOPPED) observes it; stopping a
	// tombstone is idempotent.
	require.Equal(t, "STOPPED", h.mustVerb(ctx, t, "stop"))
	state, err = h.waitFor(ctx, t, "STOPPED")
	require.NoError(t, err)
	require.Equal(t, "STOPPED", state)
	require.Equal(t, "STOPPED", h.mustVerb(ctx, t, "stop"))

	// STOPPED is terminal: waiting for any other state errors rather than
	// hanging.
	_, err = h.waitFor(ctx, t, "RUNNING")
	require.ErrorContains(t, err, "unreachable")
}

// TestSendAwait covers the mailbox happy path: send opens a turn (STARTED),
// await returns that turn's reply, the consumed message appears in the
// snapshot's history (influence ⇔ append), and two sequential turns against
// one agent correlate their replies correctly.
func (AgentRuntimeSuite) TestSendAwait(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const (
		firstPrompt  = "first prompt for the agent"
		firstReply   = "the first recorded reply"
		secondPrompt = "second prompt for the agent"
		secondReply  = "the second recorded reply"
	)
	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt(firstPrompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: firstReply},
		}).
		WithPrompt(secondPrompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: secondReply},
		}))

	h := spawnAgent(ctx, t, c, spawnOpts{model: model, name: "conversationalist"})

	// First turn: signal-with-start (no explicit start call), STARTED
	// delivery, and the recorded reply.
	delivery, reply, err := h.sendAndWait(ctx, t, firstPrompt)
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)
	require.Equal(t, firstReply, reply)

	// The consumed message was appended to the agent's history — a message
	// that influenced the agent appears in its transcript (influence ⇔
	// append) — and the snapshot's lastReply matches what await returned.
	transcript, lastReply := h.snapshot(ctx, t)
	require.Contains(t, transcript, firstPrompt)
	require.Contains(t, transcript, firstReply)
	require.Equal(t, firstReply, lastReply)

	// Second turn against the same agent: the reply correlates with the
	// message that opened it, not with the first turn's.
	delivery, reply, err = h.sendAndWait(ctx, t, secondPrompt)
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)
	require.Equal(t, secondReply, reply)

	transcript, lastReply = h.snapshot(ctx, t)
	require.Contains(t, transcript, secondPrompt)
	require.Contains(t, transcript, secondReply)
	require.Contains(t, transcript, firstPrompt) // history accumulates
	require.Equal(t, secondReply, lastReply)
}

// TestSpawnInstances locks in the instance semantics of spawn — the
// deliberate inversion of the old value-dedupe contract: two spawns of an
// IDENTICAL composition (same seed, same display name) are two distinct
// agents, with distinct pinned IDs and independent runtimes. Both dwell in
// the same recorded slow tool call concurrently — two RUNNING instances
// under one display name — and each turn resolves against its own runtime.
func (AgentRuntimeSuite) TestSpawnInstances(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	vol := c.CacheVolume("agent-spawn-" + identity.NewID())
	ctrID, err := slowToolContainer(c, vol, 6).ID(ctx)
	require.NoError(t, err)
	model := cannedReplayModel(ctx, t, c, slowToolConversation(c, false))

	// Two spawns of the exact same composition: same model, same tool
	// binding, same display name. Under the old identity model these
	// resolved to one runtime entry by content digest; spawn mints a
	// unique instance ID into each pinned chain, so they are two agents.
	opts := spawnOpts{model: model, name: "twin", toolIDs: []dagger.ID{ctrID}}
	first := spawnAgent(ctx, t, c, opts)
	second := spawnAgent(ctx, t, c, opts)
	require.NotEqual(t, first.agentID, second.agentID,
		"two spawns of an identical composition must mint distinct instances")

	// Same display name — a label, not an identity.
	require.Equal(t, "twin", first.mustRun(ctx, t, `name`).Get("name").String())
	require.Equal(t, "twin", second.mustRun(ctx, t, `name`).Get("name").String())

	// Open a turn on the first instance only. The second's runtime is
	// independent: it stays IDLE on the bare seed while the first dwells
	// in the tool call.
	var firstDelivery, firstReply string
	eg := errgroup.Group{}
	eg.Go(func() error {
		var err error
		firstDelivery, firstReply, err = first.sendAndWait(ctx, t, slowToolPrompt)
		return err
	})
	waitForSlowTool(ctx, t, c, vol)
	require.Equal(t, "RUNNING", first.state(ctx, t))
	require.Equal(t, "IDLE", second.state(ctx, t))
	_, secondLastReply := second.snapshot(ctx, t)
	require.Equal(t, "(no reply)", secondLastReply)

	// Now open the second instance's own turn: both dwell in the (shared,
	// deduped) exec concurrently — two RUNNING agents under one display
	// name — and each await resolves against its own runtime.
	var secondDelivery, secondReply string
	eg.Go(func() error {
		var err error
		secondDelivery, secondReply, err = second.sendAndWait(ctx, t, slowToolPrompt)
		return err
	})
	require.Eventually(t, func() bool {
		return second.state(ctx, t) == "RUNNING"
	}, 10*time.Second, 100*time.Millisecond)
	require.Equal(t, "RUNNING", first.state(ctx, t))

	require.NoError(t, eg.Wait())
	require.Equal(t, "STARTED", firstDelivery)
	require.Equal(t, slowToolReply, firstReply)
	// The second message opened its OWN turn (STARTED, not STEERED): it
	// enqueued into a distinct runtime, not the first's in-flight turn.
	require.Equal(t, "STARTED", secondDelivery)
	require.Equal(t, slowToolReply, secondReply)

	// Both histories carry their own consumed prompt.
	firstTranscript, _ := first.snapshot(ctx, t)
	secondTranscript, _ := second.snapshot(ctx, t)
	require.Contains(t, firstTranscript, slowToolPrompt)
	require.Contains(t, secondTranscript, slowToolPrompt)
}

// TestSpawnAfterStop covers the dismiss-and-rehire flow that motivated the
// spawn pivot: stop an instance, spawn the same composition (same display
// name) again, and the successor works — under identity-by-composition the
// second spawn resolved to the predecessor's STOPPED tombstone and the
// opening send failed. The predecessor's tombstone stays readable via its
// held ID, and its resolved message IDs still await idempotently.
func (AgentRuntimeSuite) TestSpawnAfterStop(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const (
		prompt = "prompt for the phoenix"
		reply  = "the recorded phoenix reply"
	)
	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt(prompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: reply},
		}))
	opts := spawnOpts{model: model, name: "phoenix"}

	// First incarnation: run a turn, keep the message ID, stop.
	first := spawnAgent(ctx, t, c, opts)
	firstMsgID, err := first.sendID(ctx, t, prompt)
	require.NoError(t, err)
	out, err := first.msgRun(ctx, t, firstMsgID, `await`)
	require.NoError(t, err)
	require.Equal(t, reply, out.Get("await").String())
	require.Equal(t, "STOPPED", first.mustVerb(ctx, t, "stop"))

	// Second incarnation of the identical composition: a fresh instance
	// with a fresh runtime — its opening send works. (The recording
	// replays from the top for the new runtime's bare seed.)
	second := spawnAgent(ctx, t, c, opts)
	require.NotEqual(t, first.agentID, second.agentID)
	delivery, gotReply, err := second.sendAndWait(ctx, t, prompt)
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)
	require.Equal(t, reply, gotReply)

	// The predecessor is untouched by its successor: still a readable
	// STOPPED tombstone via the held ID, snapshot intact...
	require.Equal(t, "STOPPED", first.state(ctx, t))
	transcript, lastReply := first.snapshot(ctx, t)
	require.Contains(t, transcript, prompt)
	require.Equal(t, reply, lastReply)
	// ...its old message IDs still awaiting idempotently...
	out, err = first.msgRun(ctx, t, firstMsgID, `await`)
	require.NoError(t, err)
	require.Equal(t, reply, out.Get("await").String())
	// ...and still terminally stopped: sends keep failing.
	_, err = first.sendNoWait(ctx, t, "mail for a corpse")
	require.ErrorContains(t, err, "stopped")
}

// TestPauseQueueResume covers the mailbox behind a pause: QUEUED delivery
// evidence, the state staying PAUSED across sends, and resume draining the
// queue — for an already-running agent and for one paused before it ever
// started.
func (AgentRuntimeSuite) TestPauseQueueResume(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const (
		prompt = "queued prompt"
		reply  = "the resumed reply"
	)
	newModel := func() string {
		return cannedReplayModel(ctx, t, c, c.LLM().
			WithPrompt(prompt).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: reply},
			}))
	}

	t.Run("started agent", func(ctx context.Context, t *testctx.T) {
		h := spawnAgent(ctx, t, c, spawnOpts{model: newModel(), name: "pausable"})

		require.Equal(t, "IDLE", h.mustVerb(ctx, t, "start"))
		require.Equal(t, "PAUSED", h.mustVerb(ctx, t, "pause"))

		// send never blocks: while paused it returns immediately with
		// QUEUED evidence, and the agent stays parked.
		delivery, err := h.sendNoWait(ctx, t, prompt)
		require.NoError(t, err)
		require.Equal(t, "QUEUED", delivery)
		require.Equal(t, "PAUSED", h.state(ctx, t))

		// resume drains the queue; the turn runs and the agent re-idles.
		h.mustVerb(ctx, t, "resume")
		state, err := h.waitFor(ctx, t, "IDLE")
		require.NoError(t, err)
		require.Equal(t, "IDLE", state)

		transcript, lastReply := h.snapshot(ctx, t)
		require.Equal(t, reply, lastReply)
		require.Contains(t, transcript, prompt)
	})

	t.Run("never-started agent", func(ctx context.Context, t *testctx.T) {
		h := spawnAgent(ctx, t, c, spawnOpts{model: newModel(), name: "parked"})

		// Pausing a never-started agent creates its entry paused; the
		// send's signal-with-start parks immediately instead of draining.
		require.Equal(t, "PAUSED", h.mustVerb(ctx, t, "pause"))
		delivery, err := h.sendNoWait(ctx, t, prompt)
		require.NoError(t, err)
		require.Equal(t, "QUEUED", delivery)
		require.Equal(t, "PAUSED", h.state(ctx, t))

		h.mustVerb(ctx, t, "resume")
		state, err := h.waitFor(ctx, t, "IDLE")
		require.NoError(t, err)
		require.Equal(t, "IDLE", state)

		transcript, lastReply := h.snapshot(ctx, t)
		require.Equal(t, reply, lastReply)
		require.Contains(t, transcript, prompt)
	})
}

// TestFailedAndRetry covers the FAILED tombstone: a prompt absent from the
// recording deterministically fails the turn, awaits surface the failure,
// mail sent to the tombstone queues for a retry, resume re-fails
// deterministically, and stop seals the tombstone against further sends.
func (AgentRuntimeSuite) TestFailedAndRetry(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The recording only knows this exchange; the test sends something
	// else, so the replayer reports a history divergence and the loop
	// fails.
	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt("the recorded prompt").
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "the recorded reply"},
		}))

	h := spawnAgent(ctx, t, c, spawnOpts{model: model, name: "failer"})

	// The turn consumes the message and fails; await surfaces the turn's
	// failure.
	_, _, err := h.sendAndWait(ctx, t, "a prompt the recording does not contain")
	require.ErrorContains(t, err, "failed during the turn that consumed this message")

	state, err := h.waitFor(ctx, t, "FAILED")
	require.NoError(t, err)
	require.Equal(t, "FAILED", state)

	// More mail queues behind the tombstone rather than being dropped or
	// rejected: a resume may still retry the loop.
	delivery, err := h.sendNoWait(ctx, t, "more mail while failed")
	require.NoError(t, err)
	require.Equal(t, "QUEUED", delivery)

	// Awaiting queued mail on a FAILED tombstone projects the failure
	// instead of blocking forever (the message itself stays pending for a
	// retry to consume).
	_, _, err = h.sendAndWait(ctx, t, "another queued message")
	require.ErrorContains(t, err, "failed before consuming this message")

	// resume retries from the last committed snapshot: the mismatched
	// prompt is still the pending input, so the retry re-diverges and
	// fails again, deterministically.
	_, err = h.verb(ctx, t, "resume")
	require.NoError(t, err)
	state, err = h.waitFor(ctx, t, "FAILED")
	require.NoError(t, err)
	require.Equal(t, "FAILED", state)

	// stop seals the tombstone: state projects STOPPED from now on, and
	// send fails instead of queuing mail nothing will consume.
	require.Equal(t, "STOPPED", h.mustVerb(ctx, t, "stop"))
	state, err = h.waitFor(ctx, t, "STOPPED")
	require.NoError(t, err)
	require.Equal(t, "STOPPED", state)

	_, err = h.sendNoWait(ctx, t, "mail after stop")
	require.ErrorContains(t, err, "stopped")
}

// TestSteering covers mid-turn message absorption: a send landing while the
// turn dwells in a (slow) tool call is absorbed into that turn — STEERED
// delivery — and both the opening and the steering message await the same
// turn's final reply.
func (AgentRuntimeSuite) TestSteering(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The recording contains a tool call to the bound container's stdout;
	// during replay the real exec runs, dwelling ~6s after touching the
	// marker on the shared cache volume — the deterministic signal that
	// the turn is mid-step. The steer prompt is recorded AFTER the tool
	// result, which is exactly where the loop drains a mid-turn message
	// (the step boundary), so the replayed history matches by
	// construction.
	vol := c.CacheVolume("agent-steer-" + identity.NewID())
	ctrID, err := slowToolContainer(c, vol, 6).ID(ctx)
	require.NoError(t, err)
	model := cannedReplayModel(ctx, t, c, slowToolConversation(c, true))

	h := spawnAgent(ctx, t, c, spawnOpts{model: model, name: "steerable", toolIDs: []dagger.ID{ctrID}})

	// Open the turn; the await blocks until the whole (steered) turn ends.
	var goDelivery, goReply string
	eg := errgroup.Group{}
	eg.Go(func() error {
		var err error
		goDelivery, goReply, err = h.sendAndWait(ctx, t, slowToolPrompt)
		return err
	})

	// Once the marker exists the turn is provably inside the tool call,
	// with several seconds of dwell left.
	waitForSlowTool(ctx, t, c, vol)
	require.Equal(t, "RUNNING", h.state(ctx, t))

	// The steer lands mid-step: absorbed into the in-flight turn, and its
	// await resolves to that same turn's final reply.
	steerDelivery, steerReply, err := h.sendAndWait(ctx, t, steerPrompt)
	require.NoError(t, err)
	require.Equal(t, "STEERED", steerDelivery)
	require.Equal(t, slowToolReply, steerReply)

	// The opening message's await resolved to the same reply.
	require.NoError(t, eg.Wait())
	require.Equal(t, "STARTED", goDelivery)
	require.Equal(t, slowToolReply, goReply)

	// The steered message is on the record: it joined the turn's history.
	transcript, lastReply := h.snapshot(ctx, t)
	require.Contains(t, transcript, steerPrompt)
	require.Equal(t, slowToolReply, lastReply)
}

// TestInterruptMidStep covers preempting an in-flight step: interrupt while
// the turn dwells in the slow tool call cancels just that step, parks the
// agent PAUSED with the completed prefix committed, and resume re-steps the
// still-pending input so the turn completes and the original await resolves.
//
// The interrupt does not change the history the replayer sees: the canceled
// step commits nothing beyond the already-drained prompt, so the resumed
// step re-sends the same recorded prefix (and re-dispatches the tool — the
// canceled exec was never cached, so it dwells again).
func (AgentRuntimeSuite) TestInterruptMidStep(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	vol := c.CacheVolume("agent-interrupt-" + identity.NewID())
	ctrID, err := slowToolContainer(c, vol, 6).ID(ctx)
	require.NoError(t, err)
	model := cannedReplayModel(ctx, t, c, slowToolConversation(c, false))

	h := spawnAgent(ctx, t, c, spawnOpts{model: model, name: "interruptible", toolIDs: []dagger.ID{ctrID}})

	var goDelivery, goReply string
	eg := errgroup.Group{}
	eg.Go(func() error {
		var err error
		goDelivery, goReply, err = h.sendAndWait(ctx, t, slowToolPrompt)
		return err
	})

	waitForSlowTool(ctx, t, c, vol)
	require.Equal(t, "RUNNING", h.state(ctx, t))

	// Preempt the step. The state projects RUNNING until the canceled step
	// actually lands, so wait for the park rather than asserting
	// immediately.
	_, err = h.verb(ctx, t, "interrupt")
	require.NoError(t, err)
	state, err := h.waitFor(ctx, t, "PAUSED")
	require.NoError(t, err)
	require.Equal(t, "PAUSED", state)

	// The completed prefix is intact: the consumed prompt is committed to
	// the snapshot's history, and the turn's final reply has not been
	// produced.
	transcript, _ := h.snapshot(ctx, t)
	require.Contains(t, transcript, slowToolPrompt)
	require.NotContains(t, transcript, slowToolReply)

	// Resume continues the suspended turn: the pending input re-steps, the
	// tool re-runs, and the original await resolves with the turn's reply.
	_, err = h.verb(ctx, t, "resume")
	require.NoError(t, err)

	require.NoError(t, eg.Wait())
	require.Equal(t, "STARTED", goDelivery)
	require.Equal(t, slowToolReply, goReply)

	state, err = h.waitFor(ctx, t, "IDLE")
	require.NoError(t, err)
	require.Equal(t, "IDLE", state)
	transcript, lastReply := h.snapshot(ctx, t)
	require.Contains(t, transcript, slowToolPrompt)
	require.Equal(t, slowToolReply, lastReply)
}

// TestAwaitIdempotency covers shared awaiting: two concurrent awaits on the
// same AgentMessage both get the reply. The two aliased await selections —
// on the message handle loaded from send's pinned ID — resolve concurrently
// within one query (dagql resolves sibling selections in parallel) while the
// turn dwells in the slow tool, so both are genuinely blocked on the same
// unresolved record before it resolves once for both.
func (AgentRuntimeSuite) TestAwaitIdempotency(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	vol := c.CacheVolume("agent-await-" + identity.NewID())
	ctrID, err := slowToolContainer(c, vol, 3).ID(ctx)
	require.NoError(t, err)
	model := cannedReplayModel(ctx, t, c, slowToolConversation(c, false))

	h := spawnAgent(ctx, t, c, spawnOpts{model: model, name: "sharedawait", toolIDs: []dagger.ID{ctrID}})

	msgID, err := h.sendID(ctx, t, slowToolPrompt)
	require.NoError(t, err)
	out, err := h.msgRun(ctx, t, msgID, `delivery first: await second: await`)
	require.NoError(t, err)
	require.Equal(t, "STARTED", out.Get("delivery").String())
	require.Equal(t, slowToolReply, out.Get("first").String())
	require.Equal(t, slowToolReply, out.Get("second").String())
}

// TestMessageIdentity covers re-exec pinning of message handles (design §9):
// send returns the ID of the pinned handle — the honest chain
// …agent(id:…)!message(id:…) — and that ID re-addresses the SAME message record
// from a later request. That is the cancel-and-re-await contract: an await
// canceled mid-turn loses nothing — a fresh request re-loads the handle via
// node(id:) and awaits the reply. Also locks in the lookup's clean failure
// modes: unknown message IDs, and agents with no runtime entry.
func (AgentRuntimeSuite) TestMessageIdentity(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	vol := c.CacheVolume("agent-msgid-" + identity.NewID())
	ctrID, err := slowToolContainer(c, vol, 6).ID(ctx)
	require.NoError(t, err)
	model := cannedReplayModel(ctx, t, c, slowToolConversation(c, false))

	h := spawnAgent(ctx, t, c, spawnOpts{model: model, name: "readdressable", toolIDs: []dagger.ID{ctrID}})

	// Request 1: send returns immediately (it never blocks) with the pinned
	// message ID — the addressable handle a detached DoNotCache result could
	// never yield. The delivery evidence rides the record, readable through
	// the loaded handle.
	msgID, err := h.sendID(ctx, t, slowToolPrompt)
	require.NoError(t, err)
	require.NotEmpty(t, msgID)
	out, err := h.msgRun(ctx, t, msgID, `delivery`)
	require.NoError(t, err)
	require.Equal(t, "STARTED", out.Get("delivery").String())

	// awaitByID re-addresses the pinned handle in a fresh request —
	// node(id:) replays the …agent(id:…)!message(id:…) chain — and awaits it.
	awaitByID := func(ctx context.Context) (delivery, reply string, _ error) {
		res := map[string]any{}
		err := c.Do(ctx, &dagger.Request{
			Query:     `query($id: ID!) { node(id: $id) { ... on AgentMessage { delivery await } } }`,
			Variables: map[string]any{"id": msgID},
		}, &dagger.Response{Data: &res})
		if err != nil {
			return "", "", err
		}
		raw, err := json.Marshal(res)
		if err != nil {
			return "", "", err
		}
		node := gjson.Get(string(raw), "node")
		return node.Get("delivery").String(), node.Get("await").String(), nil
	}

	// Cancel-and-re-await, first half: an await issued while the turn
	// provably dwells in the slow tool call, then canceled while blocked.
	// The await fails with the cancellation, whatever the exact
	// interleaving of issue and cancel.
	awaitCtx, cancelAwait := context.WithCancel(ctx)
	defer cancelAwait()
	awaitErr := make(chan error, 1)
	go func() {
		_, _, err := awaitByID(awaitCtx)
		awaitErr <- err
	}()
	waitForSlowTool(ctx, t, c, vol)
	require.Equal(t, "RUNNING", h.state(ctx, t))
	cancelAwait()
	require.ErrorContains(t, <-awaitErr, "context canceled")

	// Second half: a fresh request re-awaits the same handle and gets the
	// turn's reply — the canceled await lost nothing. The turn is still
	// mid-tool when this await is issued (the tool dwells for seconds past
	// the marker), so the await genuinely blocks before resolving; the
	// delivery evidence rides the record, unchanged.
	delivery, reply, err := awaitByID(ctx)
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)
	require.Equal(t, slowToolReply, reply)

	// Unknown message ID on an agent WITH a runtime entry: clear error.
	_, err = h.run(ctx, t, `message(id: "bogus") { delivery }`)
	require.ErrorContains(t, err, "no record of message")

	// Lookup on an agent with NO runtime entry: clear error — message is a
	// pure lookup and never creates one. (Spawning mints only the value;
	// the runtime entry appears at first start/send.)
	fresh := spawnAgent(ctx, t, c, spawnOpts{model: emptyReplayModel, name: "never-ran"})
	_, err = fresh.run(ctx, t, `message(id: "bogus") { delivery }`)
	require.ErrorContains(t, err, "no runtime entry")
}
