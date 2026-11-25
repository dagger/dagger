package dagql

import (
	"context"
	"fmt"
	"maps"
	"math/bits"
	"slices"

	"github.com/opencontainers/go-digest"
)

// StoredSizesAloneMax is how many roots StoredSizesAlone answers for at
// once: one bit of a word each.
const StoredSizesAloneMax = 64

// StoredSizesAlone answers, for each root, what StoredSizes answers for that
// root alone, StoredSizes(ctx, []uint64{root}, nowUnix, held)[0]: what a
// bundle of the root and nothing else carries, judging held's holdings at
// nowUnix. It answers for at most StoredSizesAloneMax roots, and reads the
// entries their closures share once, where asking StoredSizes root by root
// reads each shared entry once a root: roots that share most of their
// closures, as the outputs of one build do, cost about one walk of the
// cache, not one each.
//
// It walks the roots' closures together, as walkStoredRoot walks one root's,
// in pieces (readInPieces), and keeps a copy of what it reads of each entry
// it reaches: whether it goes as a held row or with its record, the entries
// it reaches, its size and its parts' layers, and the roots whose closures
// reach it, a bit a root. A root's closure is first its ordinary closure,
// the entries its dependencies reach, then the inputs that the calls kept
// on those entries need (storedRootWalkLocked), each with its closure.
// Which of a term's calls a bundle of the root chooses, and so which inputs
// it adds, depends on which inputs the root's own ordinary closure has, so
// each entry's kept calls are chosen once for each answer the roots that
// reach it give to the inputs a choice asks about: roots whose ordinary
// closures have, and lack, the same inputs choose the same call, and the
// copy keeps which call's inputs each root adds.
//
// Each root's size is then added up on the copy, outside the lock, in the
// order its own walk would reach the entries: a blob's addresses depend on
// which of its packs the walk reaches first (storedOutputsSeen).
//
// A walk in pieces that a write lands in reads each entry as the write left
// it if it reaches it after, as StoredSizes does: with no write during it,
// it answers what StoredSizes answers root by root.
func (c *Cache) StoredSizesAlone(ctx context.Context, roots []uint64, nowUnix int64, held *HeldBy) ([]StoredSize, error) {
	if !c.blobBacked {
		return nil, fmt.Errorf("stored sizes alone: the cache has no blob store")
	}
	if len(roots) > StoredSizesAloneMax {
		return nil, fmt.Errorf("stored sizes alone: %d roots, more than %d", len(roots), StoredSizesAloneMax)
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	w := &aloneWalk{c: c, held: held, now: nowUnix, index: map[sharedResultID]int32{},
		layerAt: map[*storedLayer]int32{}, blobAt: map[digest.Digest]int32{}, packAt: map[digest.Digest]int32{}}
	starts := w.walk(roots)
	measured := make([]storedSizeMeasure, 0, len(w.misses))
	missBytes := make([]int64, len(w.misses))
	for i, miss := range w.misses {
		m, err := c.measureStoredValue(miss)
		if err != nil {
			return nil, fmt.Errorf("stored sizes alone: entry %d: %w", miss.res.id, err)
		}
		missBytes[i] = m.size.bytes + senderStampBytes(miss.changedAt)
		measured = append(measured, m)
	}
	c.publishStoredSizes(measured)
	sizes := make([]StoredSize, len(roots))
	sum := &aloneSum{w: w, visited: make([]int32, len(w.nodes)), missBytes: missBytes,
		layerSeen: make([]int32, len(w.layers)), blobSeen: make([]int32, len(w.blobAt)), blobPack: make([]int32, len(w.blobAt))}
	for i, start := range starts {
		if start >= 0 {
			sizes[i] = sum.root(i, start)
		}
	}
	return sizes, nil
}

// aloneWalk is StoredSizesAlone's walk: the entries it reached, each once,
// by their place in nodes (index), and the values it couldn't size, which
// are measured after it.
type aloneWalk struct {
	c      *Cache
	held   *HeldBy
	now    int64
	hold   *readPieces
	index  map[sharedResultID]int32
	nodes  []aloneNode
	misses []storedSizeMiss
	// layers are the layers the entries' parts name, each once, by their
	// place in it (layerAt); a layer names its blob and its pack by their
	// places in blobAt and packAt.
	layers  []aloneLayer
	layerAt map[*storedLayer]int32
	blobAt  map[digest.Digest]int32
	packAt  map[digest.Digest]int32
}

// aloneLayer is a layer of a part's chain as the walk read it
// (storedLayer): what its entry adds to the message, and its blob and pack.
type aloneLayer struct {
	wire       int64
	blob, pack int32
}

// aloneNode is an entry StoredSizesAlone reached, as the walk read it.
type aloneNode struct {
	id sharedResultID
	// mine are the roots whose ordinary closure reaches the entry, and reach
	// those whose closure does, the inputs of its kept calls included.
	mine, reach uint64
	// held marks an entry that goes as a held row, and counted one that goes
	// with its record; an entry that is neither, with no value or none
	// stored, is in the closure and carries nothing.
	held, counted bool
	// children are the entries the walk reached through it, in the order it
	// reached them.
	children []int32
	// bytes is its held row's size or its record's measured one; a record
	// not yet measured is miss, measured after the walk, -1 for none.
	bytes int64
	miss  int32
	// parts are its parts, in key order: what each adds to the message but
	// its layers, and its layers.
	parts []aloneNodePart
	// calls are the inputs its kept calls add, each for some of the roots
	// that reach it, in the order a walk of one root adds them.
	calls []aloneCall
}

// aloneNodePart is what an entry's stored part adds to the message
// (storedSizeWalk.addOutputs): its own, and its layers, by their places in
// the walk's layers.
type aloneNodePart struct {
	wire   int64
	layers []int32
}

// aloneCall is a choice of the call of one of an entry's terms for some of
// the roots that reach it: the entries that place its inputs, -1 for one
// that each of those roots' ordinary closures has.
type aloneCall struct {
	roots  uint64
	places []int32
}

// carried reports whether a bundle carries the entry: as a held row or with
// its record.
func (n *aloneNode) carried() bool {
	return n.held || n.counted
}

// walk walks the roots' closures: each root's ordinary closure, then the
// inputs of the calls kept on the entries reached, in rounds. What it reads
// of the cache it reads in pieces (readInPieces); which roots reach which
// entries it marks on its copy between them, without egraphMu, as that
// takes a step for each entry of each root's closure. It returns where in
// nodes each root is, -1 for a root the cache has no value for, which adds
// nothing, as walkStoredRoot answers it.
func (w *aloneWalk) walk(roots []uint64) []int32 {
	starts := make([]int32, len(roots))
	w.inPieces(func() {
		for i, number := range roots {
			starts[i] = -1
			res := w.c.resultsByID[sharedResultID(number)]
			if res == nil || res.noValueLocked() {
				continue
			}
			starts[i] = w.reach(res)
		}
	})
	for i, at := range starts {
		if at >= 0 {
			w.spread(at, uint64(1)<<i, true, nil)
		}
	}
	w.keptCalls()
	return starts
}

// inPieces runs read with egraphMu held for reading in pieces, as the walk's
// hold.
func (w *aloneWalk) inPieces(read func()) {
	w.hold = w.c.readInPieces()
	w.hold.report = w.c.testStoredSizesHold
	defer w.hold.done()
	read()
}

// reach reads res and what it reaches, as storedSizeWalk.visit does, once
// for every root: an entry the walk has reached already is where it was. It
// returns its place in nodes, or -1 for an entry the cache no longer has.
// Requires egraphMu, by the walk's hold.
func (w *aloneWalk) reach(res *sharedResult) int32 {
	if at, ok := w.index[res.id]; ok {
		return at
	}
	w.hold.step()
	if res = w.c.resultsByID[res.id]; res == nil {
		return -1
	}
	at := int32(len(w.nodes))
	w.index[res.id] = at
	w.nodes = append(w.nodes, aloneNode{id: res.id, miss: -1})
	if w.held.heldLocked(res, w.now) {
		w.nodes[at].held = true
		w.nodes[at].bytes = heldRowSizeLocked(res)
		return at
	}
	if res.noValueLocked() || res.persistedEnvelope == nil {
		return at
	}
	yields := w.hold.yields()
	var children []int32
	for _, id := range storedClosureChildrenLocked(res) {
		if child := w.c.resultsByID[id]; child != nil {
			if to := w.reach(child); to >= 0 {
				children = append(children, to)
			}
		}
	}
	w.nodes[at].children = children
	// The entry's own reading, after its dependencies, is a step of its own.
	w.hold.step()
	if w.hold.yields() != yields {
		if res = w.c.resultsByID[res.id]; res == nil || res.noValueLocked() || res.persistedEnvelope == nil {
			return at
		}
	}
	node := &w.nodes[at]
	node.counted = true
	if bytes, ok := storedValueMeasuredLocked(res); ok {
		node.bytes = bytes
	} else {
		node.miss = int32(len(w.misses))
		w.misses = append(w.misses, storedSizeMissLocked(res))
	}
	for _, key := range slices.Sorted(maps.Keys(res.storedParts)) {
		part := res.storedParts[key]
		layers := make([]int32, len(part.layers))
		for i, layer := range part.layers {
			layers[i] = w.layer(layer)
		}
		node.parts = append(node.parts, aloneNodePart{wire: part.wire, layers: layers})
	}
	return at
}

// layer returns layer's place in the walk's layers, and adds it the first
// time. Requires egraphMu.
func (w *aloneWalk) layer(layer *storedLayer) int32 {
	if at, ok := w.layerAt[layer]; ok {
		return at
	}
	place := func(places map[digest.Digest]int32, dig digest.Digest) int32 {
		at, ok := places[dig]
		if !ok {
			at = int32(len(places))
			places[dig] = at
		}
		return at
	}
	at := int32(len(w.layers))
	w.layerAt[layer] = at
	w.layers = append(w.layers, aloneLayer{wire: layer.wire, blob: place(w.blobAt, layer.layer.Descriptor.Digest), pack: place(w.packAt, layer.pack)})
	return at
}

// aloneSpread is a step of spread: an entry, and roots that reach it.
type aloneSpread struct {
	at    int32
	roots uint64
}

// spread marks roots as reaching the entry at, and what it reaches: in
// their ordinary closures with mine. An entry that the roots reach already
// has its closure marked: spread stops there. A carried entry newly reached
// is added to calls, for its kept calls' inputs.
func (w *aloneWalk) spread(at int32, roots uint64, mine bool, calls *[]aloneSpread) {
	if w.c.testStoredSizesAloneMarks != nil {
		w.c.testStoredSizesAloneMarks()
	}
	stack := []aloneSpread{{at: at, roots: roots}}
	for len(stack) > 0 {
		next := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		node := &w.nodes[next.at]
		added := next.roots &^ node.reach
		if mine {
			added = next.roots &^ node.mine
			node.mine |= added
		}
		if added == 0 {
			continue
		}
		node.reach |= added
		if calls != nil && node.carried() {
			*calls = append(*calls, aloneSpread{at: next.at, roots: added})
		}
		for _, child := range node.children {
			stack = append(stack, aloneSpread{at: child, roots: added})
		}
	}
}

// mineOf returns the roots whose ordinary closure has the entry.
func (w *aloneWalk) mineOf(id sharedResultID) uint64 {
	if at, ok := w.index[id]; ok {
		return w.nodes[at].mine
	}
	return 0
}

// aloneAsked is what a choice of a kept call asked of an input: whether the
// root it chose for has it.
type aloneAsked struct {
	res *sharedResult
	has bool
}

// keptCalls chooses, for each root, the calls kept on the entries its
// closure carries, as storedRootWalkLocked chooses them for a root alone,
// and reaches the entries that place their inputs, each with its closure:
// for the entries reached so far, and for those the inputs reach. Each
// choice of a term's call (placeableCallLocked) is made for one root, which
// has an input if its ordinary closure has it or the recipient holds it;
// every other root that would answer the inputs that choice asked about
// alike makes the same choice. An input a root's ordinary closure has
// already adds nothing for it. A root is chosen for once at each entry:
// the roots an entry is reached by later are chosen for when they reach it.
// It goes in rounds: each makes the choices due in pieces of egraphMu, and
// then, without it, marks the roots the inputs it reached are reached by,
// which makes the next round's choices due.
func (w *aloneWalk) keptCalls() {
	var calls []aloneSpread
	for at := range w.nodes {
		if node := &w.nodes[at]; node.carried() && node.reach != 0 {
			calls = append(calls, aloneSpread{at: int32(at), roots: node.reach})
		}
	}
	terms := heldTerms(w.held)
	for len(calls) > 0 {
		var reached []aloneSpread
		w.inPieces(func() {
			for _, next := range calls {
				reached = w.choose(next, terms, reached)
			}
		})
		calls = nil
		for _, places := range reached {
			w.spread(places.at, places.roots, false, &calls)
		}
	}
}

// choose makes the choices of the calls kept on next's entry for next's
// roots (keptCalls), and returns reached with the entries their inputs are
// placed by, each with the roots it adds them for. Requires egraphMu, by
// the walk's hold.
func (w *aloneWalk) choose(next aloneSpread, terms map[digest.Digest][]RemoteTerm, reached []aloneSpread) []aloneSpread {
	w.hold.step()
	res := w.c.resultsByID[w.nodes[next.at].id]
	if res == nil || len(res.keptCallsLocked()) == 0 {
		return reached
	}
	for _, group := range w.c.keptCallTermsLocked(res) {
		if group = w.c.sendableCallsLocked(w.held, terms, group); len(group) == 0 {
			continue
		}
		for left := next.roots; left != 0; {
			root := bits.TrailingZeros64(left)
			var asked []aloneAsked
			_, places := w.c.placeableCallLocked(group, w.now, func(input *sharedResult) bool {
				has := w.mineOf(input.id)>>root&1 == 1 || w.held.heldLocked(input, w.now)
				asked = append(asked, aloneAsked{res: input, has: has})
				return has
			})
			same := left
			for _, a := range asked {
				answers := w.mineOf(a.res.id)
				if w.held.heldLocked(a.res, w.now) {
					answers = ^uint64(0)
				}
				if a.has {
					same &= answers
				} else {
					same &^= answers
				}
			}
			left &^= same
			call := aloneCall{roots: same}
			for _, place := range places {
				if place == nil {
					continue
				}
				to := same &^ w.mineOf(place.id)
				if to == 0 {
					call.places = append(call.places, -1)
					continue
				}
				at := w.reach(place)
				call.places = append(call.places, at)
				if at >= 0 {
					reached = append(reached, aloneSpread{at: at, roots: to})
				}
			}
			// Reaching a place can have grown nodes: the entry is found
			// again by its place in them.
			w.nodes[next.at].calls = append(w.nodes[next.at].calls, call)
		}
	}
	return reached
}

// aloneSum adds up each root's size on the walk's copy of the entries, in
// the order a walk of the root alone reaches them (walkStoredRoot), outside
// egraphMu. visited holds, for each entry, 1 more than the last root that
// reached it, and layerSeen and blobSeen the same for each layer and blob
// its parts name, a root's storedOutputsSeen: blobPack is the pack of the
// first layer of each blob it reached.
type aloneSum struct {
	w         *aloneWalk
	visited   []int32
	missBytes []int64
	layerSeen []int32
	blobSeen  []int32
	blobPack  []int32
	order     []int32
	size      StoredSize
	stack     []aloneVisit
}

// aloneVisit is a step of aloneSum's walk: an entry, and how many of its
// children it has visited.
type aloneVisit struct {
	at       int32
	children int
}

// root adds up the size of the i-th root, which is start in nodes: its
// ordinary closure, then, entry by entry in the order it reached them, the
// inputs that the calls chosen for it add, but for those its ordinary
// closure has.
func (s *aloneSum) root(i int, start int32) StoredSize {
	s.size = StoredSize{}
	s.order = s.order[:0]
	mark := int32(i + 1)
	bit := uint64(1) << i
	s.visit(start, mark)
	for k := 0; k < len(s.order); k++ {
		for _, call := range s.w.nodes[s.order[k]].calls {
			if call.roots&bit == 0 {
				continue
			}
			for _, place := range call.places {
				if place >= 0 && s.w.nodes[place].mine&bit == 0 {
					s.visit(place, mark)
				}
			}
		}
	}
	return s.size
}

// visit adds what at reaches that the root's walk hasn't, as
// storedSizeWalk.visit does: dependencies first, and an entry after them.
func (s *aloneSum) visit(at int32, mark int32) {
	if s.visited[at] == mark {
		return
	}
	s.visited[at] = mark
	s.stack = append(s.stack[:0], aloneVisit{at: at})
	for len(s.stack) > 0 {
		top := &s.stack[len(s.stack)-1]
		node := &s.w.nodes[top.at]
		if node.held {
			s.size.Records++
			s.size.Bytes += node.bytes
			s.order = append(s.order, top.at)
			s.stack = s.stack[:len(s.stack)-1]
			continue
		}
		if top.children < len(node.children) {
			child := node.children[top.children]
			top.children++
			if s.visited[child] != mark {
				s.visited[child] = mark
				s.stack = append(s.stack, aloneVisit{at: child})
			}
			continue
		}
		s.stack = s.stack[:len(s.stack)-1]
		if !node.counted {
			continue
		}
		s.size.Records++
		if node.miss >= 0 {
			s.size.Bytes += s.missBytes[node.miss]
		} else {
			s.size.Bytes += node.bytes
		}
		s.addOutputs(node.parts, mark)
		s.order = append(s.order, top.at)
	}
}

// addOutputs counts what an entry's parts add to the root's message, marked
// mark, as storedSizeWalk.addOutputs does: each layer once, and an address
// for each blob, and for each layer of it in another pack than the first.
func (s *aloneSum) addOutputs(parts []aloneNodePart, mark int32) {
	for _, part := range parts {
		s.size.OutputBytes += part.wire
		for _, at := range part.layers {
			layer := &s.w.layers[at]
			if s.layerSeen[at] != mark {
				s.layerSeen[at] = mark
				s.size.OutputBytes += layer.wire
			}
			if s.blobSeen[layer.blob] != mark {
				s.blobSeen[layer.blob] = mark
				s.blobPack[layer.blob] = layer.pack
				s.size.OutputBytes += storedBlobAddressAllowance
			} else if s.blobPack[layer.blob] != layer.pack {
				s.size.OutputBytes += storedBlobAddressAllowance
			}
		}
	}
}
