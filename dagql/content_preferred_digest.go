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
// ignored. Unlike the historical runtime ContentPreferredDigest, it does not
// retain a memo across observations, since inputs may learn content later.
func (frame *ResultCall) ContentPreferredDigestForTelemetry(ctx context.Context) (digest.Digest, error) {
	c, err := EngineCache(ctx)
	if err != nil {
		return "", err
	}
	return frame.contentPreferredDigestForTelemetry(c)
}

func (frame *ResultCall) contentPreferredDigestForTelemetry(c *Cache) (digest.Digest, error) {
	return newContentPreferredDigestTraversal(c).digest(frame)
}

// Memoize within a traversal, not permanently on the frame: a shared input can
// learn content after a parent was hashed. Pin each shared result's frame on
// first use so a diamond observes the same input throughout this traversal.
// An empty memo entry marks an active frame, including detached frame cycles.
type contentPreferredDigestTraversal struct {
	cache   *Cache
	frames  map[*ResultCall]digest.Digest
	results map[uint64]*ResultCall
}

func newContentPreferredDigestTraversal(c *Cache) *contentPreferredDigestTraversal {
	return &contentPreferredDigestTraversal{cache: c}
}

func (walk *contentPreferredDigestTraversal) digest(frame *ResultCall) (digest.Digest, error) {
	if frame == nil {
		return "", nil
	}
	if content := frame.ContentDigest(); content != "" {
		return content, nil
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
