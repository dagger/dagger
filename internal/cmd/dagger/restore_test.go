package daggercmd

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/stretchr/testify/require"
)

// The CLI half of `dagger agent --trace`
// (hack/designs/resume-from-trace.md §5.3, §5.4). What is asserted here is
// POLICY, against a fake target and a fake plan source, in the style of
// session_agent_test.go: which flag combinations mean nothing, what order the
// plan is executed in, which agent the prompt ends up pointed at, and what
// happens to an entry the trace does not carry enough to restore.
//
// The engine round trips (rehydrate, attach) and the fetch have their own
// coverage; none of them is reachable — or interesting — without an engine.

// fakeRestorePlan is the frontend seam: a plan, plus the anchors that rebuild.
type fakeRestorePlan struct {
	plan    []dagui.AgentRestore
	anchors map[string]string // snapshot digest -> encoded conversation ID
}

func (f *fakeRestorePlan) AgentRestorePlan() []dagui.AgentRestore { return f.plan }

func (f *fakeRestorePlan) EncodedIDForCallDigest(digest string) (string, error) {
	if id, ok := f.anchors[digest]; ok {
		return id, nil
	}
	// The shape DB.CallIDForDigest reports when a frame's payload never
	// reached this client (§9's first row).
	return "", fmt.Errorf("cannot rebuild ID for %q: call %s never reached this client", "llm", digest)
}

// fakeRestoreTarget records the verbs the plan was executed with, in order.
type fakeRestoreTarget struct {
	calls    []string
	focused  string
	adopted  map[string]string // instance ID -> encoded agent handle
	failOn   string            // instance ID whose Rehydrate fails
	rehydrat map[string]string // instance ID -> snapshot it was re-hydrated from
}

var _ restoreTarget = (*fakeRestoreTarget)(nil)

func newFakeRestoreTarget() *fakeRestoreTarget {
	return &fakeRestoreTarget{
		adopted:  map[string]string{},
		rehydrat: map[string]string{},
	}
}

func (f *fakeRestoreTarget) Rehydrate(_ context.Context, entry dagui.AgentRestore, snapshotID string) (string, error) {
	f.calls = append(f.calls, "rehydrate:"+entry.ID)
	if entry.ID == f.failOn {
		return "", errors.New("already has a runtime entry in this session")
	}
	f.rehydrat[entry.ID] = snapshotID
	return "handle:" + entry.ID, nil
}

func (f *fakeRestoreTarget) Adopt(_ context.Context, entry dagui.AgentRestore, agentID string) error {
	f.calls = append(f.calls, "adopt:"+entry.ID)
	f.adopted[entry.ID] = agentID
	return nil
}

func (f *fakeRestoreTarget) Focus(_ context.Context, entry dagui.AgentRestore, agentID string) error {
	f.calls = append(f.calls, "focus:"+entry.ID)
	f.focused = entry.ID
	return nil
}

// chiefAndWorkers is the shape a restore is usually asked for: a top-level
// conversation with two workers spawned under it, each anchored on a
// conversation whose payloads arrived.
func chiefAndWorkers() *fakeRestorePlan {
	now := time.Unix(1_700_000_000, 0)
	return &fakeRestorePlan{
		plan: []dagui.AgentRestore{
			{
				ID: "agent-chief", Name: "interactive", State: "IDLE",
				SnapshotDigest: "xxh3:chief", LastActivity: now.Add(3 * time.Minute),
			},
			{
				ID: "agent-scout", Name: "scout", State: "IDLE",
				SnapshotDigest: "xxh3:scout", ParentAgentID: "agent-chief",
				LastActivity: now.Add(time.Minute),
			},
			{
				ID: "agent-tests", Name: "tests", State: "STOPPED",
				SnapshotDigest: "xxh3:tests", ParentAgentID: "agent-chief",
				LastActivity: now.Add(2 * time.Minute),
			},
		},
		anchors: map[string]string{
			"xxh3:chief": "llm:chief",
			"xxh3:scout": "llm:scout",
			"xxh3:tests": "llm:tests",
		},
	}
}

func restoreRequest() traceRestore {
	return traceRestore{traceID: "2f123ba77bf7bd2d4db2f70ed20613e8"}
}

// TestRestorePlanRehydratesEverythingBeforeAnythingIsAddressed is §5.3's
// order, and it is load-bearing rather than incidental: the chief's recorded
// chain binds its workers BY ID, so a tool dispatched before a worker's
// re-hydration resolves the handle against a registry that has never heard of
// it (recommendation §6.2's seed race). Attaching a conversation is the first
// thing that can lead to one, so every rehydrate has to precede every attach.
func TestRestorePlanRehydratesEverythingBeforeAnythingIsAddressed(t *testing.T) {
	src, dst := chiefAndWorkers(), newFakeRestoreTarget()
	require.NoError(t, executeRestorePlan(context.Background(), src, dst, restoreRequest()))

	require.Equal(t, []string{
		"rehydrate:agent-chief", "rehydrate:agent-scout", "rehydrate:agent-tests",
		"adopt:agent-chief", "adopt:agent-scout", "adopt:agent-tests",
		"focus:agent-chief",
	}, dst.calls)

	// Each instance is re-hydrated from ITS OWN anchor, rebuilt through the
	// frontend's payloads — mixing two up would restore an agent under
	// somebody else's conversation, silently.
	require.Equal(t, map[string]string{
		"agent-chief": "llm:chief",
		"agent-scout": "llm:scout",
		"agent-tests": "llm:tests",
	}, dst.rehydrat)

	// And each conversation is adopted by the handle rehydrate returned, not
	// by the one the roster advertises: the restored runtime is the new
	// entry, not the dead one the trace describes.
	require.Equal(t, "handle:agent-scout", dst.adopted["agent-scout"])
}

// TestRestoreFocusesTheAgentWithNoAgentAboveIt is §3.1c. A worker's loop span
// is started under its chief's tool-call span, so "top-level" is a fact the
// plan carries; focusing a worker would point the prompt at somebody else's
// employee.
func TestRestoreFocusesTheAgentWithNoAgentAboveIt(t *testing.T) {
	src, dst := chiefAndWorkers(), newFakeRestoreTarget()
	require.NoError(t, executeRestorePlan(context.Background(), src, dst, restoreRequest()))
	require.Equal(t, "agent-chief", dst.focused)
}

// TestRestoreFocusesTheMostRecentlyActiveOfSeveral: with more than one
// top-level agent there is no single right answer, so the rule is "the one
// that was doing something most recently" — NOT the plan's order, which is
// when each agent first appeared. The two disagree here on purpose: the agent
// that appeared first is the one that went quiet first.
func TestRestoreFocusesTheMostRecentlyActiveOfSeveral(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	src := &fakeRestorePlan{
		plan: []dagui.AgentRestore{
			{ID: "agent-first", Name: "interactive", State: "IDLE",
				SnapshotDigest: "xxh3:first", LastActivity: now},
			{ID: "agent-second", Name: "reviewer", State: "IDLE",
				SnapshotDigest: "xxh3:second", LastActivity: now.Add(time.Hour)},
		},
		anchors: map[string]string{"xxh3:first": "llm:first", "xxh3:second": "llm:second"},
	}
	dst := newFakeRestoreTarget()
	require.NoError(t, executeRestorePlan(context.Background(), src, dst, restoreRequest()))
	require.Equal(t, "agent-second", dst.focused)
}

// TestRestoreFocusOverride covers --agent: by display name, by instance ID,
// and the two ways of naming one that cannot be resolved. A name is a label
// two agents may legitimately share, so an ambiguous one is refused rather
// than resolved arbitrarily.
func TestRestoreFocusOverride(t *testing.T) {
	t.Run("by name", func(t *testing.T) {
		src, dst := chiefAndWorkers(), newFakeRestoreTarget()
		req := restoreRequest()
		req.agent = "scout"
		require.NoError(t, executeRestorePlan(context.Background(), src, dst, req))
		require.Equal(t, "agent-scout", dst.focused)
	})

	t.Run("by instance ID", func(t *testing.T) {
		src, dst := chiefAndWorkers(), newFakeRestoreTarget()
		req := restoreRequest()
		req.agent = "agent-tests"
		require.NoError(t, executeRestorePlan(context.Background(), src, dst, req))
		require.Equal(t, "agent-tests", dst.focused)
	})

	t.Run("unknown", func(t *testing.T) {
		src, dst := chiefAndWorkers(), newFakeRestoreTarget()
		req := restoreRequest()
		req.agent = "nobody"
		err := executeRestorePlan(context.Background(), src, dst, req)
		require.ErrorContains(t, err, `no restored agent named "nobody"`)
		require.ErrorContains(t, err, "scout", "the failure must list what there is")
		require.Empty(t, dst.focused)
	})

	t.Run("ambiguous", func(t *testing.T) {
		src, dst := chiefAndWorkers(), newFakeRestoreTarget()
		src.plan[1].Name = "twin"
		src.plan[2].Name = "twin"
		req := restoreRequest()
		req.agent = "twin"
		err := executeRestorePlan(context.Background(), src, dst, req)
		require.ErrorContains(t, err, "name one by instance ID")
		require.ErrorContains(t, err, "agent-scout")
		require.Empty(t, dst.focused)
	})
}

// TestRestoreFailsOnAnUnrestorableAgent is §5.3.3, and the reason it fails
// LOUDLY: a worker the trace cannot restore is exactly the hole the chief's
// next tool dispatch falls into, and with §4.2 that dispatch is an error
// arriving minutes later with none of this context. The refusal names the
// agent and its anchor, and nothing is created before it happens.
func TestRestoreFailsOnAnUnrestorableAgent(t *testing.T) {
	t.Run("the projection refused it", func(t *testing.T) {
		src, dst := chiefAndWorkers(), newFakeRestoreTarget()
		src.plan[1].Err = errors.New(`agent "scout" (agent-scout) published a STOPPED record with no reason`)
		err := executeRestorePlan(context.Background(), src, dst, restoreRequest())
		require.ErrorContains(t, err, "agent-scout")
		require.ErrorContains(t, err, "--partial")
		require.Empty(t, dst.calls, "a refused restore must not create anything")
	})

	t.Run("the anchor does not rebuild", func(t *testing.T) {
		src, dst := chiefAndWorkers(), newFakeRestoreTarget()
		delete(src.anchors, "xxh3:scout")
		err := executeRestorePlan(context.Background(), src, dst, restoreRequest())
		require.ErrorContains(t, err, "agent-scout")
		require.ErrorContains(t, err, "never reached this client")
		require.Empty(t, dst.calls, "a refused restore must not create anything")
	})
}

// TestRestorePartialSkipsExactlyTheUnrestorableOnes: --partial is the opt-in
// to best-effort, and it skips precisely the entries that were refused —
// which is what a plan that refused wholesale could not express.
func TestRestorePartialSkipsExactlyTheUnrestorableOnes(t *testing.T) {
	src, dst := chiefAndWorkers(), newFakeRestoreTarget()
	delete(src.anchors, "xxh3:scout")
	src.plan[2].Err = errors.New("published no snapshot digest")

	req := restoreRequest()
	req.partial = true
	require.NoError(t, executeRestorePlan(context.Background(), src, dst, req))

	require.Equal(t, []string{"rehydrate:agent-chief", "adopt:agent-chief", "focus:agent-chief"},
		dst.calls, "--partial must skip the refused entries and restore the rest")
}

// TestRestoreFailsWhenNothingCanBeRestored: --partial degrades a restore, it
// does not turn one into an empty session that looks like it worked.
func TestRestoreFailsWhenNothingCanBeRestored(t *testing.T) {
	src, dst := chiefAndWorkers(), newFakeRestoreTarget()
	src.anchors = nil

	req := restoreRequest()
	req.partial = true
	err := executeRestorePlan(context.Background(), src, dst, req)
	require.ErrorContains(t, err, "no agent in trace")
	require.Empty(t, dst.calls)
}

// TestRestoreFailsOnAnEmptyPlan: a trace with no agents in it — a CI run, a
// typo'd ID that resolved — must say so rather than drop the user into a
// prompt that restored nothing.
func TestRestoreFailsOnAnEmptyPlan(t *testing.T) {
	dst := newFakeRestoreTarget()
	err := executeRestorePlan(context.Background(), &fakeRestorePlan{}, dst, restoreRequest())
	require.ErrorContains(t, err, "carries no agents to restore")
	require.Empty(t, dst.calls)
}

// TestRestoreStopsOnARefusedRehydration: rehydrate refuses an instance that
// already has a runtime entry (§4.1's existence check), and that refusal means
// the projection was wrong — not that the rest of the plan should be attempted
// against a half-restored session.
func TestRestoreStopsOnARefusedRehydration(t *testing.T) {
	src, dst := chiefAndWorkers(), newFakeRestoreTarget()
	dst.failOn = "agent-scout"

	err := executeRestorePlan(context.Background(), src, dst, restoreRequest())
	require.ErrorContains(t, err, `re-hydrate agent "scout" (agent-scout)`)
	require.ErrorContains(t, err, "already has a runtime entry")
	require.NotContains(t, dst.calls, "adopt:agent-chief",
		"a failed re-hydration must not leave conversations attached to a half-restored session")
}

func TestTraceFetchTimeoutRestoresWhatArrived(t *testing.T) {
	const traceID = "2f123ba77bf7bd2d4db2f70ed20613e8"
	idleTimeout := 5 * time.Second
	req := traceRestore{traceID: traceID, timeout: idleTimeout}

	timedOut, err := fetchTraceForRestore(t.Context(), req,
		func(_ context.Context, gotTraceID string, gotTimeout time.Duration) error {
			require.Equal(t, traceID, gotTraceID)
			require.Equal(t, idleTimeout, gotTimeout)
			return fmt.Errorf("logs: %w", cloud.ErrStreamStalled)
		})
	require.NoError(t, err)
	require.True(t, timedOut)
}

func TestTraceFetchErrorsRemainStrict(t *testing.T) {
	t.Run("stall without timeout", func(t *testing.T) {
		req := restoreRequest()
		_, err := fetchTraceForRestore(t.Context(), req,
			func(context.Context, string, time.Duration) error {
				return fmt.Errorf("logs: %w", cloud.ErrStreamStalled)
			})
		require.ErrorIs(t, err, cloud.ErrStreamStalled)
	})

	t.Run("non-stall with timeout", func(t *testing.T) {
		req := restoreRequest()
		req.timeout = time.Second
		want := errors.New("bad payload")
		_, err := fetchTraceForRestore(t.Context(), req,
			func(context.Context, string, time.Duration) error { return want })
		require.ErrorIs(t, err, want)
	})
}

// TestAgentTraceFlagConflicts is §5.4's surface. Both refusals are about two
// things claiming to say what the session is: a saved session and a trace are
// two stores for one conversation, and a restored session's composition comes
// from the trace rather than from the workspace.
func TestAgentTraceFlagConflicts(t *testing.T) {
	const traceID = "2f123ba77bf7bd2d4db2f70ed20613e8"

	require.NoError(t, validateAgentTraceFlags(traceID, time.Second, false, nil))
	require.NoError(t, validateAgentTraceFlags("", 0, true, []string{"editor"}),
		"the flags only conflict WITH --trace")
	require.ErrorContains(t, validateAgentTraceFlags("", time.Second, false, nil), "requires --trace")
	require.ErrorContains(t, validateAgentTraceFlags(traceID, -time.Second, false, nil), "cannot be negative")

	err := validateAgentTraceFlags(traceID, 0, true, nil)
	require.ErrorContains(t, err, "-r/--resume")

	err = validateAgentTraceFlags(traceID, 0, false, []string{"editor", "dagger-go"})
	require.ErrorContains(t, err, "editor, dagger-go")
	require.ErrorContains(t, err, "come from the trace")
}
