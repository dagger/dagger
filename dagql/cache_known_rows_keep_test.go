package dagql

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// keepTestBundle is a bundle with every kind of thing that names a row by
// ordinal, and rows nothing stands on before, between and after the rows
// kept, each row's sender number its ordinal plus 100:
//
//	1  held, nothing below: left out
//	2  the root, held: links 1, 3, 6, 9, 11, 14
//	3  held, on the way to 5: links 4, 5; left out, the root linking 5
//	4  held, nothing below: left out
//	5  held, an output with an owner (12) and a service (13)
//	6  in full, a dependency (7) and an offer whose owner names 8 and whose
//	   service is 9
//	7  held, a full row's dependency, links 10
//	8  held, an offer's owner names it
//	9  held, an offer's service; the root links it
//	10 held, nothing below: left out
//	11 held, an output whose prefix is 5's; links 13
//	12 held, an output's owner names it
//	13 held, an output's service
//	14 held, nothing below: left out
//
// with a call on 11 and chains on 5 and 11.
func keepTestBundle() (ValueBundle, *SelectedChains, []*capturedTransferRow) {
	held := func(o uint64, deps ...uint64) TransferredValue {
		return TransferredValue{Ordinal: TransferOrdinal(o), SenderNumber: 100 + o, Held: digest.FromString(fmt.Sprint("held ", o)), DependencyIDs: deps}
	}
	full := TransferredValue{Ordinal: 6, SenderNumber: 106, DependencyIDs: []uint64{7}}
	full.Record.Envelope.PendingOffers = []PersistedPartOffer{{
		Address: PersistedPartAddress{Part: "offered"},
		Owner:   PersistedOfferOwner{DependencyIDs: []uint64{8}},
		Value:   SnapshotValue{Kind: "directory", Services: []TransferredServiceBinding{{ServiceResultID: 9}}},
	}}
	bundle := ValueBundle{
		Version: ValueBundleVersion,
		Roots:   []TransferredRoot{{Ordinal: 2}},
		Values: []TransferredValue{
			held(1), held(2, 1, 3, 6, 9, 11, 14), held(3, 4, 5), held(4), held(5), full, held(7, 10),
			held(8), held(9), held(10), held(11, 13), held(12), held(13), held(14),
		},
		Outputs: []TransferredOutput{
			{
				Ordinal: 5,
				Address: PersistedPartAddress{Part: "snapshot"},
				State:   "completed",
				Owner:   &PersistedOfferOwner{DependencyIDs: []uint64{12}},
				Value:   &SnapshotValue{Kind: "directory", Services: []TransferredServiceBinding{{ServiceResultID: 13}}},
			},
			{
				Ordinal: 11,
				Address: PersistedPartAddress{Part: "fs"},
				State:   "completed",
				Prefix:  &ChainPrefix{Ordinal: 5, Address: PersistedPartAddress{Part: "snapshot"}, Layers: 1},
			},
		},
		Calls: []TransferredCall{{Call: "call", Value: 11}},
	}
	chains := &SelectedChains{Entries: []SelectedChain{{Ordinal: 5}, {Ordinal: 11}}}
	order := make([]*capturedTransferRow, len(bundle.Values))
	for i := range order {
		order[i] = &capturedTransferRow{ordinal: TransferOrdinal(i + 1)}
	}
	return bundle, chains, order
}

// bundleReferences lists every reference a bundle makes to a row, each as
// what makes it and the sender number of the row it names, sorted: what a
// renumbering must leave as it was for every row it keeps.
func bundleReferences(bundle ValueBundle, chains *SelectedChains) []string {
	sender := map[uint64]uint64{}
	for _, v := range bundle.Values {
		sender[uint64(v.Ordinal)] = v.SenderNumber
	}
	var refs []string
	add := func(kind string, from uint64, to uint64) {
		refs = append(refs, fmt.Sprintf("%s %d -> %d", kind, from, sender[to]))
	}
	for _, v := range bundle.Values {
		for _, dep := range v.DependencyIDs {
			add("dependency", v.SenderNumber, dep)
		}
		for _, offer := range v.Record.Envelope.PendingOffers {
			for _, dep := range offer.Owner.DependencyIDs {
				add("offer owner", v.SenderNumber, dep)
			}
			for _, svc := range offer.Value.Services {
				add("offer service", v.SenderNumber, svc.ServiceResultID)
			}
		}
	}
	for _, root := range bundle.Roots {
		add("root", 0, uint64(root.Ordinal))
	}
	for _, out := range bundle.Outputs {
		from := sender[uint64(out.Ordinal)]
		add("output "+string(out.Address.Part), 0, uint64(out.Ordinal))
		if out.Prefix != nil {
			add("output prefix", from, uint64(out.Prefix.Ordinal))
		}
		if out.Owner != nil {
			for _, dep := range out.Owner.DependencyIDs {
				add("output owner", from, dep)
			}
		}
		if out.Value != nil {
			for _, svc := range out.Value.Services {
				add("output service", from, svc.ServiceResultID)
			}
		}
	}
	for _, call := range bundle.Calls {
		add("call", 0, uint64(call.Value))
	}
	for _, entry := range chains.Entries {
		add("chain", 0, uint64(entry.Ordinal))
	}
	sort.Strings(refs)
	return refs
}

// Leaving out the rows nothing stands on renumbers every reference a bundle
// makes to a row it keeps, so that each still names the same row, by its
// sender's number: a value's own ordinal and its links, a full row's
// offers' owners and services, the roots, an output's row, prefix, owner
// and services, the calls and the chains. The kept rows' ordinals are dense,
// in their order, and the captured rows the bundle keeps take theirs. A
// renumbering that is wrong but still a valid bundle would land a part or an
// offer on the wrong entry; this would see it. Row 3, a link on the way to
// 5, goes with the rows nothing is needed below, and the root links 5.
func TestKeepNeededRowsRenumbersEveryRowReference(t *testing.T) {
	t.Parallel()
	bundle, chains, order := keepTestBundle()
	before := bundleReferences(bundle, chains)
	rows, leftOut, kept := keepNeededRows(&bundle, chains, order)
	require.Empty(t, kept)
	require.Equal(t, 5, leftOut, "1, 3, 4, 10 and 14")
	var senders []uint64
	for i, v := range bundle.Values {
		require.Equal(t, TransferOrdinal(i+1), v.Ordinal, "dense ordinals")
		require.Equal(t, v.Ordinal, rows[i].ordinal, "the captured row takes its value's ordinal")
		senders = append(senders, v.SenderNumber)
	}
	require.Equal(t, []uint64{102, 105, 106, 107, 108, 109, 111, 112, 113}, senders)
	// Of the references before, those that are a kept row's: every one but
	// those to or from the rows left out, and the root's link to 5 in place
	// of 3's.
	left := map[string]bool{"101": true, "103": true, "104": true, "110": true, "114": true}
	want := []string{"dependency 102 -> 105"}
	for _, ref := range before {
		from, to, _ := strings.Cut(ref, " -> ")
		if !left[to] && !left[from[strings.LastIndex(from, " ")+1:]] {
			want = append(want, ref)
		}
	}
	sort.Strings(want)
	require.Equal(t, want, bundleReferences(bundle, chains))
}

// A bundle keepNeededRows can't renumber is sent as it is, with the reason,
// which an export's line reports: values that aren't the captured rows, one
// each at its ordinal, or a needed row the kept links don't reach.
func TestKeepNeededRowsSaysWhyItKeepsEveryRow(t *testing.T) {
	t.Parallel()
	bundle, chains, order := keepTestBundle()
	_, leftOut, kept := keepNeededRows(&bundle, chains, order[1:])
	require.Equal(t, keptAllRows, kept)
	require.Zero(t, leftOut)

	bundle, chains, order = keepTestBundle()
	order[3] = &capturedTransferRow{ordinal: 9}
	_, _, kept = keepNeededRows(&bundle, chains, order)
	require.Equal(t, keptAllOrdinals, kept)

	// 9 is named only as a service once the root's link to it goes: no
	// kept link reaches it.
	bundle, chains, order = keepTestBundle()
	bundle.Values[1].DependencyIDs = []uint64{1, 3, 6, 11, 14}
	_, _, kept = keepNeededRows(&bundle, chains, order)
	require.Equal(t, keptAllUnreached, kept)
	require.Len(t, bundle.Values, 14, "sent as it is")
}

// A row an output's owner names, and that the root reaches through held rows
// nothing stands on, is linked from the root all the same: the Cloud drops
// the outputs before it walks the roots' closure again (checkValueMerge), so
// the owner's edge to it doesn't stand in for the links it went by. Here the
// root holds a completed snapshot whose owner names 3, below 2.
func TestKeepNeededRowsLinksAnOwnersRowThroughDependencies(t *testing.T) {
	t.Parallel()
	held := func(o uint64, deps ...uint64) TransferredValue {
		return TransferredValue{Ordinal: TransferOrdinal(o), SenderNumber: 100 + o, Held: digest.FromString(fmt.Sprint("owner ", o)), DependencyIDs: deps}
	}
	bundle := ValueBundle{
		Version: ValueBundleVersion,
		Roots:   []TransferredRoot{{Ordinal: 1}},
		Values:  []TransferredValue{held(1, 2), held(2, 3), held(3)},
		Outputs: []TransferredOutput{{
			Ordinal: 1,
			Address: PersistedPartAddress{Part: "snapshot"},
			State:   "completed",
			Owner:   &PersistedOfferOwner{DependencyIDs: []uint64{3}},
		}},
	}
	order := []*capturedTransferRow{{ordinal: 1}, {ordinal: 2}, {ordinal: 3}}
	_, leftOut, kept := keepNeededRows(&bundle, nil, order)
	require.Empty(t, kept)
	require.Equal(t, 1, leftOut, "2, nothing stands on it")
	require.Equal(t, []uint64{101, 103}, []uint64{bundle.Values[0].SenderNumber, bundle.Values[1].SenderNumber})
	require.Equal(t, []uint64{2}, bundle.Values[0].DependencyIDs, "the root links the owner's row")
	// The Cloud's second walk, without the outputs.
	withoutParts := bundle
	withoutParts.Outputs = nil
	rows := make(bundleRows, len(withoutParts.Values)+1)
	for i := range withoutParts.Values {
		rows[withoutParts.Values[i].Ordinal] = &withoutParts.Values[i]
	}
	_, err := bundleClosure(withoutParts, rows, nil)
	require.NoError(t, err)
}

// cloudStateOf describes every entry of a Cloud by its recipe: its
// dependencies' recipes, its holdings, whether it holds a value and is
// retained as a stored root.
func cloudStateOf(t *testing.T, cloud *Cache) map[string]string {
	t.Helper()
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	recipeOf := map[sharedResultID]string{}
	for recipe, id := range cloud.entriesByRecipe {
		recipeOf[id] = recipe.String()
	}
	state := map[string]string{}
	for id, recipe := range recipeOf {
		res := cloud.resultsByID[id]
		if res == nil {
			continue
		}
		var deps []string
		for _, dep := range res.deps {
			deps = append(deps, recipeOf[dep])
		}
		sort.Strings(deps)
		var holders []string
		for _, ref := range res.holderRefsLocked() {
			holders = append(holders, fmt.Sprintf("%s/%d", ref.key.Cache, ref.key.Number))
		}
		sort.Strings(holders)
		_, retained := cloud.persistedEdgesByResult[id]
		state[recipe] = fmt.Sprintf("deps=%v holders=%v value=%v retained=%v", deps, holders, !res.noValueLocked(), retained)
	}
	return state
}

// A content export that leaves out the known rows nothing stands on merges
// into the Cloud as the whole bundle does: the same entries, with the same
// dependencies, holdings, values and stored roots, and each of its outputs
// lands on the same entry, which is where the service stores the part. Here
// one Cloud merges the export with the rows left out and another the same
// capture with every known row, after both merged the engine's first
// export. The root was changed since, so it goes in full, with a new row
// beside a known side subtree.
func TestKeepNeededRowsMergesAsTheWholeBundleDoes(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	base := persistedListTestResult(t, ctx, c, srv, "whole-base", String("base"))
	side := persistedListTestResult(t, ctx, c, srv, "whole-side", String("side"))
	core := persistedListTestResult(t, ctx, c, srv, "whole-core", String("core"))
	out := persistedListTestResult(t, ctx, c, srv, "whole-out", &transferTestValue{Text: "pending"})
	mid := persistedListTestResult(t, ctx, c, srv, "whole-mid", String("mid"))
	root := persistedListTestResult(t, ctx, c, srv, "whole-root", String("root"))
	transferTestDependency(c, ctx, side, base)
	transferTestDependency(c, ctx, out, core)
	transferTestDependency(c, ctx, mid, out)
	transferTestDependency(c, ctx, root, mid)
	transferTestDependency(c, ctx, root, side)
	known := NewKnownRows()
	first, rows, _ := recordsOnlyCapture(t, ctx, c, known, root)
	known.Merged(rows)
	fresh := persistedListTestResult(t, ctx, c, srv, "whole-new", String("new"))
	transferTestDependency(c, ctx, root, fresh)

	kept := contentCapture(t, ctx, c, known, root, out)
	require.Positive(t, kept.LeftOutHeld)
	c.testKeepEveryKnownRow = true
	whole := contentCapture(t, ctx, c, known, root, out)
	c.testKeepEveryKnownRow = false
	require.Zero(t, whole.LeftOutHeld)
	require.Greater(t, len(whole.Bundle.Values), len(kept.Bundle.Values))

	landed := func(bundle ValueBundle) (map[string]string, map[string]string) {
		cctx, cloud := storedPartTestCache(t, WithBlobStore())
		reply, err := cloud.MergeValues(cctx, "engine", first)
		require.NoError(t, err)
		require.True(t, reply.Committed)
		storedTestCompress(cloud)
		reply, err = cloud.MergeValues(cctx, "engine", bundle)
		require.NoError(t, err)
		require.True(t, reply.Committed)
		require.Empty(t, reply.Stale)
		number := map[TransferOrdinal]uint64{}
		for _, v := range reply.Values {
			number[v.Ordinal] = v.Number
		}
		cloud.egraphMu.RLock()
		recipeOf := map[sharedResultID]string{}
		for recipe, id := range cloud.entriesByRecipe {
			recipeOf[id] = recipe.String()
		}
		cloud.egraphMu.RUnlock()
		outputs := map[string]string{}
		for _, o := range bundle.Outputs {
			outputs[string(o.Address.Part)] = recipeOf[sharedResultID(number[o.Ordinal])]
		}
		return cloudStateOf(t, cloud), outputs
	}
	keptState, keptOutputs := landed(kept.Bundle)
	wholeState, wholeOutputs := landed(whole.Bundle)
	require.NotEmpty(t, keptOutputs)
	require.Equal(t, wholeOutputs, keptOutputs, "each output lands on the same entry")
	require.Equal(t, wholeState, keptState, "the same entries, dependencies, holdings, values and stored roots")
}
