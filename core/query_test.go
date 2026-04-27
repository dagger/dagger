package core

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestQueryNewModuleInitializesDeps(t *testing.T) {
	t.Parallel()

	query := &Query{}
	mod := query.NewModule()

	assert.Assert(t, mod != nil)
	assert.Assert(t, mod.Deps != nil)
	assert.Equal(t, 0, len(mod.Deps.Mods()))
}
