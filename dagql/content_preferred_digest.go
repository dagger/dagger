package dagql

import (
	"context"
	"fmt"

	"github.com/opencontainers/go-digest"
)

// ContentPreferredDigestForTelemetry prefers this operation's recorded content
// digest. Otherwise it uses recipe encoding with recursively content-preferred
// result inputs. With no recorded content anywhere, it equals RecipeDigest.
//
// This is an observation of currently available content: it neither evaluates
// results nor canonicalizes e-graph equivalences. Non-content extra digests are
// ignored. Unlike the historical runtime ContentPreferredDigest, its memo on
// the frame holds only for the frame generation it was computed in, since
// inputs may learn content later.
func (frame *ResultCall) ContentPreferredDigestForTelemetry(ctx context.Context) (digest.Digest, error) {
	c, err := EngineCache(ctx)
	if err != nil {
		return "", err
	}
	return frame.contentPreferredDigestForTelemetry(c)
}

func (frame *ResultCall) contentPreferredDigestForTelemetry(c *Cache) (digest.Digest, error) {
	return newContentPreferredDigestTraversal(c).consistentDigest(frame)
}

// Memoize within a traversal, and on the frame only for the frame generation
// the traversal started in: a shared input learns content by having its frame
// replaced (see resultCallFrameGeneration), after a parent may have been
// hashed. Pin each shared result's frame on first use so a diamond observes
// the same input throughout this traversal. An empty memo entry marks an
// active frame, including detached frame cycles.
type contentPreferredDigestTraversal struct {
	cache   *Cache
	gen     uint64
	frames  map[*ResultCall]digest.Digest
	results map[uint64]*ResultCall
	// noMemos turns the frames' memos off, reading and writing.
	noMemos bool
	// usedMemo reports that a frame's memo stood in for walking it.
	usedMemo bool
}

type contentPreferredDigestMemo struct {
	gen    uint64
	digest digest.Digest
}

func newContentPreferredDigestTraversal(c *Cache) *contentPreferredDigestTraversal {
	return &contentPreferredDigestTraversal{cache: c, gen: resultCallFrameGeneration.Load()}
}

// consistentDigest is digest, observing one state of the frames. A memo
// doesn't pin the inputs it was computed from, so if a frame was replaced
// while a walk that used memos ran, one path may have seen an input's old
// frame through a memo and another its new frame. Then walk again without
// memos, pinning each input as it's first reached, as a walk did before
// frames kept memos. The frame generation only moves forward, so a walk that
// ends in the generation it started in saw no replacement.
func (walk *contentPreferredDigestTraversal) consistentDigest(frame *ResultCall) (digest.Digest, error) {
	dig, err := walk.digest(frame)
	if walk.usedMemo && resultCallFrameGeneration.Load() != walk.gen {
		return (&contentPreferredDigestTraversal{cache: walk.cache, noMemos: true}).digest(frame)
	}
	return dig, err
}

func (walk *contentPreferredDigestTraversal) digest(frame *ResultCall) (digest.Digest, error) {
	if frame == nil {
		return "", nil
	}
	if content := frame.ContentDigest(); content != "" {
		return content, nil
	}
	if m := frame.contentPreferredTelemetryDigest.Load(); !walk.noMemos && m != nil && m.gen == walk.gen {
		walk.usedMemo = true
		return m.digest, nil
	}
	if dig, ok := walk.frames[frame]; ok {
		if dig == "" {
			return "", fmt.Errorf("cycle while reconstructing content-preferred digest")
		}
		return dig, nil
	}
	if walk.frames == nil {
		walk.frames = make(map[*ResultCall]digest.Digest)
	}
	walk.frames[frame] = ""
	dig, err := frame.digestWithInputs(walk.refDigest)
	if err != nil {
		return "", err
	}
	walk.frames[frame] = dig
	if !walk.noMemos {
		frame.contentPreferredTelemetryDigest.Store(&contentPreferredDigestMemo{gen: walk.gen, digest: dig})
	}
	return dig, nil
}

func (walk *contentPreferredDigestTraversal) refDigest(ref *ResultCallRef) (digest.Digest, error) {
	if err := ref.Validate(); err != nil {
		return "", err
	}
	if ref.Call != nil {
		return walk.digest(ref.Call)
	}
	frame := walk.results[ref.ResultID]
	if frame == nil {
		frame = ref.loadSharedCall()
		if frame == nil {
			if walk.cache == nil {
				return "", fmt.Errorf("cannot resolve result ref %d without cache", ref.ResultID)
			}
			frame = walk.cache.resultCallByResultID(sharedResultID(ref.ResultID))
			if frame == nil {
				return "", fmt.Errorf("missing result call frame for shared result %d", ref.ResultID)
			}
		}
		if walk.results == nil {
			walk.results = make(map[uint64]*ResultCall)
		}
		walk.results[ref.ResultID] = frame
	}
	return walk.digest(frame)
}
