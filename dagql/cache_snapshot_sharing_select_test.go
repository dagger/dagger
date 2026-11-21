package dagql

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// shareTestClass builds n members of one output class: an encoded imported
// receiver missing its fs part, and n-1 imported members that hold theirs, as
// a fresh engine's class looks once most of its records have their content.
// Each member's content digest posts every other, so each reaches the whole
// class as an equivalent.
func shareTestClass(t *testing.T, ctx context.Context, c *Cache, srv *Server, label string, n int) []AnyResult {
	t.Helper()
	srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
	receiver := persistedListTestResult(t, ctx, c, srv, label+"-receiver", newShareTestValue(label+"-receiver", map[string]sharePartState{"fs": {}}))
	shareTestEncodedReceiver(t, ctx, c, receiver)
	rows := []AnyResult{receiver}
	for i := 1; i < n; i++ {
		name := fmt.Sprintf("%s-member-%d", label, i)
		row := persistedListTestResult(t, ctx, c, srv, name, newShareTestValue(name, map[string]sharePartState{"fs": {Snapshot: name + "-snap"}}))
		c.egraphMu.Lock()
		row.cacheSharedResult().imported = true
		c.egraphMu.Unlock()
		rows = append(rows, row)
	}
	shareTestUnite(t, ctx, c, label, rows...)
	return rows
}

// shareTestItem is a pass's cohort of rows, as the queue would take it.
func shareTestItem(rows []AnyResult) *snapshotShareItem {
	item := &snapshotShareItem{members: map[sharedResultID]*sharedResult{}}
	for _, row := range rows {
		item.members[row.cacheSharedResult().id] = row.cacheSharedResult()
	}
	return item
}

// shareSlotKeys names a selection's slots, receiver, donor and part, sorted.
func shareSlotKeys(slots []*shareSlot) []string {
	keys := make([]string, 0, len(slots))
	for _, slot := range slots {
		keys = append(keys, fmt.Sprintf("%d<-%d:%s", slot.receiver.row.id, slot.donor.row.id, slot.addressKey))
	}
	slices.Sort(keys)
	return keys
}

// The sharing pass's selection examines what can produce a slot: for a class
// of n members of which one lacks a part, what it examines grows with n, not
// with n squared. On a fresh engine one class of 1,190 equal results had each
// member collect every other as an equivalent under the graph lock, 1.4
// million results a pass, to give one receiver at a time its part.
func TestSnapshotSharingSelectionExaminesTheClassOnce(t *testing.T) {
	examine := func(n int) (int, []string) {
		ctx, c, srv := transferTestCache(t)
		c.snapshotManager = &shareTestManager{}
		rows := shareTestClass(t, ctx, c, srv, fmt.Sprintf("examine-%d", n), n)
		examined := 0
		c.egraphMu.Lock()
		c.testServableVisited = func(sharedResultID) { examined++ }
		c.egraphMu.Unlock()
		slots := c.selectShareSlots(ctx, shareTestItem(rows))
		c.egraphMu.Lock()
		c.testServableVisited = nil
		c.egraphMu.Unlock()
		return examined, shareSlotKeys(slots)
	}
	small, smallSlots := examine(40)
	large, largeSlots := examine(160)
	require.Len(t, smallSlots, 1, "the one receiver takes its part from one member")
	require.Len(t, largeSlots, 1, "the one receiver takes its part from one member")
	require.Positive(t, small)
	// Four times the members, examined about four times over: sixteen times
	// is each member examining the whole class.
	require.LessOrEqual(t, large, 6*small, "examined %d results for 40 members and %d for 160", small, large)
	t.Logf("examined %d results for 40 members and %d for 160", small, large)
}

// The selection decides the same slots as a selection that decides every
// eligible receiver against every member, over generated cohorts: members
// local or imported, holding parts or missing them, some expired, some whose
// requirements don't fit a receiver's, and some no route reaches.
func TestSnapshotSharingSelectionMatchesWholeCohortSelection(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	var withSlots, withExclusions int
	for i := range 40 {
		ctx, c, srv := transferTestCache(t)
		c.snapshotManager = &shareTestManager{}
		srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
		resource := persistedListTestResult(t, ctx, c, srv, fmt.Sprintf("generated-%d-resource", i), String("socket"))
		c.egraphMu.Lock()
		resource.cacheSharedResult().sessionResourceHandle = "socket"
		_, err := c.recomputeRequiredSessionResourcesLocked(resource.cacheSharedResult())
		c.egraphMu.Unlock()
		require.NoError(t, err)

		n := 2 + rng.IntN(9)
		var rows, receivers []AnyResult
		group := map[int][]AnyResult{}
		for j := range n {
			name := fmt.Sprintf("generated-%d-%d", i, j)
			parts := map[string]sharePartState{}
			encoded := rng.IntN(3) == 0
			// A member that holds some parts and lacks others is both a
			// receiver and a donor.
			for _, part := range []string{"fs", "out"} {
				switch {
				case encoded:
					parts[part] = sharePartState{}
				case rng.IntN(3) == 0:
					parts[part] = sharePartState{}
				case rng.IntN(2) == 0:
					parts[part] = sharePartState{Snapshot: name + "-" + part}
				}
			}
			row := persistedListTestResult(t, ctx, c, srv, name, newShareTestValue(name, parts))
			if encoded {
				shareTestEncodedReceiver(t, ctx, c, row)
				receivers = append(receivers, row)
			} else if rng.IntN(2) == 0 {
				c.egraphMu.Lock()
				row.cacheSharedResult().imported = true
				c.egraphMu.Unlock()
			}
			if rng.IntN(4) == 0 {
				transferTestDependency(c, ctx, row, resource)
			}
			rows = append(rows, row)
			g := rng.IntN(2)
			group[g] = append(group[g], row)
		}
		for g, members := range group {
			shareTestUnite(t, ctx, c, fmt.Sprintf("generated-%d-group-%d", i, g), members...)
		}
		// A route beside the content digest: a donor posted under a
		// receiver's own recipe.
		for _, receiver := range receivers {
			if rng.IntN(2) == 0 {
				partTestEquivalent(t, c, receiver, rows[rng.IntN(len(rows))])
			}
		}
		c.egraphMu.Lock()
		for _, row := range rows {
			_, err := c.recomputeRequiredSessionResourcesLocked(row.cacheSharedResult())
			require.NoError(t, err)
			if rng.IntN(8) == 0 {
				row.cacheSharedResult().expiresAtUnix = time.Now().Add(-time.Minute).Unix()
			}
		}
		c.egraphMu.Unlock()

		want := shareSlotKeys(referenceSelectShareSlots(ctx, c, shareTestItem(rows)))
		got := shareSlotKeys(c.selectShareSlots(ctx, shareTestItem(rows)))
		require.Equal(t, want, got, "cohort %d", i)
		if len(want) > 0 {
			withSlots++
		}
		if referenceSelectionExcludes(ctx, c, shareTestItem(rows)) {
			withExclusions++
		}
	}
	// The generated cohorts cover both: slots chosen, and a pending receiver
	// and a ready donor that no slot pairs.
	require.Positive(t, withSlots)
	require.Positive(t, withExclusions)
}

// referenceSelectShareSlots is the selection as it was when each eligible
// receiver collected every equivalent under the graph lock and was decided
// against every member: the slots the selection must keep choosing. Only the
// slot loop that reads those decisions is shared (shareSlotsFor).
func referenceSelectShareSlots(ctx context.Context, c *Cache, item *snapshotShareItem) []*shareSlot {
	slots, _ := referenceSelection(ctx, c, item)
	return slots
}

// referenceSelectionExcludes reports whether the reference selection left a
// pending receiver and a ready donor of the same part unpaired: a cohort that
// exercises an exclusion by route, requirement or expiry.
func referenceSelectionExcludes(ctx context.Context, c *Cache, item *snapshotShareItem) bool {
	_, excluded := referenceSelection(ctx, c, item)
	return excluded
}

func referenceSelection(ctx context.Context, c *Cache, item *snapshotShareItem) ([]*shareSlot, bool) {
	// From here to the slots, selectShareSlots's decisions as they were.
	rows := shareMemberOrder(item.members)
	now := time.Now().Unix()
	observed := c.probeShareMembers(ctx, rows)
	item.kind = shareClassKindOf(observed)
	lookups := make(map[sharedResultID]partLookup, len(rows))
	for _, row := range rows {
		if !row.imported {
			continue
		}
		lookup, err := c.partLookupFor(row)
		if err != nil {
			c.traceShareSkip(ctx, row, PersistedPartAddress{}, err)
			continue
		}
		lookups[row.id] = lookup
	}
	eligible := make(map[sharedResultID]bool, len(rows))
	admits := map[sharePair]bool{}
	c.lockGraphFor(ctx, workSharing)
	for _, row := range rows {
		lookup, prepared := lookups[row.id]
		eligible[row.id] = prepared && c.shareReceiverEligibleLocked(row, now)
		if !eligible[row.id] {
			continue
		}
		equivalents := referenceSessionlessPartEquivalentsLocked(c, row, lookup)
		for _, donor := range rows {
			if donor == row || partRowExpired(donor, now) {
				continue
			}
			if _, ok := equivalents[donor]; ok {
				admits[sharePair{row.id, donor.id}] = true
			}
		}
	}
	c.egraphMu.Unlock()
	// Whether the cohort left a usable receiver's pending part and a ready
	// donor of it unpaired, by eligibility, route, requirement or expiry.
	excluded := false
	for _, row := range rows {
		receiver := observed[row.id]
		if receiver == nil || !receiver.usable {
			continue
		}
		for _, key := range receiver.order {
			if !shareReceiverPending(receiver.probes[key]) {
				continue
			}
			for _, candidate := range rows {
				donor := observed[candidate.id]
				if candidate == row || donor == nil || !donor.usable {
					continue
				}
				if p, ok := donor.probes[key]; ok && shareDonorReady(p) && (!eligible[row.id] || !admits[sharePair{row.id, candidate.id}]) {
					excluded = true
				}
			}
		}
	}
	return shareSlotsFor(rows, observed, eligible, admits), excluded
}

// referenceSessionlessPartEquivalentsLocked collects every ordinary
// equivalent of receiver whose own resource requirements fit inside the
// receiver's, with the route that found it, as the selection once did for each
// receiver. E is held.
func referenceSessionlessPartEquivalentsLocked(c *Cache, receiver *sharedResult, lookup partLookup) map[*sharedResult]CacheHitRoute {
	if receiver.loadResultCall() != lookup.frame {
		return nil
	}
	candidates := c.collectPartCandidatesWithAdmissionLocked(receiver, lookup, func(row *sharedResult) bool { return c.ownPartRequirementsFitLocked(receiver, row) })
	out := make(map[*sharedResult]CacheHitRoute, len(candidates))
	for _, candidate := range candidates {
		out[candidate.row] = candidate.route
	}
	return out
}
