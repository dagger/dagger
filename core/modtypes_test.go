package core

import (
	"testing"

	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

func TestCollectedContentCollectUnknownAnyResult(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = dagql.ContextWithCache(ctx, cacheIface)
	sc := cacheIface
	root := &Query{}
	testSrv := &moduleObjectTestServer{
		mockServer: &mockServer{},
		cache:      sc,
		root:       root,
	}
	root.Server = testSrv
	dag := newCoreDagqlServerForTest(t, root)
	testSrv.dag = dag
	ctx = ContextWithQuery(ctx, root)
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "collect-unknown-client",
		SessionID: "test-session",
	})

	resCall := &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "collectUnknown",
		Type:        dagql.NewResultCallType(dagql.String("").Type()),
	}
	detached, err := dagql.NewResultForCall(dagql.String("value"), resCall)
	assert.NilError(t, err)
	res, err := sc.AttachResult(ctx, "test-session", dag, detached)
	assert.NilError(t, err)
	content := NewCollectedContent()
	assert.NilError(t, content.CollectUnknown(ctx, res))
	assert.Assert(t, content.Digest() != "")
}
