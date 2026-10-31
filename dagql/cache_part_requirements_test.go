package dagql

import (
	"testing"
	"time"

	set "github.com/hashicorp/go-set/v3"
	"github.com/stretchr/testify/require"
)

// requirementTestRow registers a bare row in c's graph, depending on deps,
// with handle as its own session resource when set, and its required set
// derived as the cache derives it.
func requirementTestRow(t *testing.T, c *Cache, id sharedResultID, handle SessionResourceHandle, deps ...*sharedResult) *sharedResult {
	t.Helper()
	row := &sharedResult{id: id, sessionResourceHandle: handle, deps: map[sharedResultID]struct{}{}}
	if c.resultsByID == nil {
		c.resultsByID = map[sharedResultID]*sharedResult{}
	}
	c.resultsByID[id] = row
	for _, dep := range deps {
		row.deps[dep.id] = struct{}{}
		c.rememberDependencyEdgeLocked(row, dep)
	}
	_, err := c.recomputeRequiredSessionResourcesLocked(row)
	require.NoError(t, err)
	return row
}

func requirementHandles(row *sharedResult) []SessionResourceHandle {
	if row.requiredSessionResources == nil {
		return nil
	}
	return row.requiredSessionResources.Slice()
}

// A part's dependencies' session resources reach the receiver and every
// result that depends on it and lacked them. A result that already required
// them, and the results above it, don't change.
func TestPartDependenciesRequireTheirResourcesAbove(t *testing.T) {
	_, c, _ := transferTestCache(t)
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	receiver := requirementTestRow(t, c, 1_000_001, "")
	a1 := requirementTestRow(t, c, 1_000_002, "", receiver)
	a2 := requirementTestRow(t, c, 1_000_003, "", a1)
	b1 := requirementTestRow(t, c, 1_000_004, "socket", receiver)
	b2 := requirementTestRow(t, c, 1_000_005, "", b1)
	dep := requirementTestRow(t, c, 1_000_006, "socket")
	b1Gen, b2Gen := b1.requiredSessionResourcesGen.Load(), b2.requiredSessionResourcesGen.Load()

	requirements, err := c.preparePartDependenciesLocked(receiver, []*sharedResult{dep})
	require.NoError(t, err)
	c.applyPartDependenciesLocked(t.Context(), receiver, []*sharedResult{dep}, requirements)

	for _, row := range []*sharedResult{receiver, a1, a2, b1, b2} {
		require.Equal(t, []SessionResourceHandle{"socket"}, requirementHandles(row), "row %d", row.id)
	}
	require.Equal(t, b1Gen, b1.requiredSessionResourcesGen.Load(), "b1 already required the socket")
	require.Equal(t, b2Gen, b2.requiredSessionResourcesGen.Load(), "b2 already required the socket")
}

// Committing a part whose dependencies require no session resource costs the
// same however many results depend on the receiver: none of them changes,
// so the commit, which holds the graph lock, doesn't visit them. On a fresh
// engine a part arrives after the results built on it, so a receiver can
// have thousands.
func TestPartDependenciesCostNothingAboveWithoutResources(t *testing.T) {
	_, c, _ := transferTestCache(t)
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	receiver := requirementTestRow(t, c, 2_000_000, "")
	below := receiver
	for i := range sharedResultID(2000) {
		// Half a chain above the receiver, half a fan out of it.
		if i%2 == 0 {
			below = requirementTestRow(t, c, 2_000_001+i, "", below)
		} else {
			requirementTestRow(t, c, 2_000_001+i, "", receiver)
		}
	}
	dep := requirementTestRow(t, c, 3_000_000, "")
	deps := []*sharedResult{dep}

	var requirements map[*sharedResult]*set.TreeSet[SessionResourceHandle]
	allocs := testing.AllocsPerRun(20, func() {
		var err error
		requirements, err = c.preparePartDependenciesLocked(receiver, deps)
		require.NoError(t, err)
	})
	start := time.Now()
	for range 20 {
		_, err := c.preparePartDependenciesLocked(receiver, deps)
		require.NoError(t, err)
	}
	t.Logf("%.0f allocations and %s a preparation", allocs, time.Since(start)/20)
	require.Less(t, allocs, 50.0, "the commit walks the receiver's dependents")
	c.applyPartDependenciesLocked(t.Context(), receiver, deps, requirements)
	require.Nil(t, receiver.requiredSessionResources)
}
