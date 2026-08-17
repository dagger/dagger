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
//
// The three TestRosterAddressing* tests reach past the API and into the
// trace, standing up an OTLP endpoint of their own and folding what the
// session's CLI forwards to it into the same dagui.DB a frontend builds its
// roster from. The round trip is the point: the client half of the agent
// directory (design §3.3) is not an engine behaviour that GraphQL can be
// asked about — it only exists once spans and log records have actually
// crossed the wire.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/testutil"
	telemetry "github.com/dagger/otel-go"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
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
// state read just after (or the caller's cancellation error).
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

// llmWithPrompt records one prompt onto a fresh conversation and returns the
// conversation's ID. It stands in for what a resuming client holds: a
// conversation with history, rebuilt from the trace's snapshot anchor, with no
// runtime anywhere behind it.
func llmWithPrompt(ctx context.Context, t *testctx.T, c *dagger.Client, model, prompt string) string {
	t.Helper()
	res := map[string]any{}
	require.NoError(t, c.Do(ctx,
		&dagger.Request{
			Query: `query($model: String!, $prompt: String!) {
				llm(model: $model) { withPrompt(prompt: $prompt) { id } }
			}`,
			Variables: map[string]any{"model": model, "prompt": prompt},
		},
		&dagger.Response{Data: &res},
	))
	raw, err := json.Marshal(res)
	require.NoError(t, err)
	id := gjson.Get(string(raw), "llm.withPrompt.id").String()
	require.NotEmpty(t, id, "conversation ID missing in response: %s", raw)
	return id
}

// rehydrateAgent runs the restore chain of design §3.2 verbatim —
// loadLLMFromID(<snapshot>) { agent(id:, name:) { rehydrate(...) } } — and
// returns a handle on the restored instance. Error-returning, because half
// the point of rehydrate is which calls it refuses.
func rehydrateAgent(ctx context.Context, c *dagger.Client, llmID, instanceID, name, state, errText string) (*agentHandle, error) {
	res := map[string]any{}
	if err := c.Do(ctx,
		&dagger.Request{
			Query: `query($llm: ID!, $id: String!, $name: String!, $state: AgentState!, $error: String!) {
				node(id: $llm) { ... on LLM {
					agent(id: $id, name: $name) { rehydrate(state: $state, error: $error) }
				} }
			}`,
			Variables: map[string]any{
				"llm":   llmID,
				"id":    instanceID,
				"name":  name,
				"state": state,
				"error": errText,
			},
		},
		&dagger.Response{Data: &res},
	); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	out := gjson.Get(string(raw), "node.agent.rehydrate")
	if !out.Exists() || out.String() == "" {
		return nil, fmt.Errorf("rehydrated agent ID missing in response: %s", raw)
	}
	return &agentHandle{c: c, agentID: out.String()}, nil
}

// reseedAgent runs the continuity verb — node(id:) { ... on Agent {
// reseed(conversation:) } } — replacing the instance's committed conversation
// with the given one. Error-returning, because half the point of reseed is
// which calls it refuses.
func (h *agentHandle) reseedAgent(ctx context.Context, t *testctx.T, llmID string) error {
	t.Helper()
	res := map[string]any{}
	return h.c.Do(ctx,
		&dagger.Request{
			Query: `query($id: ID!, $llm: ID!) {
				node(id: $id) { ... on Agent { reseed(conversation: $llm) } }
			}`,
			Variables: map[string]any{"id": h.agentID, "llm": llmID},
		},
		&dagger.Response{Data: &res},
	)
}

// unmintedAgent builds a handle on an instance no spawn ever minted: the pure
// agent(id:, name:) lookup, which touches no runtime state. This is the shape
// a client holds after rebuilding a handle from a trace whose agents it has
// NOT re-hydrated — the case §4.2 exists to make loud.
func unmintedAgent(ctx context.Context, t *testctx.T, c *dagger.Client, instanceID, name string) *agentHandle {
	t.Helper()
	res := map[string]any{}
	require.NoError(t, c.Do(ctx,
		&dagger.Request{
			Query: `query($model: String!, $id: String!, $name: String!) {
				llm(model: $model) { agent(id: $id, name: $name) { id } }
			}`,
			Variables: map[string]any{"model": emptyReplayModel, "id": instanceID, "name": name},
		},
		&dagger.Response{Data: &res},
	))
	raw, err := json.Marshal(res)
	require.NoError(t, err)
	id := gjson.Get(string(raw), "llm.agent.id").String()
	require.NotEmpty(t, id, "agent ID missing in response: %s", raw)
	return &agentHandle{c: c, agentID: id}
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
// start, an empty seed idling without any model call, and stop tombstones.
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

	// STOPPED is dormant: waitFor can still observe the tombstone, and a later
	// send or resume may move the same entry back into a live state.
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

	// Same display name — a label, not an identity. The instance ID is the
	// identity, and reading it off the handle is how a client correlates an
	// agent it drives with the entry the trace publishes for it (the loop
	// span's dagger.io/agent.id is this same value).
	firstIdentity := first.mustRun(ctx, t, `name instanceID`)
	secondIdentity := second.mustRun(ctx, t, `name instanceID`)
	require.Equal(t, "twin", firstIdentity.Get("name").String())
	require.Equal(t, "twin", secondIdentity.Get("name").String())
	require.NotEmpty(t, firstIdentity.Get("instanceID").String())
	require.NotEqual(t,
		firstIdentity.Get("instanceID").String(),
		secondIdentity.Get("instanceID").String(),
		"twins share a label but never an instance identity")

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

// TestSpawnAfterStop covers both ways forward from a stopped instance: a new
// spawn still mints an independent successor, while sending through the held
// predecessor handle relaunches that same instance from its preserved history.
// Old message IDs remain awaitable across the relaunch.
func (AgentRuntimeSuite) TestSpawnAfterStop(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const (
		prompt        = "prompt for the phoenix"
		reply         = "the recorded phoenix reply"
		restartPrompt = "prompt after the phoenix restarts"
		restartReply  = "the restarted phoenix reply"
	)
	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt(prompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: reply},
		}).
		WithPrompt(restartPrompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: restartReply},
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
	// The predecessor's old message remains addressable after stop.
	out, err = first.msgRun(ctx, t, firstMsgID, `await`)
	require.NoError(t, err)
	require.Equal(t, reply, out.Get("await").String())

	// A send through the stopped handle reopens the SAME entry and continues
	// from its preserved history. The replay provider only knows this second
	// exchange after the first, so resolving proves this was a relaunch rather
	// than a new runtime seeded from scratch.
	delivery, gotReply, err = first.sendAndWait(ctx, t, restartPrompt)
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)
	require.Equal(t, restartReply, gotReply)
	require.Equal(t, "IDLE", first.state(ctx, t))
	transcript, lastReply = first.snapshot(ctx, t)
	require.Contains(t, transcript, prompt)
	require.Contains(t, transcript, restartPrompt)
	require.Equal(t, restartReply, lastReply)
}

// TestReseed covers the continuity verb: reseed swaps an instance's
// committed conversation in place — same identity, same entry, same mailbox
// — where a stop-and-respawn would mint a successor instance under the same
// display name. The replay provider is the proof of continuation throughout:
// it diverges on any history but the recorded one, so a turn resolving after
// a reseed proves the agent really continued from the reseeded conversation
// rather than its original one.
func (AgentRuntimeSuite) TestReseed(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("swaps the conversation in place", func(ctx context.Context, t *testctx.T) {
		const (
			oldPrompt  = "prompt for the first life"
			oldReply   = "the first-life reply"
			newPrompt  = "prompt already on the reseeded conversation"
			newReply   = "the reseeded reply"
			nextPrompt = "prompt sent after the reseed"
			nextReply  = "the post-reseed reply"
		)
		oldModel := cannedReplayModel(ctx, t, c, c.LLM().
			WithPrompt(oldPrompt).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: oldReply},
			}))
		h := spawnAgent(ctx, t, c, spawnOpts{model: oldModel, name: "continuous"})

		delivery, reply, err := h.sendAndWait(ctx, t, oldPrompt)
		require.NoError(t, err)
		require.Equal(t, "STARTED", delivery)
		require.Equal(t, oldReply, reply)

		// The replacement conversation: a different recording whose history
		// already holds one exchange, with the follow-up turn recorded.
		newModel := cannedReplayModel(ctx, t, c, c.LLM().
			WithPrompt(newPrompt).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: newReply},
			}).
			WithPrompt(nextPrompt).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: nextReply},
			}))
		newConvo, err := c.LLM(dagger.LLMOpts{Model: newModel}).
			WithPrompt(newPrompt).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: newReply},
			}).
			ID(ctx)
		require.NoError(t, err)
		require.NoError(t, h.reseedAgent(ctx, t, string(newConvo)))

		// Same instance, same entry: the state is untouched, and the
		// snapshot is the NEW conversation — the first life's exchange is
		// no longer in the history at all.
		require.Equal(t, "IDLE", h.state(ctx, t))
		transcript, lastReply := h.snapshot(ctx, t)
		require.Contains(t, transcript, newPrompt)
		require.Equal(t, newReply, lastReply)
		require.NotContains(t, transcript, oldPrompt)

		// The next turn continues from the reseeded history: the recorded
		// follow-up resolving is the replay provider's proof.
		delivery, reply, err = h.sendAndWait(ctx, t, nextPrompt)
		require.NoError(t, err)
		require.Equal(t, "STARTED", delivery)
		require.Equal(t, nextReply, reply)
		transcript, lastReply = h.snapshot(ctx, t)
		require.Contains(t, transcript, newPrompt)
		require.Contains(t, transcript, nextPrompt)
		require.Equal(t, nextReply, lastReply)
		require.NotContains(t, transcript, oldPrompt)
	})

	t.Run("queued mail drains onto the new conversation", func(ctx context.Context, t *testctx.T) {
		const (
			newPrompt    = "history on the reseeded conversation"
			newReply     = "the reseeded history reply"
			queuedPrompt = "message queued before the reseed"
			queuedReply  = "the drained reply"
		)
		// The agent's own seed recording is empty: only the reseeded
		// conversation's recording knows the queued prompt, so the drain
		// resolving proves the message landed on the NEW history — a
		// message is addressed to the agent, not to a particular
		// conversation.
		h := spawnAgent(ctx, t, c, spawnOpts{model: emptyReplayModel, name: "queued"})
		require.Equal(t, "PAUSED", h.mustVerb(ctx, t, "pause"))
		msgID, err := h.sendID(ctx, t, queuedPrompt)
		require.NoError(t, err)
		out, err := h.msgRun(ctx, t, msgID, `delivery`)
		require.NoError(t, err)
		require.Equal(t, "QUEUED", out.Get("delivery").String())

		newModel := cannedReplayModel(ctx, t, c, c.LLM().
			WithPrompt(newPrompt).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: newReply},
			}).
			WithPrompt(queuedPrompt).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: queuedReply},
			}))
		newConvo, err := c.LLM(dagger.LLMOpts{Model: newModel}).
			WithPrompt(newPrompt).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: newReply},
			}).
			ID(ctx)
		require.NoError(t, err)
		require.NoError(t, h.reseedAgent(ctx, t, string(newConvo)))

		// Reseed touched exactly one fact: the pause and the queued mail
		// both survive it.
		require.Equal(t, "PAUSED", h.state(ctx, t))

		h.mustVerb(ctx, t, "resume")
		out, err = h.msgRun(ctx, t, msgID, `await`)
		require.NoError(t, err)
		require.Equal(t, queuedReply, out.Get("await").String())
		transcript, lastReply := h.snapshot(ctx, t)
		require.Contains(t, transcript, newPrompt)
		require.Contains(t, transcript, queuedPrompt)
		require.Equal(t, queuedReply, lastReply)
	})

	t.Run("a failed agent retries from the new conversation", func(ctx context.Context, t *testctx.T) {
		const (
			newPrompt  = "history on the recovery conversation"
			newReply   = "the recovery history reply"
			nextPrompt = "prompt after the recovery"
			nextReply  = "the recovered reply"
		)
		model := cannedReplayModel(ctx, t, c, c.LLM().
			WithPrompt("the recorded prompt").
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: "the recorded reply"},
			}))
		h := spawnAgent(ctx, t, c, spawnOpts{model: model, name: "recoverable"})

		// Fail the loop deterministically: a prompt the recording does not
		// contain diverges the replayer.
		_, _, err := h.sendAndWait(ctx, t, "a prompt the recording does not contain")
		require.ErrorContains(t, err, "failed")
		state, err := h.waitFor(ctx, t, "FAILED")
		require.NoError(t, err)
		require.Equal(t, "FAILED", state)

		// Reseed swaps the conversation and nothing else: the tombstone
		// keeps its error, so the projection still says FAILED — reseed and
		// resume compose instead of overlapping.
		newModel := cannedReplayModel(ctx, t, c, c.LLM().
			WithPrompt(newPrompt).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: newReply},
			}).
			WithPrompt(nextPrompt).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: nextReply},
			}))
		newConvo, err := c.LLM(dagger.LLMOpts{Model: newModel}).
			WithPrompt(newPrompt).
			WithResponse([]dagger.LLMContentBlockInput{
				{Kind: dagger.LLMContentBlockKindText, Text: newReply},
			}).
			ID(ctx)
		require.NoError(t, err)
		require.NoError(t, h.reseedAgent(ctx, t, string(newConvo)))
		require.Equal(t, "FAILED", h.state(ctx, t))

		// Resume relaunches from the reseeded conversation, whose input is
		// all settled: the failure is behind it, and the next turn runs
		// from the new history.
		_, err = h.verb(ctx, t, "resume")
		require.NoError(t, err)
		state, err = h.waitFor(ctx, t, "IDLE")
		require.NoError(t, err)
		require.Equal(t, "IDLE", state)

		delivery, reply, err := h.sendAndWait(ctx, t, nextPrompt)
		require.NoError(t, err)
		require.Equal(t, "STARTED", delivery)
		require.Equal(t, nextReply, reply)
	})

	t.Run("refuses an instance with no runtime", func(ctx context.Context, t *testctx.T) {
		h := unmintedAgent(ctx, t, c, identity.NewID(), "ghost")
		convo, err := c.LLM(dagger.LLMOpts{Model: emptyReplayModel}).ID(ctx)
		require.NoError(t, err)
		err = h.reseedAgent(ctx, t, string(convo))
		require.ErrorContains(t, err, "no runtime in this session")
	})

	t.Run("refuses an in-flight step and rewinds a suspended turn", func(ctx context.Context, t *testctx.T) {
		vol := c.CacheVolume("agent-reseed-" + identity.NewID())
		ctrID, err := slowToolContainer(c, vol, 6).ID(ctx)
		require.NoError(t, err)
		const (
			editedPrompt = "start the corrected work"
			editedReply  = "the corrected turn is done"
		)
		rewoundConversation := c.LLM().
			WithPrompt(editedPrompt).
			WithResponse([]dagger.LLMContentBlockInput{{
				Kind: dagger.LLMContentBlockKindText,
				Text: editedReply,
			}})
		model := cannedReplayModel(ctx, t, c, slowToolConversation(c, false))
		h := spawnAgent(ctx, t, c, spawnOpts{model: model, name: "busy", toolIDs: []dagger.ID{ctrID}})
		convo, err := c.LLM(dagger.LLMOpts{
			Model: cannedReplayModel(ctx, t, c, rewoundConversation),
		}).ID(ctx)
		require.NoError(t, err)

		var goReply string
		eg := errgroup.Group{}
		eg.Go(func() error {
			var err error
			_, goReply, err = h.sendAndWait(ctx, t, slowToolPrompt)
			return err
		})

		// Mid-step: the turn is provably dwelling inside the tool call.
		waitForSlowTool(ctx, t, c, vol)
		require.Equal(t, "RUNNING", h.state(ctx, t))
		err = h.reseedAgent(ctx, t, string(convo))
		require.ErrorContains(t, err, "mid-turn")

		// Suspended: an interrupt parks PAUSED with the turn still open. Reseed
		// now deliberately abandons that consumed message and commits the
		// replacement conversation, which is the engine half of inline edit.
		_, err = h.verb(ctx, t, "interrupt")
		require.NoError(t, err)
		state, err := h.waitFor(ctx, t, "PAUSED")
		require.NoError(t, err)
		require.Equal(t, "PAUSED", state)
		require.NoError(t, h.reseedAgent(ctx, t, string(convo)))

		// The old await is settled rather than left hanging, and the active
		// snapshot contains neither its prompt nor any later tool history.
		err = eg.Wait()
		require.ErrorContains(t, err, "rewound")
		require.Empty(t, goReply)
		transcript, _ := h.snapshot(ctx, t)
		require.NotContains(t, transcript, slowToolPrompt)

		// Reword and resume from the replacement: the edited prompt queues behind
		// the preserved pause, then completes against the truncated conversation.
		editedID, err := h.sendID(ctx, t, editedPrompt)
		require.NoError(t, err)
		out, err := h.msgRun(ctx, t, editedID, `delivery`)
		require.NoError(t, err)
		require.Equal(t, "QUEUED", out.Get("delivery").String())
		_, err = h.verb(ctx, t, "resume")
		require.NoError(t, err)
		out, err = h.msgRun(ctx, t, editedID, `await`)
		require.NoError(t, err)
		require.Equal(t, editedReply, out.Get("await").String())
		state, err = h.waitFor(ctx, t, "IDLE")
		require.NoError(t, err)
		require.Equal(t, "IDLE", state)
		transcript, lastReply := h.snapshot(ctx, t)
		require.Contains(t, transcript, editedPrompt)
		require.NotContains(t, transcript, slowToolPrompt)
		require.Equal(t, editedReply, lastReply)
	})

	t.Run("refuses a stopped agent", func(ctx context.Context, t *testctx.T) {
		h := spawnAgent(ctx, t, c, spawnOpts{model: emptyReplayModel, name: "sealed"})
		require.Equal(t, "STOPPED", h.mustVerb(ctx, t, "stop"))
		convo, err := c.LLM(dagger.LLMOpts{Model: emptyReplayModel}).ID(ctx)
		require.NoError(t, err)
		err = h.reseedAgent(ctx, t, string(convo))
		require.ErrorContains(t, err, "stopped")
	})
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

	t.Run("stopped agent through resume-first send", func(ctx context.Context, t *testctx.T) {
		h := spawnAgent(ctx, t, c, spawnOpts{model: newModel(), name: "restartable"})
		require.Equal(t, "STOPPED", h.mustVerb(ctx, t, "stop"))

		// modules/staff sendTo and ask deliberately call resume before send.
		// Resume must therefore relaunch a stopped worker even before mail is
		// queued; with no pending input the fresh loop simply idles.
		require.Equal(t, "IDLE", h.mustVerb(ctx, t, "resume"))

		delivery, gotReply, err := h.sendAndWait(ctx, t, prompt)
		require.NoError(t, err)
		require.Equal(t, "STARTED", delivery)
		require.Equal(t, reply, gotReply)
		require.Equal(t, "IDLE", h.state(ctx, t))
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

	// stop seals the failed retry into STOPPED, but a later message explicitly
	// reopens the same entry. The original mismatched input is still pending,
	// so this test only asserts delivery; TestSpawnAfterStop covers a successful
	// continued turn.
	require.Equal(t, "STOPPED", h.mustVerb(ctx, t, "stop"))
	state, err = h.waitFor(ctx, t, "STOPPED")
	require.NoError(t, err)
	require.Equal(t, "STOPPED", state)

	delivery, err = h.sendNoWait(ctx, t, "mail after stop")
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)
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

// TestInterruptMidStep covers preempting an in-flight tool call. Interrupt
// records the assistant tool call plus its canceled result as a valid completed
// prefix, drops follow-up mail that had not reached a step boundary, and parks
// the agent. Resume continues from the canceled result without re-dispatching
// the tool, so a later prompt can refer to why it ran.
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

	// Queue a follow-up while the tool call is in flight. It reports STEERED,
	// but has not influenced the model until the next step boundary.
	queuedID, err := h.sendID(ctx, t, steerPrompt)
	require.NoError(t, err)
	out, err := h.msgRun(ctx, t, queuedID, `delivery`)
	require.NoError(t, err)
	require.Equal(t, "STEERED", out.Get("delivery").String())

	// Preempt the step. The state projects RUNNING until the canceled tool
	// result is recorded, so wait for the park rather than asserting
	// immediately.
	_, err = h.verb(ctx, t, "interrupt")
	require.NoError(t, err)
	state, err := h.waitFor(ctx, t, "PAUSED")
	require.NoError(t, err)
	require.Equal(t, "PAUSED", state)

	// The recorded prefix is protocol-valid: the assistant's tool call and its
	// errored result are both present. The queued follow-up was unconsumed, so
	// Ctrl-C drops it and resolves its message handle with an interrupt.
	transcript, _ := h.snapshot(ctx, t)
	require.Contains(t, transcript, slowToolPrompt)
	require.Contains(t, transcript, "[Assistant tool calls]: stdout(")
	require.Contains(t, transcript, "[Tool result ERROR]")
	require.NotContains(t, transcript, steerPrompt)
	_, err = h.msgRun(ctx, t, queuedID, `await`)
	require.ErrorContains(t, err, "interrupted before consuming this message")

	// Resume submits the recorded cancellation result to the model and reaches
	// the reply without running the interrupted tool again. The opening
	// message remains part of the suspended turn and resolves normally.
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
	require.NotContains(t, transcript, steerPrompt)
	require.Equal(t, slowToolReply, lastReply)
}

// TestInterruptModuleToolCall is TestInterruptMidStep with the one
// ingredient that test lacks: the dwelling tool is a MODULE function (Dang,
// like modules/staff's ask), not a bound container method. The tool call
// chain then runs interrupt's cancellation through every seam the staff
// shape exercises — MCP.Call -> dagql single-flight -> ModuleFunction.Call
// -> in-process Dang eval -> nested client query -> container exec — and a
// live session showed that chain surviving an interrupt engine-side (loop
// in pool.Wait, MCP.Call in Cache.wait, Dang query in
// waitForLazyEvaluation, 45+ minutes after the step was preempted).
//
// Two assertions pin propagation, both bounded so a leak fails rather than
// wedges CI:
//
//   - the loop parks: PAUSED projects only when the canceled step actually
//     lands (mirrors TestInterruptMidStep), so a tool call that never
//     returns keeps the state RUNNING and the bounded waitFor errors;
//   - the work stops: the module function's exec heartbeats to a shared
//     cache volume, and after the park the beat must go quiet — the
//     observable proof that cancellation reached the BOTTOM of the chain,
//     not just the loop's own pool wait.
func (AgentRuntimeSuite) TestInterruptModuleToolCall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modDir := t.TempDir()
	copyTestdataFixture(ctx, t, modDir, "modules", "dang", "blocker")
	require.NoError(t, c.ModuleSource(modDir).AsModule().Serve(ctx))

	blockerID := queryID(ctx, t, c, `{ blocker { id } }`, "blocker.id")

	volName := "agent-modtool-" + identity.NewID()

	const (
		blockPrompt = "start the module slow work"
		blockReply  = "the module turn is done"
	)
	args, err := json.Marshal(map[string]string{
		"volume": volName,
		"bust":   identity.NewID(),
	})
	require.NoError(t, err)
	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt(blockPrompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: "Dispatching the module tool."},
			{Kind: dagger.LLMContentBlockKindToolCall, CallID: "call_1", ToolName: "block", Arguments: dagger.JSON(args)},
		}).
		// Placeholder result: the real module call runs during replay (tool
		// results are excluded from the replayer's history matching).
		WithToolResult("call_1", "", false).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: blockReply},
		}))

	h := spawnAgent(ctx, t, c, spawnOpts{model: model, name: "modtool", toolIDs: []dagger.ID{blockerID}})

	// Open the turn without awaiting the reply: the turn is about to be
	// suspended indefinitely, so there is no reply to wait for.
	delivery, err := h.sendNoWait(ctx, t, blockPrompt)
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)

	// peekBeat reads the heartbeat through the module (its cache volumes
	// are namespaced per module, so the test client cannot mount the same
	// volume itself). Empty until the exec's first beat lands.
	peekBeat := func() string {
		res := map[string]any{}
		require.NoError(t, c.Do(ctx,
			&dagger.Request{
				Query: `query($volume: String!, $bust: String!) {
					blocker { peek(volume: $volume, bust: $bust) }
				}`,
				Variables: map[string]any{"volume": volName, "bust": identity.NewID()},
			},
			&dagger.Response{Data: &res},
		))
		raw, err := json.Marshal(res)
		require.NoError(t, err)
		return strings.TrimSpace(gjson.Get(string(raw), "blocker.peek").String())
	}

	// The turn is provably inside the module tool call once the first beat
	// lands.
	startDeadline := time.Now().Add(60 * time.Second)
	for peekBeat() == "" {
		require.True(t, time.Now().Before(startDeadline),
			"the module tool's exec never started")
		time.Sleep(time.Second)
	}
	require.Equal(t, "RUNNING", h.state(ctx, t))

	// Preempt the step, then require the park within a bound: if the module
	// tool call absorbs the cancellation, the canceled step never lands,
	// PAUSED never projects, and this waitFor times out — the leak's
	// loop-side symptom.
	_, err = h.verb(ctx, t, "interrupt")
	require.NoError(t, err)
	parkCtx, cancelPark := context.WithTimeout(ctx, 60*time.Second)
	defer cancelPark()
	state, err := h.waitFor(parkCtx, t, "PAUSED")
	require.NoError(t, err, "the interrupted step never landed: the module tool call absorbed the cancellation")
	require.Equal(t, "PAUSED", state)

	// The bottom of the chain: the exec's heartbeat must go quiet. Beats
	// land every ~0.5s while it runs, so two identical reads 2s apart mean
	// it stopped; poll the pair to give cancellation a grace to travel.
	quiet := false
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		before := peekBeat()
		time.Sleep(2 * time.Second)
		if peekBeat() == before {
			quiet = true
			break
		}
	}
	require.True(t, quiet,
		"the module tool's exec kept heartbeating after the interrupt: cancellation never reached it")
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
	// pure lookup and never creates one. The handle for such an instance is
	// the bare agent(id:, name:) lookup, since spawn now creates the entry
	// it mints (see TestSendRequiresRuntime for why a miss must never be a
	// constructor).
	ghost := unmintedAgent(ctx, t, c, identity.NewID(), "never-ran")
	_, err = ghost.run(ctx, t, `message(id: "bogus") { delivery }`)
	require.ErrorContains(t, err, "no runtime entry")
}

// agentTraceSink is the consumer half of the agent directory, stood up
// in-process: an OTLP endpoint the session's CLI forwards engine telemetry
// to, folded into the same dagui.DB a frontend builds its roster from.
//
// A frontend owns its DB single-threaded, so ingest (HTTP handler
// goroutines) and the test's reads are serialized on one mutex rather than
// the DB being made concurrent.
//
// It also KEEPS every export request it was handed, in arrival order. That is
// what makes a capture of a real agent session replayable: the resume test
// serves them back over the §5.1 Cloud endpoints
// (hack/designs/resume-from-trace.md §10, "End to end, replay provider"), so
// the restore runs against telemetry the engine really published rather than
// a canned approximation of it.
type agentTraceSink struct {
	mu     sync.Mutex
	db     *dagui.DB
	logExp sdklog.Exporter
	base   string

	traces []*coltracepb.ExportTraceServiceRequest
	logs   []*collogspb.ExportLogsServiceRequest
}

func newAgentTraceSink(t *testctx.T) *agentTraceSink {
	t.Helper()
	db := dagui.NewDB()
	sink := &agentTraceSink{db: db, logExp: db.LogExporter()}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/traces", sink.tracesHandler)
	mux.HandleFunc("POST /v1/logs", sink.logsHandler)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &http.Server{Handler: mux}
	go srv.Serve(l) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })

	sink.base = "http://" + l.Addr().String()
	return sink
}

// clientOpts points the CLI session this client spawns at the sink. LIVE is
// what makes a still-running agent's loop span arrive at all: without it the
// CLI only forwards spans once they have ended.
func (sink *agentTraceSink) clientOpts() []dagger.ClientOpt {
	return []dagger.ClientOpt{
		dagger.WithEnvironmentVariable("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", sink.base+"/v1/traces"),
		dagger.WithEnvironmentVariable("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", sink.base+"/v1/logs"),
		dagger.WithEnvironmentVariable("OTEL_EXPORTER_OTLP_TRACES_LIVE", "1"),
	}
}

func (sink *agentTraceSink) tracesHandler(w http.ResponseWriter, r *http.Request) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sink.traces = append(sink.traces, &req)
	if err := sink.db.ExportSpans(r.Context(), telemetry.SpansFromPB(req.ResourceSpans)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (sink *agentTraceSink) logsHandler(w http.ResponseWriter, r *http.Request) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req collogspb.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sink.logs = append(sink.logs, &req)
	if err := telemetry.ReexportLogsFromPB(r.Context(), sink.logExp, &req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// read runs fn against the DB with ingest held off.
func (sink *agentTraceSink) read(fn func(db *dagui.DB)) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	fn(sink.db)
}

// awaitAgent blocks until the trace has published exactly one agent, in the
// given state and with an addressable call digest, and returns that roster
// entry — identity folded from the loop span's attributes, state from its
// log records.
func (sink *agentTraceSink) awaitAgent(t *testctx.T, state string) *dagui.AgentNode {
	t.Helper()
	var node *dagui.AgentNode
	require.EventuallyWithT(t, func(ct *assert.CollectT) {
		sink.read(func(db *dagui.DB) {
			agents := db.Agents()
			if !assert.Len(ct, agents, 1) {
				return
			}
			if !assert.NotEmpty(ct, agents[0].CallDigest) ||
				!assert.Equal(ct, state, agents[0].State) {
				return
			}
			node = agents[0]
		})
	}, 60*time.Second, 100*time.Millisecond)
	return node
}

// awaitAgents blocks until the trace has published the given number of
// agents, each with an addressable call digest AND a resume anchor, and
// returns them keyed by display name. It is awaitAgent's multi-agent form:
// what a restore reads is the anchor, so waiting on the roster alone would
// race the record that makes the trace restorable at all.
func (sink *agentTraceSink) awaitAgents(t *testctx.T, count int) map[string]*dagui.AgentNode {
	t.Helper()
	byName := map[string]*dagui.AgentNode{}
	require.EventuallyWithT(t, func(ct *assert.CollectT) {
		clear(byName)
		sink.read(func(db *dagui.DB) {
			agents := db.Agents()
			if !assert.Len(ct, agents, count) {
				return
			}
			for _, agent := range agents {
				if !assert.NotEmpty(ct, agent.CallDigest, "agent %q has no call digest", agent.Name) ||
					!assert.NotEmpty(ct, agent.SnapshotDigest, "agent %q has no resume anchor", agent.Name) {
					return
				}
				byName[agent.Name] = agent
			}
		})
	}, 60*time.Second, 100*time.Millisecond)
	return byName
}

// capture returns the OTLP export requests the session forwarded, in arrival
// order — the raw material a fake Cloud serves back.
func (sink *agentTraceSink) capture() ([]*coltracepb.ExportTraceServiceRequest, []*collogspb.ExportLogsServiceRequest) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return slices.Clone(sink.traces), slices.Clone(sink.logs)
}

// rebuild turns a roster entry back into a handle the way a frontend would:
// find the span carrying the advertised call digest, rebuild the ID from the
// call payloads the client has ingested — Span.CallID walks receiver
// digests, argument literals and module frames through the DB
// (dagql/dagui/extract.go), so it closes only if every frame's span reached
// this client — and encode it. Returns the handle and the rebuilt chain.
func (sink *agentTraceSink) rebuild(t *testctx.T, c *dagger.Client, node *dagui.AgentNode) (*agentHandle, *call.ID) {
	t.Helper()
	var callID *call.ID
	var encoded string
	sink.read(func(db *dagui.DB) {
		var match *dagui.Span
		for _, span := range db.Spans.Map {
			if span.CallDigest == node.CallDigest {
				match = span
				break
			}
		}
		require.NotNil(t, match, "no span carries the advertised call digest")
		require.Equal(t, "agent", match.Call().Field,
			"the digest must name the pinned agent(id:, name:) lookup")

		var err error
		callID, err = match.CallID()
		require.NoError(t, err)
		encoded, err = callID.Encode()
		require.NoError(t, err)
	})
	return &agentHandle{c: c, agentID: encoded}, callID
}

// TestRosterAddressing covers the claim §3.3 rests its whole no-namespace
// argument on: that a client can turn what the trace advertises about an
// agent back into a WORKING handle on that same live runtime. Without it the
// roster is a read-only display and an agent you did not spawn is
// unreachable — which is the capability a Query.agents namespace would have
// provided, and which telemetry is supposed to provide instead.
//
// The path is the one branch-from-message already uses: the loop span's
// dagger.io/agent.call.digest names a dagql call; the client finds the span
// carrying that call digest, rebuilds the ID from the call payloads it has
// ingested (Span.CallID walks receiver digests through the DB), encodes it,
// and loads it. The digest names spawn's internal Select of the pure
// agent(id:, name:) lookup — a span the UI hides as internal, but which
// carries its call payload like any other, which is what makes this work.
//
// The identity assertions are deliberately ones a freshly derived agent
// value could never satisfy. Re-deriving the composition yields a value with
// the same content digest, so a broken reconstruction would not error — it
// would silently land on a different, inert runtime, and the user would
// prompt a corpse. So the test asserts on runtime facts: the FAILED state
// and the transcript of a message only this instance ever received, and a
// send that reports QUEUED (mail behind a tombstone) where an inert agent
// would report STARTED.
func (AgentRuntimeSuite) TestRosterAddressing(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		// An inherited session is already attached to somebody else's
		// frontend; only a CLI session this test starts can be pointed at
		// the sink.
		t.Skip("needs its own CLI session to forward telemetry to the sink")
	}

	sink := newAgentTraceSink(t)
	c := connect(ctx, t, sink.clientOpts()...)

	h := spawnAgent(ctx, t, c, spawnOpts{model: emptyReplayModel, name: "rostered"})

	// A per-run marker, so "the reconstructed handle sees this runtime"
	// cannot pass by coincidence.
	marker := "roster marker " + identity.NewID()
	delivery, err := h.sendNoWait(ctx, t, marker)
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)

	// The empty recording fails the first model call, so the loop lands in
	// FAILED — a state no never-started agent can project, and one that
	// also makes further sends QUEUED rather than STARTED.
	state, err := h.waitFor(ctx, t, "FAILED")
	require.NoError(t, err)
	require.Equal(t, "FAILED", state)

	// (1) The engine published the agent, and the client folded it into a
	// roster entry: identity from the loop span's attributes, state from
	// its log records.
	node := sink.awaitAgent(t, "FAILED")
	require.Equal(t, "rostered", node.Name)

	// (2)+(3) The advertised digest names a call the client holds a payload
	// for, and the whole receiver chain behind it reconstructs — the part
	// that breaks if any ancestor's span never reached the client.
	rebuilt, rebuiltID := sink.rebuild(t, c, node)
	// The rebuilt chain is the honest one spawn pinned, instance ID and
	// all. (It is not byte-identical to what spawn returned: that is the
	// compact handle form of the same value, this is the recipe form.)
	require.Contains(t, rebuiltID.Display(),
		fmt.Sprintf(`agent(id: %q, name: %q)`, node.ID, "rostered"))

	// (4) The reconstructed handle addresses the SAME runtime, not a fresh
	// inert one derived from the same composition.
	require.Equal(t, "rostered", rebuilt.mustRun(ctx, t, `name`).Get("name").String())
	// And it reports the very identity the roster keyed its entry on — the
	// correlation a client needs to tell a rostered agent apart from one it
	// already drives.
	require.Equal(t, node.ID,
		rebuilt.mustRun(ctx, t, `instanceID`).Get("instanceID").String())
	require.Equal(t, "FAILED", rebuilt.state(ctx, t))
	transcript, _ := rebuilt.snapshot(ctx, t)
	require.Contains(t, transcript, marker)

	// And it is sendable, which is the whole point: mail lands in the live
	// mailbox, queued behind the tombstone a resume would drain. An inert
	// agent would have reported STARTED.
	delivery, err = rebuilt.sendNoWait(ctx, t, "sent to an agent this client never spawned")
	require.NoError(t, err)
	require.Equal(t, "QUEUED", delivery)
}

// hirerWorkerPrompt mirrors WorkerPrompt in the agent-hirer fixture: the
// system prompt hire composes into the seed, inside the module call.
const hirerWorkerPrompt = "You are a worker hired by the hirer module."

// TestRosterAddressingFromModule is TestRosterAddressing's headline case:
// the agent the roster exists for is one the user never spawned — a staff
// worker, hired by a chief, born under a module call. hire mints the
// instance inside the module function and hands back only delivery
// evidence, so what the trace advertises is all the client ever learns.
//
// The doubt it settles is whether the reconstruction walk still closes when
// the chain was not assembled by the client's own session. Every frame it
// needs is looked up by digest in the client's DB, which holds only the
// payloads that arrived on spans it ingested (dagql/dagui/extract.go:8-43),
// and the chain here mixes all three kinds: calls the client made, calls the
// MODULE made from its nested session (the system prompt hire composes in,
// and the agent(id:, name:) lookup spawn re-execs), and a module provenance
// frame — pulled in by binding a module object as the seed's toolset, the
// shape a chief's own conversation has. A missing frame does not error at
// spawn time: the roster entry silently degrades to read-only, i.e. the user
// watches a worker they can never talk to.
func (AgentRuntimeSuite) TestRosterAddressingFromModule(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to forward telemetry to the sink")
	}

	sink := newAgentTraceSink(t)
	c := connect(ctx, t, sink.clientOpts()...)

	modDir := t.TempDir()
	copyTestdataFixture(ctx, t, modDir, "modules", "go", "agent-hirer")
	require.NoError(t, c.ModuleSource(modDir).AsModule().Serve(ctx))

	// The client composes a seed and binds the module's own object as its
	// toolset — a chief's shape, and what puts a MODULE-defined call in the
	// agent's chain (the tool argument's literal, walked like a receiver).
	// It then calls hire and learns nothing but delivery evidence: no agent
	// ID ever crosses back.
	hirer, err := testutil.QueryWithClient[struct {
		Hirer struct {
			ID string
		}
	}](c, t, `{ hirer { id } }`, nil)
	require.NoError(t, err)
	seed, err := testutil.QueryWithClient[struct {
		LLM struct {
			WithTools struct {
				ID string
			}
		} `json:"llm"`
	}](c, t, `query($model: String!, $tools: ID!) {
		llm(model: $model) { withTools(object: $tools) { id } }
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"model": emptyReplayModel,
		"tools": hirer.Hirer.ID,
	}})
	require.NoError(t, err)

	// A per-run marker, so "the reconstructed handle sees this runtime"
	// cannot pass by coincidence.
	marker := "module marker " + identity.NewID()
	res, err := testutil.QueryWithClient[struct {
		Hirer struct {
			Hire string
		}
	}](c, t, `query($seed: ID!, $task: String!) {
		hirer { hire(seed: $seed, name: "hired", task: $task) }
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"seed": seed.LLM.WithTools.ID,
		"task": marker,
	}})
	require.NoError(t, err)
	require.Equal(t, "STARTED", res.Hirer.Hire)

	// The empty recording fails the first model call, so the module's agent
	// lands in FAILED — a state no never-started agent projects, and one
	// that makes further sends QUEUED rather than STARTED.
	node := sink.awaitAgent(t, "FAILED")
	require.Equal(t, "hired", node.Name)

	// The loop really is module-internal: its span hangs under the module
	// function's call span, the same place a staff worker's does under its
	// chief's tool call.
	var underModuleCall bool
	sink.read(func(db *dagui.DB) {
		for parent := range node.Span().Parents {
			if pc := parent.Call(); pc != nil && pc.Field == "hire" {
				underModuleCall = true
				break
			}
		}
	})
	require.True(t, underModuleCall, "the loop span must descend from the module call")

	// The walk closes over every kind of frame the chain mixes: the calls
	// the module issued from its own session (the system prompt it composes
	// in, and the pinned agent lookup spawn re-execs), and the module frame
	// hanging off the client's tool binding.
	rebuilt, rebuiltID := sink.rebuild(t, c, node)
	display := rebuiltID.Display()
	require.Contains(t, display, fmt.Sprintf(`agent(id: %q, name: %q)`, node.ID, "hired"))
	require.Contains(t, display, hirerWorkerPrompt,
		"the frame the module built must be in the rebuilt chain")
	mods := rebuiltID.Modules()
	require.NotEmpty(t, mods, "the module frame must resolve")
	require.Equal(t, "hirer", mods[0].Name())

	// And it addresses the live runtime the module started: the marker only
	// this instance ever received is in its transcript, and mail queues
	// behind its tombstone where a fresh inert agent would have reported
	// STARTED.
	require.Equal(t, "hired", rebuilt.mustRun(ctx, t, `name`).Get("name").String())
	require.Equal(t, "FAILED", rebuilt.state(ctx, t))
	transcript, _ := rebuilt.snapshot(ctx, t)
	require.Contains(t, transcript, marker)

	delivery, err := rebuilt.sendNoWait(ctx, t, "sent to an agent this client never spawned")
	require.NoError(t, err)
	require.Equal(t, "QUEUED", delivery)
}

// TestSendAfterSpawnerReleased probes the lifecycle window no other test
// reaches: a module function spawns an agent, the agent SUCCEEDS its first
// turn while the spawning call is still alive, the call returns — and only
// then does the next send arrive. TestSendAwait's second turn has no module
// in the loop's ancestry, and TestRosterAddressingFromModule's worker fails
// its first turn immediately (empty recording), so neither covers
// turn-1-succeeds-then-later-send.
//
// Suspected mechanism (observed once on a live staff-module session, not
// yet reproduced here): AgentRuntime.start launches the loop on
// context.WithoutCancel of the SPAWNING request, keeping that request's
// values — dagql server, query, client metadata. When the spawner is a
// module function call, those values belong to the function call's client,
// which ends with the call — so every later wake of the loop (drainMailbox's
// withPrompt Select, then Step) executes against a released client. On the
// live session a send five minutes after the spawning call ended enqueued
// fine (delivery evidence computed) but the loop never drained: the
// message's await hung forever.
//
// The ingredients here are layered to match that scenario as closely as a
// canned-replay test can, and each was verified to really occur:
//
//   - the spawner is a DANG module function, like modules/staff: the Dang
//     runtime is in-process and its function-call client's connections
//     provably close the moment the call returns (a Go runtime container's
//     nested client conn lingers to session end, masking the window);
//   - hire awaits the opening turn INSIDE the module call, so turn 1
//     completes while the spawning client is alive, exactly as observed
//     live — then stores the worker handle in module state and returns;
//   - the second exchange is module-mediated (ask = resume + send + await,
//     resolving the worker from module state), issued from a fresh
//     function-call client of its own — the staff ask shape.
//
// STATUS: this does NOT currently reproduce the hang — the loop drains and
// steps correctly on the released spawner's retained context, for a direct
// client send and for the module-mediated ask alike, so the test passes and
// stands as the regression probe for this window (a hang fails it by await
// timeout instead of wedging CI). Live-only ingredients still unaccounted
// for: a real provider model (credential/env round-trips through the loop's
// client at step time, where replay needs none), the spawning call being
// dispatched as a tool from another agent's open turn, and the minutes-long
// idle gap before the send (time-based teardown).
//
// One adjacent breakage WAS found while building this (deliberately not
// asserted here): a telemetry-rebuilt recipe ID passed as a module
// function's Agent argument fails to load with `resolve result ID for
// call: no attached result for xxh3:…` after the spawning call's results
// are released — the same handle works via node(id:), so handle-passing
// into module tools breaks where direct addressing survives.
func (AgentRuntimeSuite) TestSendAfterSpawnerReleased(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to forward telemetry to the sink")
	}

	sink := newAgentTraceSink(t)
	c := connect(ctx, t, sink.clientOpts()...)

	modDir := t.TempDir()
	copyTestdataFixture(ctx, t, modDir, "modules", "dang", "agent-hirer")
	require.NoError(t, c.ModuleSource(modDir).AsModule().Serve(ctx))

	const (
		firstReply   = "the first recorded reply"
		secondPrompt = "second prompt after the hire call ended"
		secondReply  = "the second recorded reply"
	)
	// A per-run marker in the task, so the transcript assertion cannot pass
	// by coincidence.
	task := "hire task " + identity.NewID()

	// Unlike the roster test's empty recording, this one SUCCEEDS turn 1
	// and holds a second exchange for the post-hire send. It deliberately
	// does NOT lead with the worker system prompt hire composes in: the
	// replayer drops one leading SYSTEM message from the live history
	// before matching (core/llm_replay.go), and with no synthesized default
	// prompt in play the dropped message is hire's WithSystemPrompt itself.
	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt(task).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: firstReply},
		}).
		WithPrompt(secondPrompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: secondReply},
		}))

	seed, err := testutil.QueryWithClient[struct {
		LLM struct {
			ID string
		} `json:"llm"`
	}](c, t, `query($model: String!) { llm(model: $model) { id } }`,
		&testutil.QueryOptions{Variables: map[string]any{"model": model}})
	require.NoError(t, err)

	// The spawning call: by the time hire returns, the agent has completed
	// a whole successful turn under the module call — hire awaits the
	// opening reply before storing the worker and returning — and the
	// function-call client is on its way out. Only the module object's ID
	// crosses back; the agent handle stays in module state.
	res, err := testutil.QueryWithClient[struct {
		Hirer struct {
			Hire struct {
				ID string
			}
		}
	}](c, t, `query($seed: ID!, $task: String!) {
		hirer { hire(seed: $seed, name: "hired", task: $task) { id } }
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"seed": seed.LLM.ID,
		"task": task,
	}})
	require.NoError(t, err)
	require.NotEmpty(t, res.Hirer.Hire.ID)

	// Turn 1 succeeded: the roster shows the worker IDLE, not FAILED.
	node := sink.awaitAgent(t, "IDLE")
	require.Equal(t, "hired", node.Name)

	// Rebuild the handle from the trace — the only way to address an agent
	// hire never returned an ID for — and confirm it sees the live runtime:
	// the turn this instance (and nothing else) ran is in its transcript.
	rebuilt, _ := sink.rebuild(t, c, node)
	require.Equal(t, "hired", rebuilt.mustRun(ctx, t, `name`).Get("name").String())
	require.Equal(t, "IDLE", rebuilt.state(ctx, t))
	transcript, lastReply := rebuilt.snapshot(ctx, t)
	require.Contains(t, transcript, task)
	require.Equal(t, firstReply, lastReply)

	// The second exchange, issued the way the live scenario issues it:
	// through the module — ask (resume + send + await) runs inside a fresh
	// function-call client of its own, resolving the worker handle from the
	// module's own state, while the LOOP still runs on the released spawner
	// call's context. Bounded: a hang here is the bug, reported as a
	// deadline error rather than a wedged CI run.
	askCtx, cancelAsk := context.WithTimeout(ctx, 90*time.Second)
	defer cancelAsk()
	askRes := map[string]any{}
	err = c.Do(askCtx,
		&dagger.Request{
			Query: `query($hirer: ID!, $message: String!) {
				node(id: $hirer) { ... on Hirer { ask(name: "hired", message: $message) } }
			}`,
			Variables: map[string]any{
				"hirer":   res.Hirer.Hire.ID,
				"message": secondPrompt,
			},
		},
		&dagger.Response{Data: &askRes},
	)
	require.NoError(t, err,
		"the post-hire ask never resolved: the loop's wake ran against the released spawner client")
	raw, err := json.Marshal(askRes)
	require.NoError(t, err)
	require.Equal(t, secondReply, gjson.Get(string(raw), "node.ask").String())
	require.Equal(t, "IDLE", rebuilt.state(ctx, t))
}

// TestAgentArgumentAfterSpawnerReleased pins the adjacent breakage
// TestSendAfterSpawnerReleased's construction uncovered: a telemetry-rebuilt
// recipe ID passed as a module function's `Agent!` ARGUMENT must address the
// live runtime wherever the same handle works via node(id:). It used to fail
// with `resolve result ID for call: no attached result for xxh3:…`
// (dagql resultIDForCall) once the spawning call's results were released:
// rebuilding argument inputs from a stored call frame insisted on an
// already-attached result for the inline recipe instead of falling back to
// evaluating it the way node(id:) does. Handle-passing INTO module tools is
// the staff shape's bread and butter — a chief hands worker handles to
// module functions all day — so direct addressing working while argument
// passing fails is exactly the kind of asymmetry that bites live sessions.
//
// Setup mirrors TestSendAfterSpawnerReleased (Dang spawner, turn 1 succeeds
// inside the module call, spawner client provably released); the difference
// is the second exchange goes through askAgent(agent:) with the REBUILT
// recipe-form handle instead of ask(name:) resolving module state.
func (AgentRuntimeSuite) TestAgentArgumentAfterSpawnerReleased(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to forward telemetry to the sink")
	}

	sink := newAgentTraceSink(t)
	c := connect(ctx, t, sink.clientOpts()...)

	modDir := t.TempDir()
	copyTestdataFixture(ctx, t, modDir, "modules", "dang", "agent-hirer")
	require.NoError(t, c.ModuleSource(modDir).AsModule().Serve(ctx))

	const (
		firstReply   = "the first recorded reply"
		secondPrompt = "second prompt through the handle argument"
		secondReply  = "the second recorded reply"
	)
	task := "hire task " + identity.NewID()

	model := cannedReplayModel(ctx, t, c, c.LLM().
		WithPrompt(task).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: firstReply},
		}).
		WithPrompt(secondPrompt).
		WithResponse([]dagger.LLMContentBlockInput{
			{Kind: dagger.LLMContentBlockKindText, Text: secondReply},
		}))

	seed, err := testutil.QueryWithClient[struct {
		LLM struct {
			ID string
		} `json:"llm"`
	}](c, t, `query($model: String!) { llm(model: $model) { id } }`,
		&testutil.QueryOptions{Variables: map[string]any{"model": model}})
	require.NoError(t, err)

	res, err := testutil.QueryWithClient[struct {
		Hirer struct {
			Hire struct {
				ID string
			}
		}
	}](c, t, `query($seed: ID!, $task: String!) {
		hirer { hire(seed: $seed, name: "hired", task: $task) { id } }
	}`, &testutil.QueryOptions{Variables: map[string]any{
		"seed": seed.LLM.ID,
		"task": task,
	}})
	require.NoError(t, err)
	require.NotEmpty(t, res.Hirer.Hire.ID)

	// Turn 1 succeeded inside the (now released) module call.
	node := sink.awaitAgent(t, "IDLE")
	require.Equal(t, "hired", node.Name)

	// The rebuilt recipe-form handle addresses the live runtime directly —
	// the baseline the argument path is held to.
	rebuilt, _ := sink.rebuild(t, c, node)
	require.Equal(t, "IDLE", rebuilt.state(ctx, t))

	// The same handle as a module function ARGUMENT: askAgent resumes,
	// sends and awaits on the Agent the caller passed in, inside a fresh
	// function-call client. Bounded: a hang here reports as a deadline
	// error rather than wedging CI.
	askCtx, cancelAsk := context.WithTimeout(ctx, 90*time.Second)
	defer cancelAsk()
	askRes := map[string]any{}
	err = c.Do(askCtx,
		&dagger.Request{
			Query: `query($hirer: ID!, $agent: ID!, $message: String!) {
				node(id: $hirer) { ... on Hirer { askAgent(agent: $agent, message: $message) } }
			}`,
			Variables: map[string]any{
				"hirer":   res.Hirer.Hire.ID,
				"agent":   rebuilt.agentID,
				"message": secondPrompt,
			},
		},
		&dagger.Response{Data: &askRes},
	)
	require.NoError(t, err,
		"the rebuilt handle failed as a module function argument where node(id:) works")
	raw, err := json.Marshal(askRes)
	require.NoError(t, err)
	require.Equal(t, secondReply, gjson.Get(string(raw), "node.askAgent").String())
	require.Equal(t, "IDLE", rebuilt.state(ctx, t))
}

// as a boundary: detection walks up to a .git and stops there
// (core/workspace/detect.go:62-81), so an empty one is enough to make the
// session's currentWorkspace this directory rather than whatever the test
// harness happened to inherit — which is the point, since the composition
// under test has to be one the test controls.
func newHostWorkspaceRoot(t *testctx.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dagger.toml"),
		[]byte("# workspace\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "root.txt"),
		[]byte("from workspace root"), 0o600))
	return root
}

// queryID runs a query with no variables and returns the ID at the given
// gjson path. Raw rather than typed because the three workspace shapes below
// differ only in their selection.
func queryID(ctx context.Context, t *testctx.T, c *dagger.Client, query, path string) dagger.ID {
	t.Helper()
	res := map[string]any{}
	require.NoError(t, c.Do(ctx,
		&dagger.Request{Query: query},
		&dagger.Response{Data: &res},
	))
	raw, err := json.Marshal(res)
	require.NoError(t, err)
	out := gjson.Get(string(raw), path)
	require.True(t, out.Exists() && out.String() != "",
		"no ID at %s in response: %s", path, raw)
	return dagger.ID(out.String())
}

// TestRosterAddressingHostWorkspace is TestRosterAddressing with a workspace
// bound into the seed — the one thing every real composition has and the two
// other roster tests happen to lack. A bare llm() seed carries no workspace at
// all, so those tests establish that the mechanism CAN work, not that it works
// for the compositions users actually drive (design §10.2).
//
// The three cases differ only in where the workspace comes from, and that
// alone used to decide whether addressing survived:
//
//   - host directory workspace — host.directory(…).asWorkspace(), a
//     replayable, digest-stable leaf.
//   - session workspace — Query.currentWorkspace, which is NotReplayable and
//     carries PerCallInput/PerSessionInput (core/schema/workspace.go:35-40),
//     i.e. deliberately mints a fresh value on every evaluation.
//   - session workspace overlay — the same, plus an edit, to separate the
//     sparse-overlay machinery from currentWorkspace itself. It was never the
//     overlay: this case failed exactly like the bare one.
//
// What broke was never the walk. Every frame resolves, the ID rebuilds, and
// the handle reads back its own name and instance ID — those are literals in
// the recipe. But a telemetry-rebuilt ID is the RECIPE form (design §9), so
// USING it re-executes the chain; a fresh currentWorkspace meant a fresh seed,
// a different agent value, and — while AgentRuntimes keyed on the agent
// value's content digest — a different registry key. The lookup missed, and a
// miss is indistinguishable from a never-started agent, since Get never
// creates and IDLE-with-seed-snapshot is the honest projection of one. So the
// handle looked healthy and addressed a corpse, and the first send spawned a
// second loop from the seed: the live agent kept running while a fresh,
// history-less one received the user's message.
//
// The registry now keys on the spawn-minted InstanceID (core/agent.go), a
// literal on the pinned chain that survives re-execution whatever the leaves
// do — so all three cases address the live runtime, and the assertions past
// the rebuild are what pins that.
func (AgentRuntimeSuite) TestRosterAddressingHostWorkspace(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to forward telemetry to the sink")
	}

	for _, tc := range []struct {
		name string
		// query selects a workspace ID; the client's workdir is the
		// temp-dir workspace root, so relative host paths resolve to it.
		query  string
		idPath string
	}{
		{
			name:   "host directory workspace",
			query:  `{ host { directory(path: ".") { asWorkspace { id } } } }`,
			idPath: "host.directory.asWorkspace.id",
		},
		{
			name:   "session workspace",
			query:  `{ currentWorkspace { id } }`,
			idPath: "currentWorkspace.id",
		},
		{
			name: "session workspace overlay",
			query: `{ currentWorkspace {
				withNewFile(path: "probe.txt", contents: "probe") { id }
			} }`,
			idPath: "currentWorkspace.withNewFile.id",
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			root := newHostWorkspaceRoot(t)
			sink := newAgentTraceSink(t)
			c := connect(ctx, t, append(sink.clientOpts(), dagger.WithWorkdir(root))...)

			h := spawnAgent(ctx, t, c, spawnOpts{
				model: emptyReplayModel,
				name:  "rostered",
				wsID:  queryID(ctx, t, c, tc.query, tc.idPath),
			})

			// A per-run marker, so "the reconstructed handle sees this
			// runtime" cannot pass by coincidence.
			marker := "roster marker " + identity.NewID()
			delivery, err := h.sendNoWait(ctx, t, marker)
			require.NoError(t, err)
			require.Equal(t, "STARTED", delivery)

			// The empty recording fails the first model call, so the loop
			// lands in FAILED — a state no never-started agent can project,
			// which is what makes IDLE-from-absence detectable at all.
			state, err := h.waitFor(ctx, t, "FAILED")
			require.NoError(t, err)
			require.Equal(t, "FAILED", state)

			node := sink.awaitAgent(t, "FAILED")
			require.Equal(t, "rostered", node.Name)

			// The walk closes: every frame of a workspace-carrying chain
			// reached this client. This is the half that fails loudly when
			// it fails, and it passes in all three cases — a workspace in
			// the seed is not what breaks the reconstruction.
			rebuilt, rebuiltID := sink.rebuild(t, c, node)
			require.Contains(t, rebuiltID.Display(),
				fmt.Sprintf(`agent(id: %q, name: %q)`, node.ID, "rostered"))

			// Read off the chain's own literals. They are asserted before
			// the runtime reads so a failure below cannot be mistaken for a
			// mangled chain.
			require.Equal(t, "rostered",
				rebuilt.mustRun(ctx, t, `name`).Get("name").String())
			require.Equal(t, node.ID,
				rebuilt.mustRun(ctx, t, `instanceID`).Get("instanceID").String())

			// Everything past here comes from the runtime registry rather
			// than the recipe, which is what addressing has to reach. It is
			// the half the value-digest key could not deliver for a
			// currentWorkspace-seeded agent: the rebuilt handle re-executes
			// the chain, so only an identity that rides it as a literal —
			// the instance ID — still names the live entry.
			require.Equal(t, "FAILED", rebuilt.state(ctx, t))
			transcript, _ := rebuilt.snapshot(ctx, t)
			require.Contains(t, transcript, marker)

			// And it is sendable: mail lands in the live mailbox, queued
			// behind the tombstone a resume would drain. An inert agent
			// would have reported STARTED.
			delivery, err = rebuilt.sendNoWait(ctx, t, "sent to an agent this client never spawned")
			require.NoError(t, err)
			require.Equal(t, "QUEUED", delivery)
		})
	}
}

// rebuildDigest turns any advertised call digest into an encoded ID the way a
// resuming client would: walk the call payloads this client has ingested back
// into a chain. The digest need not have its own span: payload-only frames are
// exactly why the call-payload log channel exists.
func (sink *agentTraceSink) rebuildDigest(t *testctx.T, digest string) string {
	t.Helper()
	var encoded string
	sink.read(func(db *dagui.DB) {
		callID, err := db.CallIDForDigest(digest)
		require.NoError(t, err)
		encoded, err = callID.Encode()
		require.NoError(t, err)
	})
	return encoded
}

// TestResumeAnchorRecords covers the two facts a trace has to carry before a
// later session can restore what it shows (hack/designs/resume-from-trace.md
// §4.3, §4.4), neither of which any span could express: both change over the
// loop span's life, and a live span is exported as a snapshot taken at start.
//
//  1. The SNAPSHOT DIGEST — which conversation the agent has actually
//     committed. The test does not merely assert that a digest was published:
//     it rebuilds the conversation from it and reads the transcript, because
//     the whole value of the record is that it names the committed
//     conversation rather than the seed the agent started from, or the
//     half-committed state a scan over the newest LLM call span would find.
//
//  2. The STOP REASON on the terminal record. STOPPED is published both for a
//     stop somebody asked for and for the one session teardown performs on
//     every surviving runtime, and a restoring client must not confuse them —
//     restoring a dismissal as live reverses it, refusing a teardown loses a
//     cleanly closed session. Only EXPLICIT is reachable from a live session;
//     SESSION is covered by TestKillAllStopsWithSessionReason (core), since
//     teardown's own record races the process exiting.
func (AgentRuntimeSuite) TestResumeAnchorRecords(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to forward telemetry to the sink")
	}

	sink := newAgentTraceSink(t)
	c := connect(ctx, t, sink.clientOpts()...)

	h := spawnAgent(ctx, t, c, spawnOpts{model: emptyReplayModel, name: "anchored"})

	// A per-run marker: it is on the committed conversation and on nothing
	// else, so an anchor pointing at the seed cannot pass by coincidence.
	marker := "resume marker " + identity.NewID()
	delivery, err := h.sendNoWait(ctx, t, marker)
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery)

	// The empty recording fails the first model call, so the loop lands in
	// FAILED with the drained message committed — the interrupted-mid-turn
	// shape a resume most needs to get right.
	state, err := h.waitFor(ctx, t, "FAILED")
	require.NoError(t, err)
	require.Equal(t, "FAILED", state)

	node := sink.awaitAgent(t, "FAILED")
	require.Equal(t, "anchored", node.Name)
	require.NotEmpty(t, node.SnapshotDigest,
		"without an anchor the trace cannot be resumed from at all")

	// The anchor names the COMMITTED conversation: rebuilt and read back, it
	// is the history the agent actually holds, marker included.
	encoded := sink.rebuildDigest(t, node.SnapshotDigest)
	res := map[string]any{}
	require.NoError(t, c.Do(ctx,
		&dagger.Request{
			Query:     `query($id: ID!) { node(id: $id) { ... on LLM { transcript } } }`,
			Variables: map[string]any{"id": encoded},
		},
		&dagger.Response{Data: &res},
	))
	raw, err := json.Marshal(res)
	require.NoError(t, err)
	require.Contains(t, gjson.Get(string(raw), "node.transcript").String(), marker)

	// Stopping the FAILED tombstone seals it, and the sealing record is the
	// terminal one: it says a caller asked for this.
	require.Equal(t, "STOPPED", h.mustVerb(ctx, t, `stop`))
	stopped := sink.awaitAgent(t, "STOPPED")
	require.Equal(t, string(core.AgentStopExplicit), stopped.StopReason,
		"a dismissal must be distinguishable from session teardown")
}

// TestResumeAnchorDropsSupersededWorkspaceCommit is the stale-input failure
// that motivated making the agent snapshot anchor portable. A conversation's
// raw receiver chain retains old withWorkspace bindings after a rebind. If one
// of those bindings contains Workspace.withCommit, loading the raw recipe in a
// later session re-evaluates currentWorkspace and then re-runs the old commit.
// Once that commit has been exported, the replay fails with "nothing to commit"
// even though the effective conversation uses the clean, rebound workspace.
//
// The trace anchor must therefore name PortableRecipe's flat conversation, not
// rt.last's historical receiver chain. This test stages and exports a commit,
// retains that now-stale binding below a later clean rebind, publishes the
// agent's anchor, and re-hydrates it in a second session. The old raw anchor
// fails while loading its inputs at withCommit; the portable anchor never
// reaches that superseded call and preserves the conversation.
func (AgentRuntimeSuite) TestResumeAnchorDropsSupersededWorkspaceCommit(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to forward telemetry to the sink")
	}

	workdir := t.TempDir()
	initGitRepo(ctx, t, workdir)
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "resume.txt"), []byte("before\n"), 0o644))
	for _, args := range [][]string{{"add", "resume.txt"}, {"commit", "-m", "base"}} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = workdir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "resume.txt"), []byte("committed\n"), 0o644))

	sink := newAgentTraceSink(t)
	sourceOpts := append(sink.clientOpts(), dagger.WithWorkdir(workdir))
	source := connect(ctx, t, sourceOpts...)

	// Force the staged workspace and the LLM that binds it into attached
	// results before export. Their recipe provenance still records the
	// currentWorkspace.withCommit derivation even when later passed by handle.
	staged := source.CurrentWorkspace().WithCommit(
		"staged before rebind", commitTestDate,
		dagger.WorkspaceWithCommitOpts{Paths: []string{"resume.txt"}},
	)
	stagedID, err := staged.ID(ctx)
	require.NoError(t, err)
	staged = dagger.Ref[*dagger.Workspace](source, stagedID)

	old := source.LLM(dagger.LLMOpts{Model: emptyReplayModel}).
		WithWorkspace(staged).
		WithSystemPrompt("keep the old workspace binding below the rebind")
	oldID, err := old.ID(ctx)
	require.NoError(t, err)
	old = dagger.Ref[*dagger.LLM](source, oldID)

	// The operation on the old binding is now stale: currentWorkspace in any
	// new session sees this commit already in HEAD, so replaying withCommit for
	// resume.txt has nothing left to stage.
	require.NoError(t, staged.Export(ctx))
	status := exec.CommandContext(ctx, "git", "status", "--porcelain")
	status.Dir = workdir
	out, err := status.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Empty(t, string(out))

	marker := "portable anchor " + identity.NewID()
	rebound := old.
		WithWorkspace(source.CurrentWorkspace()).
		WithPrompt(marker)
	reboundID, err := rebound.ID(ctx)
	require.NoError(t, err)

	instanceID := identity.NewID()
	_, err = rehydrateAgent(ctx, source, string(reboundID), instanceID, "portable", "IDLE", "")
	require.NoError(t, err)
	node := sink.awaitAgent(t, "IDLE")
	require.Equal(t, instanceID, node.ID)

	anchor := sink.rebuildDigest(t, node.SnapshotDigest)
	anchorID := new(call.ID)
	require.NoError(t, anchorID.Decode(anchor))
	fields := map[string]bool{}
	collectIDFieldNames(anchorID, fields)
	require.False(t, fields["withCommit"],
		"a trace anchor must not retain a superseded Workspace.withCommit binding")

	// A fresh session is the boundary that taints currentWorkspace and forces
	// recorded dependants to re-execute. Successful re-hydration here is the
	// end-to-end assertion that attach no longer revalidates the stale commit.
	target := connect(ctx, t, dagger.WithWorkdir(workdir))
	restored, err := rehydrateAgent(ctx, target, anchor, instanceID, "portable", "IDLE", "")
	require.NoError(t, err)
	transcript, _ := restored.snapshot(ctx, t)
	require.Contains(t, transcript, marker)
}

// TestRehydrateAdoptsConversation covers the verb a restore is built out of
// (hack/designs/resume-from-trace.md §4.1): an instance whose runtime entry is
// created from a conversation it did not seed, without starting its loop.
//
// spawn is mint-create-pin; rehydrate is adopt-create-pin, and the difference
// is which conversation the entry begins life holding. The assertion that
// matters is therefore not that the call succeeds but that the restored
// agent's snapshot is the conversation handed to it — a restored worker
// answering with no history is exactly the failure this exists to prevent —
// and that prompting it appends to that history rather than to a fresh seed.
func (AgentRuntimeSuite) TestRehydrateAdoptsConversation(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	restored := "restored history " + identity.NewID()
	snapshot := llmWithPrompt(ctx, t, c, emptyReplayModel, restored)
	instanceID := identity.NewID()

	h, err := rehydrateAgent(ctx, c, snapshot, instanceID, "restored", "IDLE", "")
	require.NoError(t, err)

	// The entry exists, holds the adopted conversation, and its loop was
	// never started: a restored agent spends no tokens until it is prompted.
	require.Equal(t, "IDLE", h.state(ctx, t))
	require.Equal(t, instanceID,
		h.mustRun(ctx, t, `instanceID`).Get("instanceID").String())
	transcript, _ := h.snapshot(ctx, t)
	require.Contains(t, transcript, restored,
		"a re-hydrated agent must hold the conversation it adopted, not a fresh seed")

	// Re-hydrating twice is refused rather than silently re-seeding: by the
	// time a second restore arrives the instance may already have stepped,
	// and the late call would discard whatever it built. This is also the
	// guard that makes an out-of-order restore loud (§5.3's ordering).
	_, err = rehydrateAgent(ctx, c, snapshot, instanceID, "restored", "IDLE", "")
	require.ErrorContains(t, err, "already has a runtime entry")

	// Prompting continues that conversation. The empty recording fails the
	// model call, so the turn lands in FAILED — but the message was drained
	// onto the snapshot first, which is what continuity looks like here.
	next := "continuing after the restore " + identity.NewID()
	delivery, err := h.sendNoWait(ctx, t, next)
	require.NoError(t, err)
	require.Equal(t, "STARTED", delivery,
		"a restored IDLE agent takes mail into a new turn, like any idle agent")
	state, err := h.waitFor(ctx, t, "FAILED")
	require.NoError(t, err)
	require.Equal(t, "FAILED", state)

	transcript, _ = h.snapshot(ctx, t)
	require.Contains(t, transcript, restored)
	require.Contains(t, transcript, next)
}

// TestRehydrateStates covers the other half of §4.1: state sets FACTS on the
// entry, never a stored state — the projection stays a projection
// (async-agents §3.4). Each case asserts the fact through behaviour a stored
// label could not fake.
func (AgentRuntimeSuite) TestRehydrateStates(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	newSnapshot := func(t *testctx.T) string {
		return llmWithPrompt(ctx, t, c, emptyReplayModel, "restored "+identity.NewID())
	}

	t.Run("paused queues mail behind the pause", func(ctx context.Context, t *testctx.T) {
		h, err := rehydrateAgent(ctx, c, newSnapshot(t), identity.NewID(), "parked", "PAUSED", "")
		require.NoError(t, err)
		require.Equal(t, "PAUSED", h.state(ctx, t))

		delivery, err := h.sendNoWait(ctx, t, "queued behind the restored pause")
		require.NoError(t, err)
		require.Equal(t, "QUEUED", delivery)
	})

	t.Run("failed preserves the error a resume retries past", func(ctx context.Context, t *testctx.T) {
		loopErr := "provider refused the request " + identity.NewID()
		h, err := rehydrateAgent(ctx, c, newSnapshot(t), identity.NewID(), "broken", "FAILED", loopErr)
		require.NoError(t, err)
		require.Equal(t, "FAILED", h.state(ctx, t))

		// The restored error is a real loop error, not a label: awaiting
		// mail queued behind the tombstone projects it.
		msgID, err := h.sendID(ctx, t, "queued behind the restored failure")
		require.NoError(t, err)
		_, err = h.msgRun(ctx, t, msgID, `await`)
		require.ErrorContains(t, err, loopErr)
	})

	t.Run("stopped preserves a restartable snapshot", func(ctx context.Context, t *testctx.T) {
		snapshot := newSnapshot(t)
		h, err := rehydrateAgent(ctx, c, snapshot, identity.NewID(), "dismissed", "STOPPED", "")
		require.NoError(t, err)
		require.Equal(t, "STOPPED", h.state(ctx, t))

		// The preserved conversation is readable before relaunch.
		transcript, _ := h.snapshot(ctx, t)
		require.Contains(t, transcript, "restored")

		// Sending reopens the restored entry rather than rejecting it or
		// manufacturing a new instance. This snapshot intentionally uses the
		// empty replay model, so only delivery (not turn success) is asserted.
		delivery, err := h.sendNoWait(ctx, t, "sent to a restored tombstone")
		require.NoError(t, err)
		require.Equal(t, "STARTED", delivery)
	})

	t.Run("running is refused", func(ctx context.Context, t *testctx.T) {
		// Nothing restores as RUNNING: the loop died with the old session,
		// and a roster redisplaying it as running would be lying. The client
		// maps it to IDLE with the interrupted turn's input still pending;
		// the engine refuses to be told otherwise.
		_, err := rehydrateAgent(ctx, c, newSnapshot(t), identity.NewID(), "zombie", "RUNNING", "")
		require.ErrorContains(t, err, "cannot be re-hydrated as RUNNING")
	})

	t.Run("an error without FAILED is refused", func(ctx context.Context, t *testctx.T) {
		_, err := rehydrateAgent(ctx, c, newSnapshot(t), identity.NewID(), "confused", "IDLE", "boom")
		require.ErrorContains(t, err, "only be restored with state FAILED")
	})
}

// TestSendRequiresRuntime covers §4.2: a registry miss must be an error, not a
// constructor. Send routed through GetOrCreate, so sending to an instance this
// session has no entry for BOOTED a second loop from the handle's own seed —
// the amnesiac twin of async-agents §10.2, which answered with no history
// while the real agent kept running elsewhere.
//
// Restore makes that failure routine rather than exotic: importing a trace
// puts every agent of the old session on the roster, including any this
// session failed to restore, and focusing one and typing at it must say so.
func (AgentRuntimeSuite) TestSendRequiresRuntime(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ghost := unmintedAgent(ctx, t, c, identity.NewID(), "ghost")

	// Reads still project IDLE-from-absence — a never-started agent and an
	// unrestored one are legitimately indistinguishable to a read.
	require.Equal(t, "IDLE", ghost.state(ctx, t))

	// The write is where it has to be loud.
	_, err := ghost.sendNoWait(ctx, t, "typed at an agent that was never restored")
	require.ErrorContains(t, err, "has no runtime in this session")
}

// TestRehydratePublishesIdentity covers §4.5: telemetry is the directory
// (async-agents §3.3), so an agent that is restored but not started has to
// publish its identity or it is invisible — and an agent a client cannot see
// is an agent it cannot address, which would make the restored session's own
// trace unresumable in turn (§8's chained resume).
//
// rehydrate therefore opens and immediately ends an identity span, and
// publishes its state and snapshot on it. The test asserts the whole loop a
// client actually walks: roster entry, addressable digest, rebuilt handle,
// and — through that handle — the restored conversation.
func (AgentRuntimeSuite) TestRehydratePublishesIdentity(ctx context.Context, t *testctx.T) {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		t.Skip("needs its own CLI session to forward telemetry to the sink")
	}

	sink := newAgentTraceSink(t)
	c := connect(ctx, t, sink.clientOpts()...)

	restored := "restored history " + identity.NewID()
	snapshot := llmWithPrompt(ctx, t, c, emptyReplayModel, restored)
	instanceID := identity.NewID()

	_, err := rehydrateAgent(ctx, c, snapshot, instanceID, "restored", "IDLE", "")
	require.NoError(t, err)

	// The restored agent is on the roster without ever having been started:
	// identity from the span it published, state and anchor from its records.
	node := sink.awaitAgent(t, "IDLE")
	require.Equal(t, instanceID, node.ID)
	require.Equal(t, "restored", node.Name)
	require.NotEmpty(t, node.SnapshotDigest,
		"a restored agent must publish its own anchor, or the new trace cannot be resumed either")

	// And it is addressable from the trace alone, like any spawned agent.
	rebuilt, rebuiltID := sink.rebuild(t, c, node)
	require.Contains(t, rebuiltID.Display(),
		fmt.Sprintf(`agent(id: %q, name: %q)`, instanceID, "restored"))
	transcript, _ := rebuilt.snapshot(ctx, t)
	require.Contains(t, transcript, restored)
}
