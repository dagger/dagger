package dagql

import (
	"iter"
)

// digestPostings are, for each digest, the results posted under it: the
// postings runtime makes, each digest's own (exact), and the snapshot of an
// output class taken at import (imported). All digests of a restored class
// share its snapshot; copying its results once per digest would take
// quadratic space.
//
// Nearly every digest has one exact result, which exact holds in place
// (idSetIndex). Runtime insertions stay local to the digest. Removal is only
// used when a result is collected globally, so it also removes the result
// from the shared snapshot. All access is protected by Cache.egraphMu.
type digestPostings struct {
	exact    idSetIndex[string, sharedResultID]
	imported map[string]*idSet[sharedResultID]
}

// Contains reports whether id is posted under dig.
func (p *digestPostings) Contains(dig string, id sharedResultID) bool {
	return p.imported[dig].Contains(id) || p.exact.Contains(dig, id)
}

// Insert posts id under dig, and reports whether it wasn't.
func (p *digestPostings) Insert(dig string, id sharedResultID) bool {
	if p.imported[dig].Contains(id) {
		return false
	}
	return p.exact.Insert(dig, id)
}

// Remove takes id from dig's postings, its snapshot's included.
func (p *digestPostings) Remove(dig string, id sharedResultID) {
	if snapshot := p.imported[dig]; snapshot != nil {
		snapshot.Remove(id)
		if snapshot.Empty() {
			delete(p.imported, dig)
		}
	}
	p.exact.Remove(dig, id)
}

// Size is how many results are posted under dig.
func (p *digestPostings) Size(dig string) int {
	return p.imported[dig].Size() + p.exact.Size(dig)
}

// Has reports whether any result is posted under dig.
func (p *digestPostings) Has(dig string) bool {
	return p.Size(dig) > 0
}

// Items yields each result posted under dig once. Callers sort candidates in
// their own set; the order within a digest's postings has no meaning.
func (p *digestPostings) Items(dig string) iter.Seq[sharedResultID] {
	return func(yield func(sharedResultID) bool) {
		for id := range p.imported[dig].All() {
			if !yield(id) {
				return
			}
		}
		for id := range p.exact.All(dig) {
			if !yield(id) {
				return
			}
		}
	}
}

// Digests yields each digest with a posting once, in no order a caller may
// rely on.
func (p *digestPostings) Digests() iter.Seq[string] {
	return func(yield func(string) bool) {
		for dig := range p.exact.Keys() {
			if !yield(dig) {
				return
			}
		}
		for dig, snapshot := range p.imported {
			if snapshot.Empty() || p.exact.Size(dig) > 0 {
				continue
			}
			if !yield(dig) {
				return
			}
		}
	}
}

// Len is how many digests have a posting.
func (p *digestPostings) Len() int {
	n := p.exact.Len()
	for dig, snapshot := range p.imported {
		if !snapshot.Empty() && p.exact.Size(dig) == 0 {
			n++
		}
	}
	return n
}

// importedPostingGroup retains a restored class's original membership and
// the digests that share it. Class merges do not add digests or results to an
// imported snapshot.
type importedPostingGroup struct {
	members *idSet[sharedResultID]
	digests []string
}

// restoreDigestPostingsLocked requires egraphMu. Capture membership at import:
// later unions must not silently broaden the postings of an existing digest.
func (c *Cache) restoreDigestPostingsLocked() {
	// Rebuild both directions together, including when restoring more than once.
	c.restoredDigestGroups = make(map[sharedResultID][]*importedPostingGroup, len(c.resultsByID))
	if c.egraphResultsByDigest.imported == nil {
		c.egraphResultsByDigest.imported = make(map[string]*idSet[sharedResultID])
	}
	for outputEqID, resultIDs := range c.outputEqClassResults {
		group := &importedPostingGroup{
			members: resultIDs.Clone(),
			digests: make([]string, 0, c.eqClassToDigests[outputEqID].Size()),
		}
		for resultID := range group.members.All() {
			c.markResultBroadlyIndexedLocked(resultID)
			if resultID != 0 {
				c.restoredDigestGroups[resultID] = append(c.restoredDigestGroups[resultID], group)
			}
		}
		for dig := range c.eqClassToDigests[outputEqID].All() {
			// A digest's postings become its class's snapshot alone.
			c.egraphResultsByDigest.exact.Delete(dig)
			c.egraphResultsByDigest.imported[dig] = group.members
			group.digests = append(group.digests, dig)
		}
	}
}

// removeImportedPostingGroupsLocked requires egraphMu. Remove membership once
// per original group; visit its digests only when its last result disappears.
func (c *Cache) removeImportedPostingGroupsLocked(resultID sharedResultID, groups []*importedPostingGroup) {
	for _, group := range groups {
		group.members.Remove(resultID)
		if !group.members.Empty() {
			continue
		}
		for _, dig := range group.digests {
			// Keep later runtime postings while releasing the empty snapshot.
			if c.egraphResultsByDigest.imported[dig] == group.members {
				delete(c.egraphResultsByDigest.imported, dig)
			}
		}
		group.members = nil
		group.digests = nil
	}
}
