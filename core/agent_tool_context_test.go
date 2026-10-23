package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestComposingAgentContext(t *testing.T) {
	ctx := WithComposingAgent(t.Context(), "family")
	for range 3 {
		require.Equal(t, "family", composingAgentHandle(ctx))
		_, caller := CallerAgent(ctx)
		require.False(t, caller, "composition lineage must not create caller provenance")
		fnCall := newFunctionCall(FunctionCall{composingAgentHandle: composingAgentHandle(ctx)})
		ctx = ContextWithQuery(t.Context(), NewRoot(&mockServer{functionCall: fnCall}))
	}
	require.Equal(t, "family", composingAgentHandle(ctx))
	require.Equal(t, "child", composingAgentHandle(WithComposingAgent(ctx, "child")))
	require.Empty(t, composingAgentHandle(WithComposingAgent(ctx, "")))
}

// An agent loop started from a module call made during another agent's
// composition keeps that call's client. Its own spawns must not inherit the
// composing agent as their parent: the loop is the caller, and never composes.
func TestAgentLoopContextIsNotComposing(t *testing.T) {
	fnCall := newFunctionCall(FunctionCall{composingAgentHandle: "chief"})
	started := ContextWithQuery(t.Context(), NewRoot(&mockServer{functionCall: fnCall}))
	require.Equal(t, "chief", composingAgentHandle(started), "the starting call was made while the chief composed")

	worker, ok := AgentFromContext(testAgentContext(t, t.Context(), "worker", "worker"))
	require.True(t, ok)
	loop := agentLoopContext(started, worker)
	require.Empty(t, composingAgentHandle(loop))
	caller, ok := CallerAgent(loop)
	require.True(t, ok)
	require.Equal(t, "worker", caller.Self().Handle)

	// A spawn composing from inside the loop still names its own agent.
	require.Equal(t, "child", composingAgentHandle(WithComposingAgent(loop, "child")))
}

// The composing agent is execution context, not a recorded input: module calls
// made while composing two different agents share their cache entries. Lineage
// follows the execution, so a call that executes spawns under the agent being
// composed, and a cache hit, executing nothing, still names its first child.
func TestComposingAgentCallCache(t *testing.T) {
	var newLease func(engine.ClientLeaseKind, string) *engine.ClientLifecycleLease
	newLease = func(kind engine.ClientLeaseKind, owner string) *engine.ClientLifecycleLease {
		return engine.NewClientLifecycleLease(kind, owner, nil, func(kind engine.ClientLeaseKind, owner string) (*engine.ClientLifecycleLease, error) {
			return newLease(kind, owner), nil
		})
	}
	lease := newLease(engine.ClientLeaseRequest, "test")
	t.Cleanup(lease.Release)
	scope, err := engine.NewClientScope(&engine.ClientMetadata{ClientID: "client", SessionID: "session"}, lease)
	require.NoError(t, err)
	ctx, err := engine.ContextWithClientScope(t.Context(), scope)
	require.NoError(t, err)
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	srv, err := dagql.NewServer(ctx, &Query{})
	require.NoError(t, err)
	srv.InstallObject(dagql.NewClass[*Agent](srv))
	srv.InstallObject(dagql.NewClass[*Module](srv))
	mod, err := dagql.NewObjectResultForCall(&Module{}, srv, testResultCall("module", &Module{}, nil))
	require.NoError(t, err)
	fn := &ModuleFunction{mod: mod, metadata: &Function{}}
	registry := NewAgentRuntimes()
	t.Cleanup(func() { require.NoError(t, registry.KillAll(context.Background(), nil)) })
	for _, handle := range []string{"parent-A", "parent-B"} {
		parentCtx := testAgentContext(t, ctx, handle, handle)
		parent, _ := AgentFromContext(parentCtx)
		_, err := registry.Create(parentCtx, parent, AgentStateIdle, "", false, "")
		require.NoError(t, err)
	}
	calls := 0
	spawnHelper := func(ctx context.Context, _ dagql.ObjectResult[*Query], _ struct{}) (dagql.ObjectResult[*Agent], error) {
		calls++
		child, err := dagql.NewObjectResultForCurrentCall(ctx, srv, &Agent{Handle: fmt.Sprintf("child-%d", calls), Name: "child"})
		if err != nil {
			return child, err
		}
		_, err = registry.Create(ctx, child, AgentStateIdle, "", false, "")
		return child, err
	}
	// No public arguments: nothing distinguishes the same helper invoked by
	// the same entry while composing two different agents.
	cached := dagql.NodeFunc("cachedSpawn", spawnHelper)
	cached.Spec.GetDynamicInput = fn.DynamicInputsForCall
	fresh := dagql.NodeFunc("freshSpawn", spawnHelper).DoNotCache("spawns on every call")
	fresh.Spec.GetDynamicInput = fn.DynamicInputsForCall
	dagql.Fields[*Query]{cached, fresh}.Install(srv)

	composing := func(handle string) context.Context {
		return WithComposingAgent(ctx, handle)
	}
	spawnUnder := func(field, handle string) (string, string) {
		var child dagql.ObjectResult[*Agent]
		require.NoError(t, srv.Select(composing(handle), srv.Root(), &child, dagql.Selector{Field: field}))
		rt, err := registry.Require(ctx, child)
		require.NoError(t, err)
		return child.Self().Handle, rt.ParentHandle()
	}

	child, parent := spawnUnder("cachedSpawn", "parent-A")
	require.Equal(t, "parent-A", parent, "a spawn executed during composition belongs to the composing agent")
	again, parent := spawnUnder("cachedSpawn", "parent-B")
	require.Equal(t, 1, calls, "composing another agent must not miss the cache")
	require.Equal(t, child, again, "a cache hit does not spawn again")
	require.Equal(t, "parent-A", parent)

	var recipe dagql.ObjectResult[*Agent]
	require.NoError(t, srv.Select(composing("parent-B"), srv.Root(), &recipe, dagql.Selector{Field: "cachedSpawn"}))
	id, err := recipe.RecipeID(ctx)
	require.NoError(t, err)
	require.Empty(t, id.Args(), "the composition is not recorded on module calls")

	for _, handle := range []string{"parent-A", "parent-B"} {
		_, parent := spawnUnder("freshSpawn", handle)
		require.Equal(t, handle, parent, "a spawn that executes follows the agent being composed")
	}
}

func TestAgentToolCallContext(t *testing.T) {
	ctx := t.Context()
	require.False(t, IsAgentToolCall(ctx))
	m := &MCP{}
	called := false
	_, failed := m.Call(ctx, []LLMTool{{
		Name: "push",
		Call: func(ctx context.Context, _ any) (any, error) {
			called = true
			require.True(t, IsAgentToolCall(ctx))
			return "ok", nil
		},
	}}, &LLMToolCall{Name: "push"})
	require.True(t, called)
	require.False(t, failed)
	require.False(t, IsAgentToolCall(ctx), "tool scope must not leak back into caller")
}
