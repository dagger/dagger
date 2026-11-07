package dagql

import (
	"fmt"
	"slices"
	"testing"

	set "github.com/hashicorp/go-set/v3"
	"github.com/stretchr/testify/require"
)

func TestPartDependenciesWithoutRequirements(t *testing.T) {
	ctx, c := storedPartTestCache(t, WithBlobStore())
	row := storedPartTestPublish(t, ctx, c, "s", "requirements-row", &captureTestValue{text: "row"}).cacheSharedResult()
	dep := storedPartTestPublish(t, ctx, c, "s", "requirements-dep", &captureTestValue{text: "dep"}).cacheSharedResult()
	parent := storedPartTestPublish(t, ctx, c, "s", "requirements-parent", &captureTestValue{text: "parent"}).cacheSharedResult()
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	c.applyPartDependenciesLocked(ctx, parent, []*sharedResult{row}, nil)
	dep.requiredSessionResources = set.NewTreeSet(compareSessionResourceHandles)
	dep.requiredSessionResources.Insert(SessionResourceHandle("required-secret"))

	requirements, err := c.partDependenciesLocked(row, []*sharedResult{dep}, true)
	require.NoError(t, err)
	got := map[sharedResultID][]SessionResourceHandle{}
	for result, resources := range requirements {
		got[result.id] = slices.Collect(resources.Items())
	}
	require.Equal(t, map[sharedResultID][]SessionResourceHandle{
		row.id: {"required-secret"}, parent.id: {"required-secret"},
	}, got)
	without, err := c.partDependenciesLocked(row, []*sharedResult{dep}, false)
	require.NoError(t, err)
	require.Nil(t, without)
	require.Nil(t, row.requiredSessionResources)
	require.Nil(t, parent.requiredSessionResources)

	const missingID sharedResultID = 1 << 63
	for _, needed := range []bool{true, false} {
		t.Run(fmt.Sprintf("requirements=%t", needed), func(t *testing.T) {
			_, err := c.partDependenciesLocked(row, []*sharedResult{{id: missingID}}, needed)
			require.EqualError(t, err, "part graph: missing dependency")
			_, err = c.partDependenciesLocked(row, []*sharedResult{parent}, needed)
			require.EqualError(t, err, fmt.Sprintf("part graph: ownership cycle through %d", row.id))
			parent.deps[missingID] = struct{}{}
			_, err = c.partDependenciesLocked(row, nil, needed)
			delete(parent.deps, missingID)
			require.EqualError(t, err, fmt.Sprintf("part graph: missing ancestor dependency %d", missingID))
			row.depParents.Insert(missingID)
			_, err = c.partDependenciesLocked(row, nil, needed)
			row.depParents.Remove(missingID)
			require.EqualError(t, err, fmt.Sprintf("part graph: missing ancestor %d", missingID))
		})
	}
}
