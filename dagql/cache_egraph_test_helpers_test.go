package dagql

import (
	"context"
	"fmt"
	"time"

	"github.com/dagger/dagger/dagql/call"
)

func (c *Cache) lookupMatchForIDLocked(ctx context.Context, id *call.ID) (lookupMatch, error) {
	match := lookupMatch{
		primaryLookupPossible: true,
		missingInputIndex:     -1,
	}
	if id == nil {
		return match, nil
	}
	nowUnix := time.Now().Unix()

	match = c.lookupMatchForDigestsLocked(id.Digest(), id.ExtraDigests(), nowUnix)
	if match.candidates != nil && !match.candidates.Empty() {
		return match, nil
	}

	selfDigest, inputDigests, err := id.SelfDigestAndInputs()
	if err != nil {
		return match, fmt.Errorf("derive call term: %w", err)
	}
	match.selfDigest = selfDigest
	match.inputDigests = inputDigests
	match.inputEqIDs = make([]eqClassID, len(inputDigests))
	for i, inDig := range inputDigests {
		classID, ok := c.egraphDigestToClass[inDig.String()]
		if !ok {
			match.primaryLookupPossible = false
			match.missingInputIndex = i
			break
		}
		root := c.findEqClassLocked(classID)
		if root == 0 {
			match.primaryLookupPossible = false
			match.missingInputIndex = i
			break
		}
		match.inputEqIDs[i] = root
	}
	if match.primaryLookupPossible {
		match.termDigest = calcEgraphTermDigest(selfDigest, match.inputEqIDs)
		termSet := c.egraphTermsByTermDigest[match.termDigest]
		if termSet != nil {
			match.termSetSize = termSet.Size()
		}
		candidates := newSharedResultSet()
		c.appendTermSetResultsLocked(candidates, termSet, nowUnix)
		if !candidates.Empty() {
			match.candidates = candidates
		}
	}
	return match, nil
}

func (c *Cache) resolveSharedResultForInputIDLocked(ctx context.Context, id *call.ID) (*sharedResult, error) {
	match, err := c.lookupMatchForIDLocked(ctx, id)
	if err != nil {
		return nil, err
	}
	if match.candidates == nil || match.candidates.Empty() {
		return nil, fmt.Errorf("no cached shared result found for structural input %s", id.Digest())
	}
	for hitRes := range match.candidates.Items() {
		return hitRes, nil
	}
	return nil, fmt.Errorf("no cached shared result found for structural input %s", id.Digest())
}
