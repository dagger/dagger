package core

import (
	"testing"

	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

func TestCollectedContentCollectUnknownAnyResult(t *testing.T) {
	t.Parallel()

	id := call.New().Append(dagql.String("").Type(), "collectUnknown")
	res, err := dagql.NewResultForID(dagql.String("value"), id)
	assert.NilError(t, err)

	content := NewCollectedContent()
	assert.NilError(t, content.CollectUnknown(t.Context(), res))
	assert.Equal(t, 1, len(content.IDs))
	assert.Assert(t, content.IDs[id.Digest()] != nil)
}
