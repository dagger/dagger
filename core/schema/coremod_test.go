package schema

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
)

func TestCoreModObjectReturnReplayability(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = dagql.ContextWithCache(ctx, cache)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID: "module-return-client", SessionID: "module-return-session",
	})
	server := &currentTypeDefsTestServer{}
	root := core.NewRoot(server)
	srv, err := dagql.NewServer(ctx, root)
	require.NoError(t, err)
	server.dag = srv
	ctx = core.ContextWithQuery(ctx, root)

	var executions atomic.Int32
	const reason = "Requires explicit authorization from the calling client"
	dagql.Fields[*core.GitPushResult]{
		dagql.Func("unsafeNext", func(context.Context, *core.GitPushResult, struct{}) (*core.GitPushResult, error) {
			executions.Add(1)
			return &core.GitPushResult{}, nil
		}).NotReplayable(reason),
		dagql.Func("next", func(context.Context, *core.GitPushResult, struct{}) (*core.GitPushResult, error) {
			executions.Add(1)
			return &core.GitPushResult{}, nil
		}),
	}.Install(srv)
	dagql.Fields[*core.Query]{
		dagql.Func("unsafeReceipt", func(context.Context, *core.Query, struct{}) (*core.GitPushResult, error) {
			executions.Add(1)
			return &core.GitPushResult{}, nil
		}).NotReplayable(reason),
		dagql.Func("safeReceipt", func(context.Context, *core.Query, struct{}) (*core.GitPushResult, error) {
			return &core.GitPushResult{}, nil
		}),
		dagql.Func("receiptFrom", func(context.Context, *core.Query, struct {
			Receipt dagql.ID[*core.GitPushResult]
		}) (*core.GitPushResult, error) {
			executions.Add(1)
			return &core.GitPushResult{}, nil
		}),
	}.Install(srv)

	obj := &CoreModObject{name: "GitPushResult"}
	receiptType := (&core.GitPushResult{}).Type()
	unsafe := call.New().Append(receiptType, "unsafeReceipt")
	for name, id := range map[string]*call.ID{
		"direct":   unsafe,
		"receiver": unsafe.Append(receiptType, "next"),
		"ID argument": call.New().Append(receiptType, "receiptFrom",
			call.WithArgs(call.NewArgument("receipt", call.NewLiteralID(unsafe), false))),
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := id.Encode()
			require.NoError(t, err)
			_, err = obj.ConvertFromSDKResult(ctx, encoded)
			require.ErrorContains(t, err, "cannot replay module return ID")
			require.ErrorContains(t, err, "unsafeReceipt")
			require.ErrorContains(t, err, reason)
			require.Zero(t, executions.Load(), "reject the entire recipe before evaluating any fields")
		})
	}
	t.Run("forged receiver type", func(t *testing.T) {
		id := call.New().Append((&core.Directory{}).Type(), "safeReceipt").Append(receiptType, "unsafeNext")
		encoded, err := id.Encode()
		require.NoError(t, err)
		_, err = obj.ConvertFromSDKResult(ctx, encoded)
		require.ErrorContains(t, err, "unsafeNext is not replayable")
		require.Zero(t, executions.Load(), "a forged receiver type must not hide a non-replayable field")
	})

	t.Run("replayable recipe", func(t *testing.T) {
		encoded, err := call.New().Append(receiptType, "safeReceipt").Encode()
		require.NoError(t, err)
		res, err := obj.ConvertFromSDKResult(ctx, encoded)
		require.NoError(t, err)
		require.Equal(t, "GitPushResult", res.Type().Name())
	})

	t.Run("existing handle", func(t *testing.T) {
		// A legitimate call has already executed in the original caller's
		// context. Returning its handle must not replay the producing recipe.
		var receipt dagql.ObjectResult[*core.GitPushResult]
		require.NoError(t, srv.Select(ctx, srv.Root(), &receipt, dagql.Selector{Field: "unsafeReceipt"}))
		before := executions.Load()
		id, err := receipt.ID()
		require.NoError(t, err)
		require.True(t, id.IsHandle())
		encoded, err := id.Encode()
		require.NoError(t, err)
		res, err := obj.ConvertFromSDKResult(ctx, encoded)
		require.NoError(t, err)
		require.Equal(t, "GitPushResult", res.Type().Name())
		require.Equal(t, before, executions.Load())
	})
}
