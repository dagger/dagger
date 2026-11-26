package dagql

import (
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// A join of two classes marks, for readers and for the stamps of the bundles
// that borrow from the class, at least what each side gains from the other
// (markJoinLocked): by the exact rule, a side's entries are named when the
// other side holds a part, by storing one, uploading one or marking one
// absent, and stamped when the other side stores one. Over random classes of
// entries, some in two classes, and random writes between hand-backs, joins,
// parts stored and dropped, entries leaving and holds ending, every entry the
// exact rule names at a join is in the change handed back, and every entry it
// stamps has a stamp past the write's start, unless it has left since.
func TestClassJoinMarksWhatEachSideGains(t *testing.T) {
	t.Parallel()
	var exact, named int
	for seed := range uint64(300) {
		e, n := checkClassJoinMarks(t, seed)
		exact += e
		named += n
	}
	t.Logf("joins named %d entries where the exact rule names %d", named, exact)
}

// checkClassJoinMarks runs one seed's writes, and returns how many entries
// the exact rule named at its joins, and how many the joins named.
func checkClassJoinMarks(t *testing.T, seed uint64) (exact, named int) {
	t.Helper()
	rng := rand.New(rand.NewPCG(seed, seed))
	c := &Cache{
		resultsByID:           map[sharedResultID]*sharedResult{},
		resultOutputEqClasses: idSetIndex[sharedResultID, eqClassID]{},
		outputEqClassResults:  idSetIndex[eqClassID, sharedResultID]{},
		egraphParents:         []eqClassID{0},
		changes:               &changeMarks{},
		uploads:               map[sharedResultID]map[string]int{},
	}
	var next sharedResultID
	newEntry := func(classes ...eqClassID) {
		next++
		res := &sharedResult{id: next}
		switch rng.IntN(6) {
		case 0:
			res.storedParts = storedPartList{{key: "part"}}
		case 1:
			c.uploads[next] = map[string]int{"part": 1}
		case 2:
			res.partFacts = &partFacts{declared: []declaredPart{{key: "part", state: "absent"}}}
		}
		c.resultsByID[next] = res
		for _, class := range classes {
			c.outputEqClassResults.Insert(class, next)
			c.resultOutputEqClasses.Insert(next, class)
		}
	}
	var classes []eqClassID
	for range 2 + rng.IntN(7) {
		class := eqClassID(len(c.egraphParents))
		c.egraphParents = append(c.egraphParents, class)
		classes = append(classes, class)
		size := 1 + rng.IntN(4)
		if rng.IntN(3) == 0 {
			size = 1 + rng.IntN(80)
		}
		for range size {
			newEntry(class)
		}
	}
	// Some entries are in two classes.
	for range rng.IntN(4) {
		a, b := classes[rng.IntN(len(classes))], classes[rng.IntN(len(classes))]
		if a != b {
			newEntry(a, b)
		}
	}
	live := func() []eqClassID {
		var out []eqClassID
		for _, class := range classes {
			if c.egraphParents[class] == class && c.outputEqClassResults.Size(class) > 0 {
				out = append(out, class)
			}
		}
		return out
	}
	entries := func() []sharedResultID {
		var out []sharedResultID
		for id := sharedResultID(1); id <= next; id++ {
			if c.resultsByID[id] != nil {
				out = append(out, id)
			}
		}
		return out
	}
	holds := func(side idMembers[sharedResultID]) bool {
		for id := range side.All() {
			if c.holdsAnyPartLocked(c.resultsByID[id]) {
				return true
			}
		}
		return false
	}
	lends := func(side idMembers[sharedResultID]) bool {
		for id := range side.All() {
			if len(c.resultsByID[id].storedParts) > 0 {
				return true
			}
		}
		return false
	}
	for range 3 + rng.IntN(4) {
		start := c.writes
		wantNamed := map[sharedResultID]bool{}
		wantStamped := map[sharedResultID]bool{}
		for range 1 + rng.IntN(6) {
			switch op := rng.IntN(20); {
			case op < 11:
				roots := live()
				if len(roots) < 2 {
					continue
				}
				i := rng.IntN(len(roots))
				j := (i + 1 + rng.IntN(len(roots)-1)) % len(roots)
				ra, rb := roots[i], roots[j]
				dst, src := c.outputEqClassResults.Members(ra), c.outputEqClassResults.Members(rb)
				for _, side := range []struct{ names, other idMembers[sharedResultID] }{{dst, src}, {src, dst}} {
					otherHolds, otherLends := holds(side.other), lends(side.other)
					for id := range side.names.All() {
						if otherHolds {
							wantNamed[id] = true
							exact++
						}
						if otherLends {
							wantStamped[id] = true
						}
					}
				}
				before := len(c.changes.entries)
				// As mergeEqClassesNoRepairLocked does: the join's marks, then
				// the absorbed class's entries moved to the survivor.
				c.markJoinLocked(src, dst)
				named += len(c.changes.entries) - before
				c.egraphParents[rb] = ra
				for id := range src.All() {
					c.resultOutputEqClasses.Rename(id, rb, ra)
				}
				c.outputEqClassResults.Move(rb, ra)
			case op < 14:
				ids := entries()
				id := ids[rng.IntN(len(ids))]
				res := c.resultsByID[id]
				if len(res.storedParts) == 0 {
					res.storedParts = storedPartList{{key: "part"}}
					c.markEntryClassLocked(id, StampPartStored)
				} else {
					c.markEntryClassLocked(id, StampPartDropped)
					res.storedParts = nil
				}
			case op < 17:
				ids := entries()
				if len(ids) < 2 {
					continue
				}
				id := ids[rng.IntN(len(ids))]
				c.markLeavingLocked(id)
				for class := range c.resultOutputEqClasses.All(id) {
					c.outputEqClassResults.Remove(c.eqClassRootLocked(class), id)
				}
				c.resultOutputEqClasses.Delete(id)
				delete(c.resultsByID, id)
				delete(c.uploads, id)
			default:
				// A hold of the write ends: its stamps apply, its marks stay.
				c.stampPendingLocked()
			}
		}
		var change RemoteChange
		c.takeChangesLocked(&change)
		handed := map[uint64]bool{}
		for _, id := range change.Entries {
			handed[id] = true
		}
		for id := range wantNamed {
			if c.resultsByID[id] != nil {
				require.True(t, handed[uint64(id)], "seed %d: entry %d gained a holder in a join, and isn't named", seed, id)
			}
		}
		for id := range wantStamped {
			if res := c.resultsByID[id]; res != nil {
				require.Greater(t, res.changedAt, start, "seed %d: entry %d gained a part to borrow in a join, and isn't stamped", seed, id)
			}
		}
	}
	return exact, named
}
