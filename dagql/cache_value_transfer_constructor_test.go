package dagql

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// A constructor that takes a Workspace can never hit across engines by its
// recipe: currentWorkspace mixes a random value into every call. What relates
// the two engines' constructor results is the content digest a module function
// with Workspace arguments puts on the object it returns, labelled for the
// remote cache, so it travels. A downstream method is then skipped on the
// receiving engine only if that method's own result was exported: a method's
// result depends on the constructor's, not the reverse, so exporting the
// constructor's result alone carries nothing the method could hit.
func TestTransferConstructorContentUnitesDownstreamCall(t *testing.T) {
	content := digest.FromString("same module object content")
	construct := func(t *testing.T, ctx context.Context, c *Cache, srv *Server, perCall string) AnyResult {
		t.Helper()
		// perCall stands for currentWorkspace's random implicit input: the two
		// engines' constructor recipes are never equal.
		frame := &ResultCall{Kind: ResultCallKindField, Field: "project", Type: NewResultCallType((&transferTestValue{}).Type()),
			ImplicitInputs: []*ResultCallArg{{Name: "cachePerCall", Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: perCall}}}}
		res, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "project"}, frame)
		})
		require.NoError(t, err)
		require.NoError(t, c.TeachContentDigest(ctx, res, content, call.ExtraDigestLabelRemoteCache))
		return res
	}
	describe := func(t *testing.T, ctx context.Context, c *Cache, srv *Server, project AnyResult) (AnyResult, int) {
		t.Helper()
		bodies := 0
		frame := &ResultCall{Kind: ResultCallKindField, Field: "describe", Type: NewResultCallType((&transferTestValue{}).Type()),
			Receiver: &ResultCallRef{ResultID: uint64(project.cacheSharedResult().id)}}
		res, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			bodies++
			return NewResultForCall(&transferTestValue{Text: "described"}, frame)
		})
		require.NoError(t, err)
		return res, bodies
	}
	for _, exportMethodResult := range []bool{true, false} {
		name := "exporting only the constructor's result"
		if exportMethodResult {
			name = "exporting the downstream method's result"
		}
		t.Run(name, func(t *testing.T) {
			actx, a, asrv := transferTestCache(t)
			aProject := construct(t, actx, a, asrv, "engine-a-call")
			aDescribed, bodies := describe(t, actx, a, asrv, aProject)
			require.Equal(t, 1, bodies)
			root := aProject
			if exportMethodResult {
				root = aDescribed
			}
			bundle := exportTestBundle(t, actx, a, root)

			bctx, b, bsrv := transferTestCache(t)
			_, err := b.ImportValues(bctx, bundle)
			require.NoError(t, err)
			bProject := construct(t, bctx, b, bsrv, "engine-b-call")
			_, bodies = describe(t, bctx, b, bsrv, bProject)
			if exportMethodResult {
				require.Zero(t, bodies, "the imported method result is hit through the constructors' shared content")
			} else {
				require.Equal(t, 1, bodies, "nothing of the method was transferred, so it runs")
			}
		})
	}
}
