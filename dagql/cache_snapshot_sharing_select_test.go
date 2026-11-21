package dagql

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/call"
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
// requirements don't fit a receiver's, and some no route reaches. And the
// per-row equivalence test, which a share slot's admission and an install's
// recheck use too, answers for every receiver and every member as the
// candidate collector does, with the same route: the collector is what it
// re-states, and the two may not drift.
func TestSnapshotSharingSelectionMatchesWholeCohortSelection(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	var withSlots, withExclusions int
	routes := map[CacheHitRoute]int{}
	var unrelated int
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

		// Lookup preparation takes E itself, so it runs before the section.
		lookups := map[sharedResultID]partLookup{}
		for _, row := range rows {
			if lookup, err := c.partLookupFor(row.cacheSharedResult()); err == nil {
				lookups[row.cacheSharedResult().id] = lookup
			}
		}
		var mismatches []string
		c.egraphMu.Lock()
		for _, r := range rows {
			receiver := r.cacheSharedResult()
			lookup, ok := lookups[receiver.id]
			if !ok {
				continue
			}
			want := referenceSessionlessPartEquivalentsLocked(c, receiver, lookup)
			equivalent := c.sessionlessPartEquivalenceLocked(receiver, lookup)
			for _, m := range rows {
				member := m.cacheSharedResult()
				wantRoute, wantOK := want[member]
				route, ok := equivalent(member)
				if ok != wantOK || route != wantRoute {
					mismatches = append(mismatches, fmt.Sprintf("receiver %d, member %d: %q %v, collector %q %v", receiver.id, member.id, route, ok, wantRoute, wantOK))
				}
				switch {
				case member == receiver:
				case wantOK:
					routes[wantRoute]++
				default:
					unrelated++
				}
			}
		}
		c.egraphMu.Unlock()
		require.Empty(t, mismatches, "cohort %d", i)
	}
	// The generated cohorts cover both: slots chosen, and a pending receiver
	// and a ready donor that no slot pairs; and members another reaches by
	// its recipe, by a digest, and by no route.
	require.Positive(t, withSlots)
	require.Positive(t, withExclusions)
	require.Positive(t, routes[CacheHitRouteRecipe], "a member posted under another's recipe")
	require.Positive(t, routes[CacheHitRouteDigest], "a member reached by a content digest")
	require.Positive(t, unrelated, "a member no route reaches")
}

// The per-row equivalence reaches a member structurally as the candidate
// collector does: through the receiver's term, and through the term's output
// class only when the term holds no servable result. A servable term result
// keeps the collector from the output class even when its own requirements
// don't fit the receiver's. The generated cohorts reach members by recipe and
// by content digest; these reach them by the receiver's term.
func TestSessionlessPartEquivalenceStructuralRoutes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                          string
		receiverExpired, donorExpired bool
		donorNeedsResource            bool
		// The routes by which the receiver reaches the donor, whose input
		// is taught equivalent to the receiver's, and the class mate, which
		// shares only the donor's content digest; "" is neither.
		donor, classMate CacheHitRoute
	}{
		{name: "through the term", donor: CacheHitRouteStructural},
		{name: "through the output class", receiverExpired: true, donorExpired: true, classMate: CacheHitRouteStructural},
		{name: "a servable receiver keeps it from the output class", donorExpired: true},
		{name: "a servable donor whose requirements don't fit keeps it from the output class", receiverExpired: true, donorNeedsResource: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, c, srv := transferTestCache(t)
			srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
			srcA, srcB := digest.FromString("structural-src-a"), digest.FromString("structural-src-b")
			receiver := shareTestStructuralResult(t, ctx, c, srv, srcA, newShareTestValue("receiver", map[string]sharePartState{"fs": {}}))
			donor := shareTestStructuralResult(t, ctx, c, srv, srcB, newShareTestValue("donor", map[string]sharePartState{"fs": {Snapshot: "donor-fs"}}))
			classMate := persistedListTestResult(t, ctx, c, srv, "class-mate", newShareTestValue("class-mate", map[string]sharePartState{"fs": {Snapshot: "mate-fs"}}))
			// Teaching the inputs equivalent makes the two operations one
			// term and merges their output classes; the class mate joins
			// that class through the donor's content digest.
			shareTestTeachEquivalent(t, ctx, c, srv, "structural-teacher", srcA, srcB)
			shareTestUnite(t, ctx, c, "structural-donor-content", donor, classMate)
			if tc.donorNeedsResource {
				resource := persistedListTestResult(t, ctx, c, srv, "resource", String("socket"))
				c.egraphMu.Lock()
				resource.cacheSharedResult().sessionResourceHandle = "socket"
				_, err := c.recomputeRequiredSessionResourcesLocked(resource.cacheSharedResult())
				c.egraphMu.Unlock()
				require.NoError(t, err)
				transferTestDependency(c, ctx, donor, resource)
			}
			c.egraphMu.Lock()
			var requirementErr error
			for _, row := range []AnyResult{receiver, donor, classMate} {
				if _, err := c.recomputeRequiredSessionResourcesLocked(row.cacheSharedResult()); err != nil {
					requirementErr = err
				}
			}
			expired := time.Now().Add(-time.Minute).Unix()
			if tc.receiverExpired {
				receiver.cacheSharedResult().expiresAtUnix = expired
			}
			if tc.donorExpired {
				donor.cacheSharedResult().expiresAtUnix = expired
			}
			c.egraphMu.Unlock()
			require.NoError(t, requirementErr)

			lookup, err := c.partLookupFor(receiver.cacheSharedResult())
			require.NoError(t, err)
			rows := map[string]*sharedResult{"donor": donor.cacheSharedResult(), "class mate": classMate.cacheSharedResult()}
			got, collected := map[string]CacheHitRoute{}, map[string]CacheHitRoute{}
			c.egraphMu.Lock()
			equivalent := c.sessionlessPartEquivalenceLocked(receiver.cacheSharedResult(), lookup)
			want := referenceSessionlessPartEquivalentsLocked(c, receiver.cacheSharedResult(), lookup)
			for name, row := range rows {
				got[name], _ = equivalent(row)
				collected[name] = want[row]
			}
			c.egraphMu.Unlock()
			expected := map[string]CacheHitRoute{"donor": tc.donor, "class mate": tc.classMate}
			require.Equal(t, expected, collected, "the candidate collector")
			require.Equal(t, expected, got, "the per-row equivalence")
		})
	}
}

// shareTestStructuralResult is a result of one operation over an input
// witnessed by src: results over inputs taught equivalent share the
// operation's term, each under a recipe of its own.
func shareTestStructuralResult(t *testing.T, ctx context.Context, c *Cache, srv *Server, src digest.Digest, value Typed) AnyResult {
	t.Helper()
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Field: "structural-op",
		Type:  NewResultCallType(value.Type()),
		Args: []*ResultCallArg{{
			Name: "src",
			Value: &ResultCallLiteral{
				Kind:                 ResultCallLiteralKindDigestedString,
				DigestedStringValue:  "witnessed",
				DigestedStringDigest: src,
			},
		}},
	}
	res, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(value, frame)
	})
	require.NoError(t, err)
	return res
}

// shareTestTeachEquivalent teaches digs equivalent by publishing a result
// that carries them all as content digests, which puts them in one class.
func shareTestTeachEquivalent(t *testing.T, ctx context.Context, c *Cache, srv *Server, field string, digs ...digest.Digest) {
	t.Helper()
	extras := make([]call.ExtraDigest, 0, len(digs))
	for _, dig := range digs {
		extras = append(extras, call.ExtraDigest{Label: call.ExtraDigestLabelContent, Digest: dig})
	}
	frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(String("").Type()), ExtraDigests: extras}
	_, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(String(field), frame)
	})
	require.NoError(t, err)
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
