package core

import (
	"testing"

	"github.com/opencontainers/go-digest"
	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql"
)

func TestCollectedContentCollectUnknownAnyResult(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cacheIface, err := dagql.NewCache(ctx, "")
	assert.NilError(t, err)
	sc := cacheIface
	dag := dagql.NewServer(&Query{}, sc)

	resCall := &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "collectUnknown",
		Type:        dagql.NewResultCallType(dagql.String("").Type()),
	}
	detached, err := dagql.NewResultForCall(dagql.String("value"), resCall)
	assert.NilError(t, err)
	res, err := sc.AttachResult(ctx, "test-session", dag, detached)
	assert.NilError(t, err)
	recipeID, err := res.RecipeID()
	assert.NilError(t, err)

	content := NewCollectedContent()
	assert.NilError(t, content.CollectUnknown(ctx, recipeID))
	assert.Equal(t, 1, len(content.IDs))
	assert.Assert(t, content.IDs[digest.Digest(recipeID.Digest())] != nil)
}
