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
// Nearly every digest's only posting is one exact result, which one holds in
// the map's own slot; every other digest has a posting of its own in rest. A
// digest is in one of the two maps at most, and a lookup that finds it in one
// is done. Runtime insertions stay local to the digest. Removal is only used
// when a result is collected globally, so it also removes the result from the
// shared snapshot. All access is protected by Cache.egraphMu.
type digestPostings struct {
	one  map[string]sharedResultID
	rest map[string]*digestPosting
}

// digestPosting is the postings of a digest that has more than one exact
// result, or a snapshot.
type digestPosting struct {
	exact    *idSet[sharedResultID]
	imported *idSet[sharedResultID]
}

func (p *digestPosting) size() int {
	return p.imported.Size() + p.exact.Size()
}

// Contains reports whether id is posted under dig.
func (p *digestPostings) Contains(dig string, id sharedResultID) bool {
	if only, ok := p.one[dig]; ok {
		return only == id
	}
	posting := p.rest[dig]
	return posting != nil && (posting.imported.Contains(id) || posting.exact.Contains(id))
}

// Insert posts id under dig, and reports whether it wasn't.
func (p *digestPostings) Insert(dig string, id sharedResultID) bool {
	if only, ok := p.one[dig]; ok {
		if only == id {
			return false
		}
		delete(p.one, dig)
		p.putRest(dig, &digestPosting{exact: idSetOf(only, id)})
		return true
	}
	if posting := p.rest[dig]; posting != nil {
		if posting.imported.Contains(id) {
			return false
		}
		if posting.exact == nil {
			posting.exact = newIDSet[sharedResultID]()
		}
		return posting.exact.Insert(id)
	}
	if p.one == nil {
		p.one = make(map[string]sharedResultID)
	}
	p.one[dig] = id
	return true
}

// Remove takes id from dig's postings, its snapshot's included.
func (p *digestPostings) Remove(dig string, id sharedResultID) {
	if only, ok := p.one[dig]; ok {
		if only == id {
			delete(p.one, dig)
		}
		return
	}
	posting := p.rest[dig]
	if posting == nil {
		return
	}
	posting.imported.Remove(id)
	posting.exact.Remove(id)
	p.settle(dig, posting)
}

// settle keeps dig's posting in the form it now needs: none when it holds
// nothing, its slot when it holds only one exact result.
func (p *digestPostings) settle(dig string, posting *digestPosting) {
	if posting.imported.Empty() {
		posting.imported = nil
	}
	switch {
	case posting.imported != nil || posting.exact.Size() > 1:
	case posting.exact.Size() == 1:
		delete(p.rest, dig)
		for only := range posting.exact.All() {
			if p.one == nil {
				p.one = make(map[string]sharedResultID)
			}
			p.one[dig] = only
		}
	default:
		delete(p.rest, dig)
	}
}

func (p *digestPostings) putRest(dig string, posting *digestPosting) {
	if p.rest == nil {
		p.rest = make(map[string]*digestPosting)
	}
	p.rest[dig] = posting
}

// Size is how many results are posted under dig.
func (p *digestPostings) Size(dig string) int {
	if _, ok := p.one[dig]; ok {
		return 1
	}
	if posting := p.rest[dig]; posting != nil {
		return posting.size()
	}
	return 0
}

// Has reports whether any result is posted under dig.
func (p *digestPostings) Has(dig string) bool {
	return p.Size(dig) > 0
}

// Items yields each result posted under dig once. Callers sort candidates in
// their own set; the order within a digest's postings has no meaning.
func (p *digestPostings) Items(dig string) iter.Seq[sharedResultID] {
	return func(yield func(sharedResultID) bool) {
		if only, ok := p.one[dig]; ok {
			yield(only)
			return
		}
		posting := p.rest[dig]
		if posting == nil {
			return
		}
		for id := range posting.imported.All() {
			if !yield(id) {
				return
			}
		}
		for id := range posting.exact.All() {
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
		for dig := range p.one {
			if !yield(dig) {
				return
			}
		}
		for dig, posting := range p.rest {
			// Another digest's removal can empty a shared snapshot.
			if posting.size() == 0 {
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
	n := len(p.one)
	for _, posting := range p.rest {
		if posting.size() > 0 {
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
	for outputEqID := range c.outputEqClassResults.Keys() {
		group := &importedPostingGroup{
			members: c.outputEqClassResults.Clone(outputEqID),
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
			delete(c.egraphResultsByDigest.one, dig)
			c.egraphResultsByDigest.putRest(dig, &digestPosting{imported: group.members})
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
			if posting := c.egraphResultsByDigest.rest[dig]; posting != nil && posting.imported == group.members {
				posting.imported = nil
				c.egraphResultsByDigest.settle(dig, posting)
			}
		}
		group.members = nil
		group.digests = nil
	}
}
