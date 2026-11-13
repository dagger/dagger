package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/stretchr/testify/require"
)

type leaseCheckedRuntime struct {
	*controlledEditRuntime
	editing *atomic.Bool
}

func (r *leaseCheckedRuntime) Reseed(ctx context.Context, llm *core.LLM) error {
	if r.editing.Load() {
		return errors.New("conversation edit lease active")
	}
	return r.fakeRuntime.Reseed(ctx, llm)
}

// contextUseObserver closes used when something first waits on the context.
// ResetWorkspace takes its save/reload guard before it uses its context.
type contextUseObserver struct {
	context.Context
	once sync.Once
	used chan struct{}
}

func (c *contextUseObserver) Done() <-chan struct{} {
	c.once.Do(func() { close(c.used) })
	return c.Context.Done()
}

func TestResetWorkspaceWaitsForBoundaryEdit(t *testing.T) {
	for _, owned := range []bool{false, true} {
		for _, cancelReload := range []bool{false, true} {
			name := "attached"
			if owned {
				name = "owned"
			}
			if cancelReload {
				name += "/cancel"
			} else {
				name += "/wait"
			}
			t.Run(name, func(t *testing.T) {
				a, baseRT, queries := idleAgent(t, map[string]any{
					"model": "test-model", "id": "workspace-snapshot", "sync": "reloaded-llm",
				})
				var editing atomic.Bool
				controlled := &controlledEditRuntime{fakeRuntime: baseRT}
				rt := &leaseCheckedRuntime{controlledEditRuntime: controlled, editing: &editing}
				controlled.begin = func(ctx context.Context, token string) (core.ID, error) {
					editing.Store(true)
					return baseRT.BeginEdit(ctx, token)
				}
				controlled.commit = func(ctx context.Context, token string, llm *core.LLM) error {
					editing.Store(false)
					return baseRT.CommitEdit(ctx, token, llm)
				}
				controlled.abort = func(ctx context.Context, token string) error {
					editing.Store(false)
					return baseRT.AbortEdit(ctx, token)
				}
				a.bindRuntime(rt, "same-agent", "", owned)
				before, _ := a.llmSnapshot()
				ready, release := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				unblock := func() { releaseOnce.Do(func() { close(release) }) }
				t.Cleanup(unblock)
				editDone := make(chan error, 1)
				go func() {
					editDone <- a.editAtBoundary(t.Context(), "controlled compaction", func(ctx context.Context, llm *core.LLM) (*core.LLM, error) {
						close(ready)
						select {
						case <-release:
							return llm.WithModel("edited-model"), nil
						case <-ctx.Done():
							return nil, context.Cause(ctx)
						}
					})
				}()
				select {
				case <-ready:
				case <-time.After(5 * time.Second):
					t.Fatal("boundary edit did not start")
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				reloadCtx := &contextUseObserver{Context: ctx, used: make(chan struct{})}
				reloadDone := make(chan error, 1)
				go func() { reloadDone <- a.ResetWorkspace(reloadCtx) }()
				select {
				case <-reloadCtx.used:
				case err := <-reloadDone:
					t.Fatalf("reload returned before waiting for the boundary edit: %v", err)
				case <-time.After(5 * time.Second):
					t.Fatal("reload did not start waiting for the boundary edit")
				}
				// Reload refuses to run if it finds its guard held, so this looks only
				// after reload has taken it, and only once.
				if a.syncOpL.TryLock() {
					a.syncOpL.Unlock()
					t.Fatal("reload must retain its save/reload guard while waiting")
				}
				select {
				case err := <-reloadDone:
					t.Fatalf("reload bypassed the active boundary edit: %v", err)
				case <-time.After(25 * time.Millisecond):
				}
				require.False(t, queries.sawChain("currentWorkspace"), "reload must not capture/rebind before obtaining the conversation gate")
				require.ErrorContains(t, a.ResetWorkspace(t.Context()), "another save/reload")
				if cancelReload {
					cancel()
					require.ErrorIs(t, <-reloadDone, context.Canceled)
					after, _ := a.llmSnapshot()
					require.Same(t, before, after)
					require.Nil(t, a.lastSynced())
					require.True(t, a.syncOpL.TryLock(), "canceled reload must release synchronization guard")
					a.syncOpL.Unlock()
					unblock()
					require.NoError(t, <-editDone)
					require.Zero(t, rt.reseedCount())
				} else {
					unblock()
					require.NoError(t, <-editDone)
					require.NoError(t, <-reloadDone)
					require.True(t, queries.sawChain("withModel", "withWorkspace", "sync"), "reload must retain the just-committed edit")
					require.Equal(t, []string{"beginEdit", "commitEdit", "reseed"}, rt.verbLog())
					require.NotNil(t, a.lastSynced())
				}
				require.Same(t, rt, a.runtime())
				require.Equal(t, "same-agent", a.agentHandle)
				require.Equal(t, owned, a.owned)
				_, _, stops := rt.counts()
				require.Zero(t, stops, "waiting for compaction must never stop or fork the runtime")
			})
		}
	}
}

func TestBranchAndRewindWaitForBoundaryEdit(t *testing.T) {
	for _, operation := range []string{"branch", "summary branch", "rewind"} {
		t.Run(operation, func(t *testing.T) {
			a, baseRT, _ := idleAgent(t, map[string]any{
				"model": "test-model", "transcript": "old conversation", "contextWindow": 0, "lastReply": "summary",
			})
			var editing atomic.Bool
			editing.Store(true)
			rt := &leaseCheckedRuntime{controlledEditRuntime: &controlledEditRuntime{fakeRuntime: baseRT}, editing: &editing}
			a.bindRuntime(rt, "same-agent", "", true)
			unlock, err := a.lockConversation(t.Context())
			require.NoError(t, err)
			var releaseOnce sync.Once
			unblock := func() {
				releaseOnce.Do(func() {
					editing.Store(false)
					unlock()
				})
			}
			defer unblock()
			done := make(chan error, 1)
			target := core.Ref[*core.LLM](core.NewQuery(a.session.dag), "branch-target")
			go func() {
				if operation == "rewind" {
					done <- a.Rewind(t.Context(), target)
				} else {
					done <- a.Branch(t.Context(), target, idtui.BranchSummary{Summarize: operation == "summary branch"})
				}
			}()
			select {
			case err := <-done:
				t.Fatalf("replacement bypassed pending boundary edit: %v", err)
			case <-time.After(25 * time.Millisecond):
			}
			unblock()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("replacement deadlocked while reading/adopting its snapshot")
			}
			require.Same(t, rt, a.runtime())
			require.Equal(t, "same-agent", a.agentHandle)
			require.Equal(t, []string{"reseed"}, rt.verbLog())
			_, _, stops := rt.counts()
			require.Zero(t, stops)
		})
	}
}

func TestPrivateAgentEditProtocol(t *testing.T) {
	var fields []string
	token := `token-with-"quotes"`
	dag, err := dagger.Connect(t.Context(), dagger.WithConn(agentTestConn{do: func(req *http.Request) (*http.Response, error) {
		var query dagger.Request
		require.NoError(t, json.NewDecoder(req.Body).Decode(&query))
		variables, _ := query.Variables.(map[string]any)
		body := `{"data":{"node":{"id":"agent-id"}}}`
		switch {
		case strings.Contains(query.Query, "__beginEdit"):
			fields = append(fields, "begin")
			require.Equal(t, token, variables["token"])
			require.Contains(t, query.Query, "{ id }")
			body = `{"data":{"node":{"edit":{"id":"boundary-id"}}}}`
		case strings.Contains(query.Query, "__commitEdit"):
			fields = append(fields, "commit")
			require.Equal(t, token, variables["token"])
			require.Equal(t, "conversation-id", variables["conversation"])
			require.NotContains(t, query.Query, "{ id }")
			body = `{"data":{"node":{"edit":"agent-id"}}}`
		case strings.Contains(query.Query, "__abortEdit"):
			fields = append(fields, "abort")
			require.Equal(t, token, variables["token"])
			require.NotContains(t, query.Query, "{ id }")
			body = `{"data":{"node":{"edit":"agent-id"}}}`
		case strings.Contains(query.Query, `id:"conversation"`):
			body = `{"data":{"node":{"id":"conversation-id"}}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}}))
	require.NoError(t, err)
	t.Cleanup(func() { dag.Close() })
	rt := liveAgent{dag: dag, agent: core.Ref[*core.Agent](core.NewQuery(dag), "agent")}
	id, err := rt.BeginEdit(t.Context(), token)
	require.NoError(t, err)
	require.Equal(t, core.ID("boundary-id"), id)
	require.NoError(t, rt.CommitEdit(t.Context(), token, core.Ref[*core.LLM](core.NewQuery(dag), "conversation")))
	require.NoError(t, rt.AbortEdit(t.Context(), token))
	require.Equal(t, []string{"begin", "commit", "abort"}, fields)
}

type waitingEditMessage struct {
	started chan struct{}
	release chan struct{}
}

func (m *waitingEditMessage) Delivery(context.Context) (core.AgentMessageDelivery, error) {
	return core.AgentMessageDeliveryStarted, nil
}

func (m *waitingEditMessage) Response(ctx context.Context) (string, error) {
	close(m.started)
	select {
	case <-m.release:
		return "done", nil
	case <-ctx.Done():
		return "", context.Cause(ctx)
	}
}

type responseWaitRuntime struct {
	*fakeRuntime
	message *waitingEditMessage
}

func (r *responseWaitRuntime) SendMessage(ctx context.Context, message string) (agentMessage, error) {
	if _, err := r.fakeRuntime.SendMessage(ctx, message); err != nil {
		return nil, err
	}
	return r.message, nil
}

func TestWithPromptAllowsBoundaryEditDuringResponse(t *testing.T) {
	a, baseRT, _ := idleAgent(t, map[string]any{"model": "test-model"})
	a.autoCompact = false
	rt := &responseWaitRuntime{fakeRuntime: baseRT, message: &waitingEditMessage{started: make(chan struct{}), release: make(chan struct{})}}
	a.bindRuntime(rt, "same-agent", "", true)
	turnDone := make(chan error, 1)
	go func() { turnDone <- a.WithPrompt(t.Context(), "original prompt") }()
	select {
	case <-rt.message.started:
	case <-time.After(5 * time.Second):
		t.Fatal("prompt did not start waiting for response")
	}
	commandDone := make(chan error, 1)
	go func() { commandDone <- a.Model(t.Context(), "test-model") }()
	select {
	case err := <-commandDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("response waiter held the conversation mutation gate")
	}
	select {
	case err := <-turnDone:
		t.Fatalf("editing ended the original response wait: %v", err)
	default:
	}
	close(rt.message.release)
	require.NoError(t, <-turnDone)
	require.Equal(t, []string{"send", "resume", "beginEdit", "commitEdit"}, rt.verbLog())
	require.Equal(t, []string{"original prompt"}, rt.sent)
}

func (f *fakeRuntime) BeginEdit(context.Context, string) (core.ID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verbs = append(f.verbs, "beginEdit")
	return f.snapshot, nil
}

func (f *fakeRuntime) CommitEdit(context.Context, string, *core.LLM) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verbs = append(f.verbs, "commitEdit")
	return nil
}

func (f *fakeRuntime) AbortEdit(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verbs = append(f.verbs, "abortEdit")
	return nil
}

type controlledEditRuntime struct {
	*fakeRuntime
	begin      func(context.Context, string) (core.ID, error)
	commit     func(context.Context, string, *core.LLM) error
	abort      func(context.Context, string) error
	snapshotFn func(context.Context) (core.ID, error)
}

func (r *controlledEditRuntime) SnapshotID(ctx context.Context) (core.ID, error) {
	if r.snapshotFn != nil {
		return r.snapshotFn(ctx)
	}
	return r.fakeRuntime.SnapshotID(ctx)
}

func (r *controlledEditRuntime) BeginEdit(ctx context.Context, token string) (core.ID, error) {
	if r.begin != nil {
		return r.begin(ctx, token)
	}
	return r.fakeRuntime.BeginEdit(ctx, token)
}

func (r *controlledEditRuntime) CommitEdit(ctx context.Context, token string, llm *core.LLM) error {
	if r.commit != nil {
		return r.commit(ctx, token, llm)
	}
	return r.fakeRuntime.CommitEdit(ctx, token, llm)
}

func (r *controlledEditRuntime) AbortEdit(ctx context.Context, token string) error {
	if r.abort != nil {
		return r.abort(ctx, token)
	}
	return r.fakeRuntime.AbortEdit(ctx, token)
}

func TestBoundaryCommandsPreserveActiveTurn(t *testing.T) {
	for _, state := range []core.AgentState{core.AgentStateIdle, core.AgentStatePaused, core.AgentStateRunning, core.AgentStateWaitingInput, core.AgentStateFailed} {
		for _, operation := range []string{"model", "effort", "compact"} {
			t.Run(string(state)+"/"+operation, func(t *testing.T) {
				a, rt, _ := idleAgent(t, map[string]any{"model": "test-model", "reasoningEffort": "high", "lastReply": "summary"})
				rt.setState(state)
				a.owned = false // attached workers obey exactly the same edit policy
				turnCtx, cancel := context.WithCancelCause(t.Context())
				defer cancel(nil)
				a.beginTurn(cancel)
				defer a.endTurn()
				var err error
				switch operation {
				case "model":
					err = a.Model(t.Context(), "test-model", core.LLMWithModelOpts{Provider: "local"})
				case "effort":
					err = a.Effort(t.Context(), "high")
				case "compact":
					err = a.CompactInPlace(t.Context())
				}
				require.NoError(t, err)
				require.NoError(t, turnCtx.Err(), "editing must not cancel the active response waiter")
				require.Equal(t, []string{"beginEdit", "commitEdit"}, rt.verbLog())
				require.Same(t, rt, a.runtime())
				require.Equal(t, "agent-chief", a.agentHandle)
				gotState, err := rt.State(t.Context())
				require.NoError(t, err)
				require.Equal(t, state, gotState)
				sent, interrupted, stopped := rt.counts()
				require.Zero(t, sent)
				require.Zero(t, interrupted)
				require.Zero(t, stopped)
				require.Zero(t, rt.reseedCount())
			})
		}
	}
}

func TestBoundaryEditCancellationReleasesLease(t *testing.T) {
	for _, stage := range []string{"begin", "transform", "commit"} {
		t.Run(stage, func(t *testing.T) {
			a, baseRT, _ := idleAgent(t, map[string]any{"model": "test-model", "id": "boundary-snapshot"})
			before, _ := a.llmSnapshot()
			rt := &controlledEditRuntime{fakeRuntime: baseRT}
			a.bindRuntime(rt, "agent-chief", "attached", false)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var token string
			aborted := 0
			rt.begin = func(ctx context.Context, value string) (core.ID, error) {
				token = value
				require.NotEmpty(t, token)
				if stage == "begin" {
					cancel()
					return "", context.Cause(ctx)
				}
				return "boundary-snapshot", nil
			}
			rt.commit = func(ctx context.Context, value string, _ *core.LLM) error {
				require.Equal(t, token, value)
				require.Equal(t, "commit", stage)
				cancel()
				return context.Cause(ctx)
			}
			rt.abort = func(cleanup context.Context, value string) error {
				aborted++
				require.NoError(t, cleanup.Err(), "cleanup must outlive the canceled command")
				_, bounded := cleanup.Deadline()
				require.True(t, bounded)
				require.Equal(t, token, value)
				return nil
			}
			err := a.editAtBoundary(ctx, "test edit", func(ctx context.Context, llm *core.LLM) (*core.LLM, error) {
				id, err := llm.ID(ctx)
				require.NoError(t, err)
				require.Equal(t, core.ID("boundary-snapshot"), id, "edit the boundary snapshot, not the stale client seed")
				if stage == "transform" {
					cancel()
					return nil, context.Cause(ctx)
				}
				return llm.WithModel("test-model"), nil
			})
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, 1, aborted)
			after, _ := a.llmSnapshot()
			require.Same(t, before, after)
			require.Same(t, rt, a.runtime(), "failure must never detach or fork")
			sent, interrupted, stopped := rt.counts()
			require.Zero(t, sent)
			require.Zero(t, interrupted)
			require.Zero(t, stopped)
		})
	}
}

func TestBoundaryEditFailureKeepsOriginalConversation(t *testing.T) {
	a, baseRT, _ := idleAgent(t, map[string]any{"model": "test-model"})
	before, _ := a.llmSnapshot()
	wantErr := errors.New("summary failed")
	err := a.editAtBoundary(t.Context(), "test edit", func(context.Context, *core.LLM) (*core.LLM, error) {
		return nil, wantErr
	})
	require.ErrorIs(t, err, wantErr)
	after, _ := a.llmSnapshot()
	require.Same(t, before, after)
	require.Same(t, baseRT, a.runtime())
	require.Equal(t, []string{"beginEdit", "abortEdit"}, baseRT.verbLog())
}

func TestBoundaryEditSerializesOpeningAndSnapshotAdoption(t *testing.T) {
	a, baseRT, _ := idleAgent(t, map[string]any{"model": "test-model", "id": "committed-edit"})
	a.detachAgent()
	// Hold the exact gate prompt preparation owns while the runtime is absent.
	releasePreparation, err := a.lockConversation(t.Context())
	require.NoError(t, err)
	began := make(chan struct{})
	allowCommit := make(chan struct{})
	rt := &controlledEditRuntime{fakeRuntime: baseRT}
	rt.begin = func(context.Context, string) (core.ID, error) {
		close(began)
		return "latest-boundary", nil
	}
	rt.commit = func(ctx context.Context, _ string, _ *core.LLM) error {
		select {
		case <-allowCommit:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
		rt.mu.Lock()
		rt.snapshot = "committed-edit"
		rt.mu.Unlock()
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- a.Model(t.Context(), "test-model") }()
	select {
	case <-began:
		t.Fatal("edit ran before prompt preparation handed off its runtime")
	case err := <-done:
		t.Fatalf("edit bypassed opening preparation: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	a.bindRuntime(rt, "same-agent", "", true)
	releasePreparation()
	select {
	case <-began:
	case <-time.After(5 * time.Second):
		t.Fatal("edit did not acquire prepared runtime")
	}
	// A response waiter arriving during the edit must fetch its snapshot only
	// after commit, or it could overwrite the new model with the old seed.
	snapshotUsed := make(chan core.ID, 1)
	rt.snapshotFn = func(ctx context.Context) (core.ID, error) {
		id, err := rt.fakeRuntime.SnapshotID(ctx)
		snapshotUsed <- id
		return id, err
	}
	synced := make(chan error, 1)
	go func() { synced <- a.syncFromAgent(rt) }()
	select {
	case err := <-synced:
		t.Fatalf("snapshot adoption bypassed pending edit: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(allowCommit)
	require.NoError(t, <-done)
	require.NoError(t, <-synced)
	require.Equal(t, core.ID("committed-edit"), <-snapshotUsed)
	llm, _ := a.llmSnapshot()
	id, err := llm.ID(t.Context())
	require.NoError(t, err)
	require.Equal(t, core.ID("committed-edit"), id)
}

func TestConversationGateWaitIsCancelable(t *testing.T) {
	a := &sessionAgent{}
	unlock, err := a.lockConversation(t.Context())
	require.NoError(t, err)
	defer unlock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = a.lockConversation(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestClearFailureKeepsRuntimeAndConversation(t *testing.T) {
	_, a, rt := focusedWorker(t)
	rt.setState(core.AgentStateRunning)
	wantErr := errors.New("clear refused")
	rt.reseedErr = wantErr
	before, _ := a.llmSnapshot()
	require.ErrorIs(t, a.Clear(t.Context()), wantErr)
	after, _ := a.llmSnapshot()
	require.Same(t, before, after)
	require.Same(t, rt, a.runtime())
	_, interrupts, stops := rt.counts()
	require.Equal(t, 1, interrupts)
	require.Zero(t, stops)
}

func TestStopForLogoutStopsAttachedRuntime(t *testing.T) {
	for _, owned := range []bool{false, true} {
		s, agents := testSession(t, "focused", "unrelated")
		a := agents[0]
		rt := runtimeOf(t, a)
		a.owned = owned
		require.NoError(t, a.StopForLogout(t.Context()))
		_, interrupts, stops := rt.counts()
		require.Equal(t, 1, interrupts)
		require.Equal(t, 1, stops)
		require.Same(t, rt, a.runtime(), "keep the stopped runtime's identity")
		_, _, otherStops := runtimeOf(t, agents[1]).counts()
		require.Zero(t, otherStops)
		require.Same(t, a, s.Target())
	}
}

func TestLogoutWaitsForOpeningTurn(t *testing.T) {
	s, agents := testSession(t, "focused")
	a := agents[0]
	a.detachAgent()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	a.beginTurn(cancel)
	release, err := a.lockConversation(t.Context())
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- a.StopForLogout(t.Context()) }()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("logout did not cancel the opening turn")
	}
	rt := newFakeRuntime()
	a.bindRuntime(rt, "spawned-before-cancellation", "", false)
	release()
	a.endTurn()
	require.NoError(t, <-done)
	_, _, stops := rt.counts()
	require.Equal(t, 1, stops)
	require.Same(t, a, s.Target())
}

// Ensure tests do not accidentally add a second independently initialized gate
// to one conversation while exercising concurrent first use.
func TestConversationGateInitializesOnce(t *testing.T) {
	a := &sessionAgent{}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := a.lockConversation(t.Context())
			require.NoError(t, err)
			unlock()
		}()
	}
	wg.Wait()
}
