package core

import (
	"testing"

	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql"
)

func TestCollectedContentCollectUnknownAnyResult(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := dagql.NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = dagql.ContextWithCache(ctx, cacheIface)
	sc := cacheIface
	dag := newCoreDagqlServerForTest(t, &Query{})

	resCall := &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "collectUnknown",
		Type:        dagql.NewResultCallType(dagql.String("").Type()),
	}
	detached, err := dagql.NewResultForCall(dagql.String("value"), resCall)
	assert.NilError(t, err)
	res, err := sc.AttachResult(ctx, "test-session", dag, detached)
	assert.NilError(t, err)
	recipeID, err := res.RecipeID(ctx)
	assert.NilError(t, err)

	content := NewCollectedContent()
	assert.NilError(t, content.CollectUnknown(ctx, recipeID))
	assert.Assert(t, content.Digest() != "")
}
