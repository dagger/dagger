package dagql

import (
	"context"
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestLazyContentPreferredDigest(t *testing.T) {
	sr, rootCtx, root := newLazyRecordingRoot("root")
	defer root.End()
	ctx := cacheTestContext(rootCtx)
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, c)
	defer cacheTestReleaseSession(t, c, ctx)
	srv := cacheTestServer(t)
	frame := &ResultCall{Field: "lateContent", Type: NewResultCallType((&cacheTestObject{}).Type())}
	content := digest.FromString("lazy-output")
	var res AnyResult
	res, err = c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{
			lazyEval: func(ctx context.Context) error { return c.TeachContentDigest(ctx, res, content) },
		}), nil
	})
	require.NoError(t, err)
	require.True(t, HasPendingLazyEvaluation(res))
	require.NoError(t, c.Evaluate(ctx, res))
	var found bool
	for _, span := range sr.Ended() {
		if span.Name() != "resume lateContent" {
			continue
		}
		for _, kv := range span.Attributes() {
			if string(kv.Key) == telemetryattrs.DagContentPreferredDigestAttr {
				require.Equal(t, content.String(), kv.Value.AsString())
				found = true
			}
		}
	}
	require.True(t, found, "lazy completion must export content learned after API completion")
}
