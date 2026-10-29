package dagql

import (
	"context"
	"math/rand"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// moduleRefLiteral is a literal referencing result id, with its frame inline
// when inline is set.
func moduleRefLiteral(id uint64, inline *ResultCall) *ResultCallLiteral {
	return &ResultCallLiteral{
		Kind:      ResultCallLiteralKindResultRef,
		ResultRef: &ResultCallRef{ResultID: id, Call: inline},
	}
}

// moduleFrame is a Module-typed frame whose arguments reference ids.
func moduleFrame(ids ...uint64) *ResultCall {
	frame := &ResultCall{Type: &ResultCallType{NamedType: "Module"}}
	for _, id := range ids {
		frame.Args = append(frame.Args, &ResultCallArg{Value: moduleRefLiteral(id, nil)})
	}
	return frame
}

// walkedModuleResultIDs is what ModuleResultIDs replaces: the Module results
// WalkResultCall visits, each once, in visiting order.
func walkedModuleResultIDs(c *Cache, root *ResultCall) ([]uint64, error) {
	var ids []uint64
	seen := map[uint64]struct{}{}
	err := c.WalkResultCall(root, func(ref *ResultCallRef, frame *ResultCall) error {
		if ref.ResultID == 0 || frame.Type == nil || frame.Type.NamedType != "Module" {
			return nil
		}
		if _, ok := seen[ref.ResultID]; !ok {
			seen[ref.ResultID] = struct{}{}
			ids = append(ids, ref.ResultID)
		}
		return nil
	})
	return ids, err
}

func requireModuleResultIDsMatchWalk(t *testing.T, c *Cache, root *ResultCall) {
	t.Helper()
	want, wantErr := walkedModuleResultIDs(c, root)
	got, err := c.ModuleResultIDs(t.Context(), root)
	if wantErr != nil {
		require.Error(t, err)
		return
	}
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestModuleResultIDsMatchesWalk(t *testing.T) {
	c := &Cache{resultsByID: map[sharedResultID]*sharedResult{}}
	for id := sharedResultID(1); id <= 5; id++ {
		c.resultsByID[id] = &sharedResult{resultCall: moduleFrame()}
	}
	inline := &ResultCall{
		Receiver: &ResultCallRef{ResultID: 3},
		Args:     []*ResultCallArg{{Value: moduleRefLiteral(2, nil)}},
	}
	root := &ResultCall{
		Module:   &ResultCallModule{ResultRef: &ResultCallRef{ResultID: 1}},
		Receiver: &ResultCallRef{Call: inline},
		Args: []*ResultCallArg{
			nil,
			{Name: "list", Value: &ResultCallLiteral{
				Kind: ResultCallLiteralKindList,
				ListItems: []*ResultCallLiteral{
					moduleRefLiteral(3, nil),
					moduleRefLiteral(4, c.resultsByID[4].resultCall),
					moduleRefLiteral(0, inline),
				},
			}},
		},
		ImplicitInputs: []*ResultCallArg{
			{Name: "object", Value: &ResultCallLiteral{
				Kind: ResultCallLiteralKindObject,
				ObjectFields: []*ResultCallArg{
					nil,
					{Value: moduleRefLiteral(5, nil)},
					{Value: moduleRefLiteral(0, nil)},
				},
			}},
		},
	}

	// The second call answers from the memo.
	for range 2 {
		requireModuleResultIDsMatchWalk(t, c, root)
	}
	ids, err := c.ModuleResultIDs(t.Context(), root)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 3, 2, 4, 5}, ids)
}

func TestModuleResultIDsMatchesWalkOnSharedGraphs(t *testing.T) {
	rng := rand.New(rand.NewSource(71))
	for range 200 {
		c := &Cache{resultsByID: map[sharedResultID]*sharedResult{}}
		var frames []*ResultCall
		for i := range 40 {
			frame := &ResultCall{}
			if rng.Intn(3) == 0 {
				frame.Type = &ResultCallType{NamedType: "Module"}
			}
			for j := 0; i > 0 && j < rng.Intn(7); j++ {
				k := rng.Intn(i)
				ref := &ResultCallRef{ResultID: uint64(k + 1)}
				if rng.Intn(3) == 0 {
					ref.Call = frames[k]
					if rng.Intn(2) == 0 {
						ref.ResultID = 0
					}
				}
				frame.Args = append(frame.Args, &ResultCallArg{Value: &ResultCallLiteral{
					Kind:      ResultCallLiteralKindResultRef,
					ResultRef: ref,
				}})
			}
			frames = append(frames, frame)
			c.resultsByID[sharedResultID(i+1)] = &sharedResult{resultCall: frame}
		}
		// Memoize subgraphs first, in an order unrelated to the walk's.
		for _, i := range rng.Perm(len(frames)) {
			requireModuleResultIDsMatchWalk(t, c, frames[i])
		}
		for _, frame := range frames {
			requireModuleResultIDsMatchWalk(t, c, frame)
		}
	}
}

func TestModuleResultIDsAfterFrameReplacement(t *testing.T) {
	c := &Cache{resultsByID: map[sharedResultID]*sharedResult{
		1: {resultCall: moduleFrame(2)},
		2: {resultCall: moduleFrame()},
		3: {resultCall: moduleFrame()},
	}}
	root := &ResultCall{Receiver: &ResultCallRef{ResultID: 1}}
	requireModuleResultIDsMatchWalk(t, c, root)

	// A referenced result's frame is replaced by one referencing another module.
	oldGen := resultCallFrameGeneration.Load()
	c.resultsByID[1].storeResultCall(moduleFrame(3))
	requireModuleResultIDsMatchWalk(t, c, root)

	// A memo computed before the replacement but stored after it isn't served.
	root.moduleResultIDs.Store(&moduleResultIDsMemo{gen: oldGen, ids: []uint64{1, 2}})
	ids, err := c.ModuleResultIDs(t.Context(), root)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 3}, ids)

	// A referenced result without a frame is an error, as in the walk.
	c.resultsByID[1].storeResultCall(nil)
	_, err = c.ModuleResultIDs(t.Context(), root)
	require.Error(t, err)

	c.resultsByID[1].storeResultCall(moduleFrame(2))
	requireModuleResultIDsMatchWalk(t, c, root)
}

func TestModuleResultIDsStoresNoMemoInCycle(t *testing.T) {
	a, b := moduleFrame(2), moduleFrame(1, 3)
	c := &Cache{resultsByID: map[sharedResultID]*sharedResult{
		1: {resultCall: a},
		2: {resultCall: b},
		3: {resultCall: moduleFrame()},
	}}
	for range 2 {
		requireModuleResultIDsMatchWalk(t, c, a)
		requireModuleResultIDsMatchWalk(t, c, b)
	}
	require.Nil(t, a.moduleResultIDs.Load())
	require.Nil(t, b.moduleResultIDs.Load())
}

func TestModuleResultIDsDuringFrameReplacement(t *testing.T) {
	c := &Cache{resultsByID: map[sharedResultID]*sharedResult{
		1: {resultCall: moduleFrame(2)},
		2: {resultCall: moduleFrame()},
		3: {resultCall: moduleFrame()},
	}}
	root := &ResultCall{Receiver: &ResultCallRef{ResultID: 1}}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				ids, err := c.ModuleResultIDs(t.Context(), root)
				if err != nil || len(ids) != 2 || ids[0] != 1 || (ids[1] != 2 && ids[1] != 3) {
					t.Errorf("got %v, %v; want [1 2] or [1 3]", ids, err)
					return
				}
			}
		}()
	}
	for j := range 100 {
		c.resultsByID[1].storeResultCall(moduleFrame(uint64(2 + j%2)))
	}
	wg.Wait()
	requireModuleResultIDsMatchWalk(t, c, root)
}

// moduleLadder is a ladder of Module frames, each referencing the one below
// twice, so every frame below the top is shared by two paths; the bottom
// references itself, a cycle under all of them.
func moduleLadder(depth int) (*Cache, *ResultCall) {
	c := &Cache{resultsByID: map[sharedResultID]*sharedResult{}}
	var top *ResultCall
	for i := depth; i >= 0; i-- {
		below := uint64(i + 2)
		if i == depth {
			below = uint64(i + 1)
		}
		frame := moduleFrame(below, below)
		c.resultsByID[sharedResultID(i+1)] = &sharedResult{resultCall: frame}
		top = frame
	}
	return c, top
}

func TestModuleResultIDsComputesEachFrameOnceUnderACycle(t *testing.T) {
	const depth = 30
	c, top := moduleLadder(depth)

	// Frames under a cycle can't be memoized, but one rebuild still computes
	// each of them once; computing them once per path would be 2^30 here.
	r := newModuleIDsRebuild(t.Context(), resultCallFrameGeneration.Load())
	_, _, err := c.moduleResultIDs(top, r)
	require.NoError(t, err)
	require.Equal(t, depth+1, r.computed)

	requireModuleResultIDsMatchWalk(t, c, top)
}

// TestModuleResultIDsComputesEachFrameOnceAcrossGenerations runs rebuilds of
// two roots sharing a graph while frames elsewhere keep being replaced. Each
// rebuild's memo slots can then be overwritten by a rebuild of another
// generation, but no rebuild may compute a frame twice.
func TestModuleResultIDsComputesEachFrameOnceAcrossGenerations(t *testing.T) {
	const depth = 32
	c := &Cache{resultsByID: map[sharedResultID]*sharedResult{}}
	var roots [2]*ResultCall
	var below [2]uint64
	for level := 0; level <= depth; level++ {
		var next [2]uint64
		for side := range 2 {
			frame := &ResultCall{Type: &ResultCallType{NamedType: "Module"}}
			if level > 0 {
				// Each frame references both frames of the level below, in an
				// order that differs by side.
				frame.Args = []*ResultCallArg{
					{Value: moduleRefLiteral(below[side], nil)},
					{Value: moduleRefLiteral(below[1-side], nil)},
				}
			}
			id := uint64(level*2 + side + 1)
			c.resultsByID[sharedResultID(id)] = &sharedResult{resultCall: frame}
			next[side] = id
			roots[side] = frame
		}
		below = next
	}
	frames := 2*depth + 2

	unrelated := &sharedResult{resultCall: &ResultCall{}}
	stop := make(chan struct{})
	replaced := make(chan struct{})
	go func() {
		defer close(replaced)
		for {
			select {
			case <-stop:
				return
			default:
				unrelated.storeResultCall(&ResultCall{})
			}
		}
	}()

	var wg sync.WaitGroup
	for side := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				r := newModuleIDsRebuild(t.Context(), resultCallFrameGeneration.Load())
				if _, _, err := c.moduleResultIDs(roots[side], r); err != nil {
					t.Error(err)
					return
				}
				if r.computed > frames {
					t.Errorf("one rebuild computed %d frames; only %d are reachable", r.computed, frames)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	<-replaced
	for side := range 2 {
		requireModuleResultIDsMatchWalk(t, c, roots[side])
	}
}

func TestModuleResultIDsStopsWhenCanceled(t *testing.T) {
	c, top := moduleLadder(1000)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := c.ModuleResultIDs(ctx, top)
	require.ErrorIs(t, err, context.Canceled)
}

// TestModuleResultIDsRebuildKeepsItsFramesWhenMemosAreOverwritten forces the
// interleaving the stress test above only makes likely: a rebuild computes a
// subgraph, a rebuild of another generation overwrites the subgraph's memo
// slots, and the first rebuild reaches the subgraph again. It must not compute
// any of it twice.
func TestModuleResultIDsRebuildKeepsItsFramesWhenMemosAreOverwritten(t *testing.T) {
	const depth = 64
	c := &Cache{resultsByID: map[sharedResultID]*sharedResult{}}
	var sub *ResultCall
	for i := depth; i >= 0; i-- {
		var frame *ResultCall
		if i == depth {
			frame = moduleFrame()
		} else {
			frame = moduleFrame(uint64(i+2), uint64(i+2))
		}
		c.resultsByID[sharedResultID(i+1)] = &sharedResult{resultCall: frame}
		sub = frame
	}

	gen := resultCallFrameGeneration.Load()
	a := newModuleIDsRebuild(t.Context(), gen)
	_, _, err := c.moduleResultIDs(sub, a)
	require.NoError(t, err)
	require.Equal(t, depth+1, a.computed)

	// Another generation's rebuild overwrites every memo slot in the subgraph.
	b := newModuleIDsRebuild(t.Context(), gen+1)
	_, _, err = c.moduleResultIDs(sub, b)
	require.NoError(t, err)

	// Reaching the subgraph again from a new parent computes only the parent.
	parent := &ResultCall{Receiver: &ResultCallRef{ResultID: 1}}
	_, _, err = c.moduleResultIDs(parent, a)
	require.NoError(t, err)
	require.Equal(t, depth+2, a.computed)
}
