package dagql

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// Pricing and bundling a root again, with nothing changed, groups the calls
// kept on its entries by term only once: the grouping is kept with the entry.
func TestKeptCallTermsAreGroupedOnce(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	var built atomic.Int32
	test.cloud.testCallTermsBuilt = func() { built.Add(1) }
	now := time.Now().Unix()
	_, err := test.cloud.StoredSizes(test.ctx, []uint64{test.number}, now, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, built.Load())
	_, err = test.cloud.StoredSizes(test.ctx, []uint64{test.number}, now, nil)
	require.NoError(t, err)
	built1, err := test.cloud.StoredBundle(test.ctx, []uint64{test.number}, now, nil)
	require.NoError(t, err)
	require.Len(t, built1.Bundle.Calls, 1)
	require.EqualValues(t, 1, built.Load(), "nothing changed since the first grouping")
}

// Every kept grouping read in this package's tests is checked against one
// built fresh.
func init() {
	verifyKeptCallTerms = true
}

// An entry's kept grouping follows each change to its calls and to their
// inputs' classes: a class for an input that had none, a union that moves an
// input's class under another root, one that leaves it the root, a
// compaction that renumbers classes; and the entry's leaving forgets its
// inputs.
func TestKeptCallTermsFollowTheirInputsClasses(t *testing.T) {
	t.Parallel()
	ctx, c := storedPartTestCache(t, WithBlobStore())
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	c.initEgraphLocked()
	entry := &sharedResult{id: c.nextSharedResultID}
	c.nextSharedResultID++
	c.putResultLocked(entry)
	self := digest.FromString("self")
	keep := func(recipe string, inputs ...string) {
		id := callIdentity{field: "f", recipe: digest.FromString(recipe), self: self}
		for _, in := range inputs {
			id.inputs = append(id.inputs, digest.FromString(in))
			id.witnesses = append(id.witnesses, false)
		}
		c.keepCallLocked(entry, RemoteHolding{Recipe: digest.FromString("entry"), Request: id.recipe}, &keptCall{id: id})
	}
	class := func(in string) eqClassID { return c.ensureEqClassForDigestLocked(ctx, digest.FromString(in).String()) }
	groups := func(step string, want int) {
		t.Helper()
		kept := c.keptCallTermsLocked(entry)
		require.Equal(t, c.callTermsLocked(entry), kept, step)
		require.Len(t, kept, want, step)
	}

	class("a")
	keep("x", "a")
	keep("y", "b")
	groups("an input with no class stands for itself", 2)
	class("b")
	groups("the input has a class", 2)
	c.mergeEqClassesLocked(ctx, class("a"), class("b"))
	groups("the inputs' classes are one", 1)
	keep("z", "c")
	groups("a call is kept", 2)
	// A class of higher rank absorbs c's, which joins a and b's root
	// under it; then a class of lower rank joins that root, which stays.
	class("d")
	c.mergeEqClassesLocked(ctx, class("d"), class("e"))
	c.mergeEqClassesLocked(ctx, class("d"), class("c"), class("a"))
	groups("every input's class is one", 1)
	c.mergeEqClassesLocked(ctx, class("a"), class("f"))
	groups("a class joins the inputs' root", 1)
	// The entry's output keeps the inputs' class live, under a new number.
	c.resultOutputEqClasses[entry.id] = map[eqClassID]struct{}{class("a"): {}}
	compacted, _, _ := c.compactEqClassesLocked(true)
	require.True(t, compacted)
	groups("classes are renumbered", 1)

	require.NotEmpty(t, c.callInputEntries)
	c.deleteResultLocked(entry)
	require.Empty(t, c.callInputEntries, "the entry left with its calls")
}
