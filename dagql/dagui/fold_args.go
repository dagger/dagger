package dagui

import (
	"hash/fnv"
	"strconv"
	"strings"
)

// Argument spans.
//
// A row renders its call's ID-valued arguments through the spans that ran
// them and the spans that produced them: an argument shows as its creator's
// call (DB.Simplify, by output digest) through its most interesting span
// (DB.MostInterestingSpan, by call digest), and so on down the arguments of
// the calls it renders, and a row's receiver as its creator's (Span.Base,
// through Simplify). A folding DB holds neither for folded spans, so it
// retains them as rendering asks for them: DB.MostInterestingSpan and
// DB.Simplify note the digest (ask), the frontend has the spans read back
// once its frame is drawn (Fold.RetainAsked), and spans still to come that
// carry it are retained on arrival. The view renders them a round trip
// later, as it does a row expanded over folded children; only what
// rendering reaches is retained, at whatever depth it reaches it.
//
// Folded spans are indexed by digest in digestIndex: a 64-bit key per
// distinct digest (an xxh3 digest is its own key), the newest span carrying
// it, and the rest chained through Fold.callNext / Fold.outputNext, so a
// span costs 8 bytes of chain and a distinct digest ~16.

// digestKind says what a digest is of a span: the call it carries, the
// output it produced, or either.
type digestKind uint8

const (
	digestCall digestKind = 1 << iota
	digestOutput
)

// askedDigest is a digest rendering asked about, not acted on yet.
type askedDigest struct {
	key  uint64
	kind digestKind
}

// digestKey is a digest's index key: an xxh3 digest's own 64 bits, any
// other digest's FNV-1a hash. Keys that collide only retain spans the view
// may not need.
func digestKey(dig string) uint64 {
	if hex, ok := strings.CutPrefix(dig, "xxh3:"); ok && len(hex) == 16 {
		if k, err := strconv.ParseUint(hex, 16, 64); err == nil {
			return k
		}
	}
	h := fnv.New64a()
	h.Write([]byte(dig))
	return h.Sum64()
}

// digestIndex maps a digest key to the newest folded span (idx+1) indexed
// under it: open addressing over keys and heads, 0 marking an empty slot.
type digestIndex struct {
	keys  []uint64
	heads []uint32
	n     int
}

func (t *digestIndex) slot(k uint64) int {
	mask := uint64(len(t.keys) - 1)
	h := k ^ k>>29
	h *= 0xbf58476d1ce4e5b9
	h ^= h >> 32
	for s := h & mask; ; s = (s + 1) & mask {
		if t.heads[s] == 0 || t.keys[s] == k {
			return int(s)
		}
	}
}

// head returns the newest span indexed under k, idx+1 (0 for none).
func (t *digestIndex) head(k uint64) uint32 {
	if len(t.keys) == 0 {
		return 0
	}
	return t.heads[t.slot(k)]
}

// push makes span i the newest under k, returning the one it displaces.
func (t *digestIndex) push(k uint64, i uint32) (prev uint32) {
	if (t.n+1)*4 > len(t.keys)*3 {
		oldKeys, oldHeads := t.keys, t.heads
		size := max(1024, 2*len(oldKeys))
		t.keys, t.heads = make([]uint64, size), make([]uint32, size)
		for s, head := range oldHeads {
			if head != 0 {
				ns := t.slot(oldKeys[s])
				t.keys[ns], t.heads[ns] = oldKeys[s], head
			}
		}
	}
	s := t.slot(k)
	prev = t.heads[s]
	if prev == 0 {
		t.n++
		t.keys[s] = k
	}
	t.heads[s] = i + 1
	return prev
}

// chainEnd ends a chain in callNext / outputNext, so that 0 means a span
// isn't indexed.
const chainEnd = ^uint32(0)

// indexDigests records folded span i, once each, under the call it carries
// and the output it produced (which its snapshots name once it ended), for
// RetainAsked to find.
func (f *Fold) indexDigests(i uint32, info foldInfo) {
	if info.callDigest != "" && f.callNext[i] == 0 {
		f.callNext[i] = chainOf(f.byCall.push(digestKey(info.callDigest), i))
	}
	if info.output != "" && f.outputNext[i] == 0 {
		f.outputNext[i] = chainOf(f.byOutput.push(digestKey(info.output), i))
	}
}

func chainOf(prev uint32) uint32 {
	if prev == 0 {
		return chainEnd
	}
	return prev
}

// isWanted reports whether an arriving span carries a call, or produced an
// output, that rendering asked about.
func (f *Fold) isWanted(info foldInfo) bool {
	if len(f.wanted) == 0 {
		return false
	}
	if info.callDigest != "" && f.wanted[digestKey(info.callDigest)]&digestCall != 0 {
		return true
	}
	return info.output != "" && f.wanted[digestKey(info.output)]&digestOutput != 0
}

// ask notes that rendering looked up the spans carrying dig (as kind): they
// are retained from then on, the folded ones once RetainAsked runs.
func (f *Fold) ask(dig string, kind digestKind) {
	if f == nil || dig == "" {
		return
	}
	k := digestKey(dig)
	if f.wanted[k]&kind == kind {
		return
	}
	if f.wanted == nil {
		f.wanted = map[uint64]digestKind{}
	}
	f.wanted[k] |= kind
	f.asked = append(f.asked, askedDigest{k, kind})
	if len(f.asked) == 1 && f.opts.Asked != nil {
		f.opts.Asked()
	}
}

// RetainAsked retains, reading them back, the folded spans rendering asked
// about since it last ran (see ask), reporting whether any is read back. A
// frontend runs it once a frame that asked is drawn: asking happens while
// rendering, which mustn't change the DB.
func (f *Fold) RetainAsked() bool {
	if f == nil || len(f.asked) == 0 {
		return false
	}
	settle := f.db.deferSpanOrder()
	defer settle()
	before := f.Loads + len(f.load)
	for _, a := range f.asked {
		if a.kind&digestCall != 0 {
			for j := f.byCall.head(a.key); j != 0 && j != chainEnd; j = f.callNext[j-1] {
				f.promote(j-1, noSpan)
			}
		}
		if a.kind&digestOutput != 0 {
			for j := f.byOutput.head(a.key); j != 0 && j != chainEnd; j = f.outputNext[j-1] {
				f.promote(j-1, noSpan)
			}
		}
	}
	f.asked = f.asked[:0]
	asked := f.Loads+len(f.load) > before
	f.finishBatch()
	return asked
}

// Asked reports whether rendering asked about digests RetainAsked hasn't
// acted on yet.
func (f *Fold) Asked() bool {
	return f != nil && len(f.asked) > 0
}
