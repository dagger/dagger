package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

// A function call that read its owner's secret for an agent keeps its own
// result from being reused, but never expires a result the cache already
// holds for everyone, such as a build it returns.
func TestDeclineOwnerSecretReuse(t *testing.T) {
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

	marked := newFunctionCall(FunctionCall{})
	marked.MarkOwnerSecretRead()
	runs := map[string]int{}
	object := func(ctx context.Context, name string) (dagql.ObjectResult[*Agent], error) {
		runs[name]++
		return dagql.NewObjectResultForCurrentCall(ctx, srv, &Agent{Handle: fmt.Sprintf("%s-%d", name, runs[name])})
	}
	dagql.Fields[*Query]{
		// build stands for a Container another call built, held by the cache.
		dagql.NodeFunc("build", func(ctx context.Context, _ dagql.ObjectResult[*Query], _ struct{}) (dagql.ObjectResult[*Agent], error) {
			return object(ctx, "build")
		}),
		// returnsBuild read the owner's secret and returns build.
		dagql.NodeFunc("returnsBuild", func(ctx context.Context, _ dagql.ObjectResult[*Query], _ struct{}) (dagql.ObjectResult[*Agent], error) {
			runs["returnsBuild"]++
			var built dagql.ObjectResult[*Agent]
			if err := srv.Select(ctx, srv.Root(), &built, dagql.Selector{Field: "build"}); err != nil {
				return built, err
			}
			declineOwnerSecretReuse(ctx, marked, built)
			return built, nil
		}),
		// derived read the owner's secret and returns a value of its own.
		dagql.NodeFunc("derived", func(ctx context.Context, _ dagql.ObjectResult[*Query], _ struct{}) (dagql.ObjectResult[*Agent], error) {
			res, err := object(ctx, "derived")
			if err == nil {
				declineOwnerSecretReuse(ctx, marked, res)
			}
			return res, err
		}),
		// unmarked read nothing of its owner's.
		dagql.NodeFunc("unmarked", func(ctx context.Context, _ dagql.ObjectResult[*Query], _ struct{}) (dagql.ObjectResult[*Agent], error) {
			res, err := object(ctx, "unmarked")
			if err == nil {
				declineOwnerSecretReuse(ctx, newFunctionCall(FunctionCall{}), res)
			}
			return res, err
		}),
	}.Install(srv)
	selectTwice := func(field string) {
		for range 2 {
			var res dagql.ObjectResult[*Agent]
			require.NoError(t, srv.Select(ctx, srv.Root(), &res, dagql.Selector{Field: field}))
		}
	}

	selectTwice("build")
	selectTwice("returnsBuild")
	selectTwice("build")
	require.Equal(t, 1, runs["build"], "returning a held build must not expire it for everyone")

	selectTwice("derived")
	require.Equal(t, 2, runs["derived"], "a result derived from the owner's secret is not reused")
	selectTwice("unmarked")
	require.Equal(t, 1, runs["unmarked"])
}

// The same module call made by two agents, or by an agent and by no agent,
// never shares one execution: each may get a different answer from the owner.
func TestAgentConcurrencyKey(t *testing.T) {
	key := func(ctx context.Context) string {
		req := &dagql.CallRequest{ResultCall: &dagql.ResultCall{}, ConcurrencyKey: "session"}
		agentConcurrencyKey(ctx, req)
		return req.ConcurrencyKey
	}
	ctx := t.Context()
	require.Equal(t, "session", key(ctx), "a call no agent makes keeps the session's key")
	first := key(testAgentContext(t, ctx, "agent-1", "worker"))
	second := key(testAgentContext(t, ctx, "agent-2", "worker"))
	require.NotEqual(t, "session", first)
	require.NotEqual(t, first, second, "agents named alike are still apart")
	require.Equal(t, first, key(testAgentContext(t, ctx, "agent-1", "worker")), "one agent's calls still join")
	require.Equal(t, "session", key(WithComposingAgent(ctx, "agent-1")), "composing is not calling")

	none := &dagql.CallRequest{ResultCall: &dagql.ResultCall{}}
	agentConcurrencyKey(testAgentContext(t, ctx, "agent-1", "worker"), none)
	require.Empty(t, none.ConcurrencyKey, "a call that never joins stays so")
}
