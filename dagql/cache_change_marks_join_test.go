package dagql

import (
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// joinTestCache is a blob-backed cache's change marks over classes made by
// hand, joined as mergeEqClassesNoRepairLocked joins them: entries in one
// class or two, each storing a part, uploading one, marking one absent, or
// holding none.
type joinTestCache struct {
	*Cache
	next sharedResultID
}

func newJoinTestCache() *joinTestCache {
	return &joinTestCache{Cache: &Cache{
		resultsByID:           map[sharedResultID]*sharedResult{},
		resultOutputEqClasses: idSetIndex[sharedResultID, eqClassID]{},
		outputEqClassResults:  idSetIndex[eqClassID, sharedResultID]{},
		egraphParents:         []eqClassID{0},
		changes:               &changeMarks{},
		uploads:               map[sharedResultID]map[string]int{},
	}}
}

// class adds an empty class and returns its id.
func (c *joinTestCache) class() eqClassID {
	class := eqClassID(len(c.egraphParents))
	c.egraphParents = append(c.egraphParents, class)
	return class
}

// entry adds an entry to classes, holding what holds says: "stores",
// "uploads", "absent" or nothing.
func (c *joinTestCache) entry(holds string, classes ...eqClassID) sharedResultID {
	c.next++
	id := c.next
	res := &sharedResult{id: id}
	switch holds {
	case "stores":
		res.storedParts = storedPartList{{key: "part"}}
	case "uploads":
		c.uploads[id] = map[string]int{"part": 1}
	case "absent":
		res.partFacts = &partFacts{declared: []declaredPart{{key: "part", state: "absent"}}}
	}
	c.resultsByID[id] = res
	for _, class := range classes {
		c.outputEqClassResults.Insert(class, id)
		c.resultOutputEqClasses.Insert(id, class)
	}
	return id
}

// join joins the classes rooted at ra and rb, ra surviving, as
// mergeEqClassesNoRepairLocked does: the join's marks, then rb's entries
// moved to ra. It returns the entries the join named.
func (c *joinTestCache) join(ra, rb eqClassID) []sharedResultID {
	dst, src := c.outputEqClassResults.Members(ra), c.outputEqClassResults.Members(rb)
	before := len(c.changes.entries)
	c.markJoinLocked(src, dst, ra)
	named := append([]sharedResultID(nil), c.changes.entries[before:]...)
	c.egraphParents[rb] = ra
	for id := range src.All() {
		c.resultOutputEqClasses.Rename(id, rb, ra)
	}
	c.outputEqClassResults.Move(rb, ra)
	return named
}

// leave takes the entry out of its classes and the cache, as a collection
// does (markLeavingLocked).
func (c *joinTestCache) leave(id sharedResultID) {
	c.markLeavingLocked(id)
	for class := range c.resultOutputEqClasses.All(id) {
		c.outputEqClassResults.Remove(c.eqClassRootLocked(class), id)
	}
	c.resultOutputEqClasses.Delete(id)
	delete(c.resultsByID, id)
	delete(c.uploads, id)
}

// A join stamps the entries of the class it joined, and no other class's: a
// lender of the smaller side that is also in a third class gives that
// class's other entries nothing new to borrow, so they stay current. The
// larger side's entries, which can borrow the lender's part now, are
// stamped.
func TestClassJoinStampsOnlyTheClassItJoined(t *testing.T) {
	t.Parallel()
	c := newJoinTestCache()
	small, large, third := c.class(), c.class(), c.class()
	lender := c.entry("stores", small, third)
	bystander := c.entry("", third)
	var borrowers []sharedResultID
	for range 5 {
		borrowers = append(borrowers, c.entry("", large))
	}
	start := c.writes
	c.join(small, large)
	var change RemoteChange
	c.takeChangesLocked(&change)
	for _, id := range borrowers {
		require.Greater(t, c.resultsByID[id].changedAt, start, "entry %d can borrow the lender's part now", id)
	}
	require.Greater(t, c.resultsByID[lender].changedAt, start, "the lender joined a class")
	require.LessOrEqual(t, c.resultsByID[bystander].changedAt, start, "the third class gained nothing to borrow")
}

// holds reports whether an entry of side holds a part (holdsAnyPartLocked).
func (c *joinTestCache) holds(side idMembers[sharedResultID]) bool {
	for id := range side.All() {
		if c.holdsAnyPartLocked(c.resultsByID[id]) {
			return true
		}
	}
	return false
}

// lends reports whether an entry of side stores a part.
func (c *joinTestCache) lends(side idMembers[sharedResultID]) bool {
	for id := range side.All() {
		if len(c.resultsByID[id].storedParts) > 0 {
			return true
		}
	}
	return false
}

// checkedJoin joins the classes rooted at ra and rb, requires the join to
// name every entry the exact rule names, and adds those to wantNamed and the
// entries the rule stamps to wantStamped. It returns how many entries the
// rule named and how many the join named.
func (c *joinTestCache) checkedJoin(t *testing.T, seed uint64, ra, rb eqClassID, wantNamed, wantStamped map[sharedResultID]bool) (exact, named int) {
	t.Helper()
	dst, src := c.outputEqClassResults.Members(ra), c.outputEqClassResults.Members(rb)
	joinNames := map[sharedResultID]bool{}
	for _, side := range []struct{ names, other idMembers[sharedResultID] }{{dst, src}, {src, dst}} {
		otherHolds, otherLends := c.holds(side.other), c.lends(side.other)
		for id := range side.names.All() {
			if otherHolds {
				joinNames[id] = true
				wantNamed[id] = true
			}
			if otherLends {
				wantStamped[id] = true
			}
		}
	}
	got := c.join(ra, rb)
	gotNamed := map[sharedResultID]bool{}
	for _, id := range got {
		gotNamed[id] = true
	}
	for id := range joinNames {
		require.True(t, gotNamed[id], "seed %d: entry %d gained a holder in a join, and the join doesn't name it", seed, id)
	}
	return len(joinNames), len(got)
}

// A join of two classes marks, for readers and for the stamps of the bundles
// that borrow from the class, at least what each side gains from the other
// (markJoinLocked): by the exact rule, a side's entries are named when the
// other side holds a part, by storing one, uploading one or marking one
// absent, and stamped when the other side stores one. Over random classes of
// entries, some in two classes, and random writes, joins, parts stored and
// dropped, entries leaving and holds ending, every entry the exact rule
// names at a join is named by that join and in the change the write hands
// back, and every entry it stamps has a stamp past the hold's start as soon
// as the hold ends, unless it has left since.
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
	c := newJoinTestCache()
	holdings := []string{"stores", "uploads", "absent", "", "", ""}
	var classes []eqClassID
	for range 2 + rng.IntN(7) {
		class := c.class()
		classes = append(classes, class)
		size := 1 + rng.IntN(4)
		if rng.IntN(3) == 0 {
			size = 1 + rng.IntN(80)
		}
		for range size {
			c.entry(holdings[rng.IntN(len(holdings))], class)
		}
	}
	// Some entries are in two classes.
	for range rng.IntN(4) {
		a, b := classes[rng.IntN(len(classes))], classes[rng.IntN(len(classes))]
		if a != b {
			c.entry(holdings[rng.IntN(len(holdings))], a, b)
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
		for id := sharedResultID(1); id <= c.next; id++ {
			if c.resultsByID[id] != nil {
				out = append(out, id)
			}
		}
		return out
	}
	for range 3 + rng.IntN(4) {
		start := c.writes
		wantStamped := map[sharedResultID]bool{}
		// wantNamed are the entries the write's joins must name, in the
		// change it hands back as in each join's own names.
		wantNamed := map[sharedResultID]bool{}
		// checkStamps requires, as a hold of the write ends, every entry a
		// join of the write must stamp to be stamped.
		checkStamps := func() {
			for id := range wantStamped {
				if res := c.resultsByID[id]; res != nil {
					require.Greater(t, res.changedAt, start, "seed %d: entry %d gained a part to borrow in a join, and isn't stamped", seed, id)
				}
			}
		}
		for range 1 + rng.IntN(6) {
			switch op := rng.IntN(20); {
			case op < 11:
				roots := live()
				if len(roots) < 2 {
					continue
				}
				i := rng.IntN(len(roots))
				j := (i + 1 + rng.IntN(len(roots)-1)) % len(roots)
				e, n := c.checkedJoin(t, seed, roots[i], roots[j], wantNamed, wantStamped)
				exact += e
				named += n
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
				c.leave(ids[rng.IntN(len(ids))])
			default:
				// A hold of the write ends: its stamps apply, its marks stay.
				// Joins after it must stamp again, past it.
				c.stampPendingLocked()
				checkStamps()
				start, wantStamped = c.writes, map[sharedResultID]bool{}
			}
		}
		var change RemoteChange
		c.takeChangesLocked(&change)
		checkStamps()
		handed := map[uint64]bool{}
		for _, id := range change.Entries {
			handed[id] = true
		}
		for id := range wantNamed {
			if c.resultsByID[id] != nil {
				require.True(t, handed[uint64(id)], "seed %d: entry %d gained a holder in a join, and the change handed back doesn't name it", seed, id)
			}
		}
	}
	return exact, named
}
