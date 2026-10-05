package dagql

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// storedBundleTestCloud merges an export of a root R, which depends on P,
// into a new Cloud cache, and returns the Cloud's numbers for R and P.
func storedBundleTestCloud(t *testing.T, text string) (context.Context, *Cache, uint64, uint64) {
	t.Helper()
	actx, a, asrv := transferTestCache(t)
	r := persistedListTestResult(t, actx, a, asrv, "bundle-root", &transferTestValue{Text: text})
	p := persistedListTestResult(t, actx, a, asrv, "bundle-dep", &transferTestValue{Text: "dep"})
	transferTestDependency(a, actx, r, p)
	bundle := exportTestBundle(t, actx, a, r)
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	var rNumber, pNumber uint64
	for _, value := range reply.Values {
		for _, sent := range bundle.Values {
			if sent.Ordinal != value.Ordinal {
				continue
			}
			switch sent.SenderNumber {
			case uint64(r.cacheSharedResult().id):
				rNumber = value.Number
			case uint64(p.cacheSharedResult().id):
				pNumber = value.Number
			}
		}
	}
	require.NotZero(t, rNumber)
	require.NotZero(t, pNumber)
	return ctx, cloud, rNumber, pNumber
}

// storedBundleTestEntry returns the Cloud entry with the number.
func storedBundleTestEntry(c *Cache, number uint64) *sharedResult {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.resultsByID[sharedResultID(number)]
}

// A stored bundle carries the records of the roots' closure, named by the
// Cloud's numbers and replacement counts, and each entry's own stored parts,
// expired or not, with the entry as the output's storing entry. A root the
// cache doesn't have is gone, and one known only through holdings has no
// value. The bundle merges into an engine.
func TestStoredBundleCarriesTheClosureAndItsOwnParts(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	stored, err := cloud.SetStoredPart(ctx, r, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)
	_, err = cloud.AttachRemoteHolding(ctx, HolderKey{Cache: "cache-x", Number: 3}, holdingOf("holdings-only"))
	require.NoError(t, err)
	cloud.egraphMu.RLock()
	holdingsOnly := uint64(cloud.holderEntries[HolderKey{Cache: "cache-x", Number: 3}])
	cloud.egraphMu.RUnlock()
	mergeTestExpire(cloud, storedBundleTestEntry(cloud, r))

	got, err := cloud.StoredBundle(ctx, []uint64{r, 12345, holdingsOnly})
	require.NoError(t, err)
	require.Equal(t, []uint64{12345}, got.Gone)
	require.Equal(t, []uint64{holdingsOnly}, got.NoValue)
	require.Len(t, got.Bundle.Values, 2, "R and its dependency")
	numbers := map[uint64]bool{}
	for _, value := range got.Bundle.Values {
		numbers[value.SenderNumber] = true
	}
	require.Equal(t, map[uint64]bool{r: true, p: true}, numbers)
	require.Len(t, got.Bundle.Outputs, 1, "R's own part travels though R has expired")
	require.Equal(t, []uint64{r}, got.Donors)
	require.Empty(t, got.Bundle.Outputs[0].Chain.Addresses)
	require.Empty(t, got.Bundle.Outputs[0].Chain.RenewalKey)

	bctx, b, _ := transferTestCache(t)
	reply, err := b.MergeValues(bctx, cloudCacheID, got.Bundle)
	require.NoError(t, err, "the bundle merges into an engine")
	require.True(t, reply.Committed)
}

// storedBundleTestClassMember merges another record, of a different recipe,
// into the Cloud and joins it to the entry's class by a content digest. It
// returns the member's number.
func storedBundleTestClassMember(t *testing.T, ctx context.Context, cloud *Cache, entry uint64, field string) uint64 {
	t.Helper()
	bundle, _ := mergeTestSource(t, field, "member")
	reply, err := cloud.MergeValues(ctx, "cache-b", bundle)
	require.NoError(t, err)
	member := reply.Imported()[0].ResultID
	for _, number := range []uint64{entry, member} {
		require.NoError(t, cloud.TeachContentDigest(ctx, Result[Typed]{shared: storedBundleTestEntry(cloud, number)}, testDigest("bundle-class-content")))
	}
	return member
}

// A part the entry's record declares and it doesn't store is borrowed from
// an unexpired entry of its class, which is the output's storing entry; an
// expired member lends nothing.
func TestStoredBundleBorrowsFromAnUnexpiredMember(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	member := storedBundleTestClassMember(t, ctx, cloud, r, "bundle-member")
	stored, err := cloud.SetStoredPart(ctx, member, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)

	got, err := cloud.StoredBundle(ctx, []uint64{r})
	require.NoError(t, err)
	require.Len(t, got.Bundle.Outputs, 1)
	require.Equal(t, []uint64{member}, got.Donors, "the donor stores it")
	require.Empty(t, got.Bundle.Outputs[0].Owner.DependencyIDs, "no services, no owner entries")

	mergeTestExpire(cloud, storedBundleTestEntry(cloud, member))
	got, err = cloud.StoredBundle(ctx, []uint64{r})
	require.NoError(t, err)
	require.Empty(t, got.Bundle.Outputs, "an expired member lends nothing")
}

// A borrowed part's services are mapped to rows of the bundle's closure of
// the same class, and its owner lists exactly those rows; a part with a
// service the closure holds nothing of is left out.
func TestStoredBundleBorrowsThroughThePlacementRule(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	member := storedBundleTestClassMember(t, ctx, cloud, r, "bundle-member")
	// The member's part names a service: first R's dependency P, which the
	// closure holds, then an entry outside it.
	outside, _ := mergeTestSource(t, "outside-service", "service")
	reply, err := cloud.MergeValues(ctx, "cache-c", outside)
	require.NoError(t, err)
	outsideNumber := reply.Imported()[0].ResultID
	for _, tc := range []struct {
		name    string
		service uint64
		placed  bool
	}{
		{"a service the closure holds", p, true},
		{"a service outside the closure", outsideNumber, false},
	} {
		part := testLiveOffer()
		part.Value.Services = []TransferredServiceBinding{{ServiceResultID: tc.service, Hostname: "svc"}}
		part.Owner.DependencyIDs = []uint64{tc.service}
		stored, err := cloud.SetStoredPart(ctx, member, part, 0)
		require.NoError(t, err)
		require.True(t, stored)
		got, err := cloud.StoredBundle(ctx, []uint64{r})
		require.NoError(t, err, tc.name)
		if !tc.placed {
			require.Empty(t, got.Bundle.Outputs, tc.name)
			continue
		}
		require.Len(t, got.Bundle.Outputs, 1, tc.name)
		var pOrdinal TransferOrdinal
		for _, value := range got.Bundle.Values {
			if value.SenderNumber == p {
				pOrdinal = value.Ordinal
			}
		}
		output := got.Bundle.Outputs[0]
		require.Equal(t, uint64(pOrdinal), output.Value.Services[0].ServiceResultID, tc.name)
		require.Equal(t, []uint64{uint64(pOrdinal)}, output.Owner.DependencyIDs, tc.name)
	}
}

// A stored bundle racing a merge that replaces an expired stored record
// carries either the old record with its own part or the new record without
// it, never a mix: the walk and every copy happen in one hold, and the
// replacement takes the lock for writing.
func TestStoredBundleNeverMixesAReplacement(t *testing.T) {
	t.Parallel()
	for range 50 {
		ctx, cloud, r, _ := storedBundleTestCloud(t, "old")
		stored, err := cloud.SetStoredPart(ctx, r, testLiveOffer(), 0)
		require.NoError(t, err)
		require.True(t, stored)
		mergeTestExpire(cloud, storedBundleTestEntry(cloud, r))
		fresh, _ := mergeTestSource(t, "bundle-root", "new")

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cloud.MergeValues(ctx, "cache-b", fresh)
			require.NoError(t, err)
		}()
		got, err := cloud.StoredBundle(ctx, []uint64{r})
		wg.Wait()
		require.NoError(t, err)
		var rootValue TransferredValue
		for _, value := range got.Bundle.Values {
			if value.SenderNumber == r {
				rootValue = value
			}
		}
		record, err := json.Marshal(rootValue.Record.Envelope)
		require.NoError(t, err)
		if rootValue.SenderReplacements == 0 {
			require.Contains(t, string(record), "old")
			require.Len(t, got.Bundle.Outputs, 1, "the old record with its own part")
		} else {
			require.Contains(t, string(record), "new")
			require.Empty(t, got.Bundle.Outputs, "the new record, whose part isn't stored")
		}
		require.Equal(t, r, rootValue.SenderNumber, "the entry is replaced, never retired")
	}
}

// storedPartServiceTestCloud merges a second copy of R, from cache-b, into
// storedBundleTestCloud's Cloud. Its record depends on a service entry S that
// R's doesn't: R keeps its own record, and only cache-b's holding keeps S. It
// returns the numbers of R and S.
func storedPartServiceTestCloud(t *testing.T) (context.Context, *Cache, uint64, uint64) {
	t.Helper()
	ctx, cloud, r, p := storedBundleTestCloud(t, "first")
	bctx, b, bsrv := transferTestCache(t)
	br := persistedListTestResult(t, bctx, b, bsrv, "bundle-root", &transferTestValue{Text: "second"})
	bs := persistedListTestResult(t, bctx, b, bsrv, "different-service", &transferTestValue{Text: "service"})
	transferTestDependency(b, bctx, br, bs)
	bundle := exportTestBundle(t, bctx, b, br)
	reply, err := cloud.MergeValues(ctx, "cache-b", bundle)
	require.NoError(t, err)
	var service uint64
	for _, row := range reply.Values {
		for _, sent := range bundle.Values {
			if sent.Ordinal != row.Ordinal {
				continue
			}
			switch sent.SenderNumber {
			case uint64(br.cacheSharedResult().id):
				require.Equal(t, r, row.Number)
				require.Equal(t, []uint64{p}, row.Deps, "R keeps its own record")
			case uint64(bs.cacheSharedResult().id):
				service = row.Number
			}
		}
	}
	require.NotZero(t, service)
	return ctx, cloud, r, service
}

// storedPartNaming is a part whose service, and so its owner, is the entry
// with the number.
func storedPartNaming(number uint64) PersistedPartOffer {
	part := testLiveOffer()
	part.Value.Services = []TransferredServiceBinding{{ServiceResultID: number, Hostname: "db"}}
	part.Owner.DependencyIDs = []uint64{number}
	return part
}

// storedPartTestDeps returns the dependencies of the entry with the number.
func storedPartTestDeps(c *Cache, number uint64) []uint64 {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	var deps []uint64
	for id := range c.resultsByID[sharedResultID(number)].deps {
		deps = append(deps, uint64(id))
	}
	slices.Sort(deps)
	return deps
}

// Storing a part makes the entries it names the entry's dependencies, as
// installing a part does on an engine: after the engine that sent the service
// leaves, the service stays, and the entry's stored bundle builds, with the
// service among its record's dependencies.
func TestStoredPartKeepsTheEntriesItNames(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, service := storedPartServiceTestCloud(t)
	stored, err := cloud.SetStoredPart(ctx, r, storedPartNaming(service), 0)
	require.NoError(t, err)
	require.True(t, stored)

	_, err = cloud.CollectRemoteHoldings(ctx, cloud.ReleaseRemoteCache(ctx, "cache-b"))
	require.NoError(t, err)
	require.NotNil(t, storedBundleTestEntry(cloud, service), "the service outlives its sender")
	got, err := cloud.StoredBundle(ctx, []uint64{r})
	require.NoError(t, err)
	ordinals := map[uint64]TransferOrdinal{}
	for _, value := range got.Bundle.Values {
		ordinals[value.SenderNumber] = value.Ordinal
	}
	for _, value := range got.Bundle.Values {
		if value.SenderNumber == r {
			require.Contains(t, value.DependencyIDs, uint64(ordinals[service]), "the service is among R's dependencies")
		}
	}
	require.Len(t, got.Bundle.Outputs, 1)
}

// The dependencies a stored part adds belong to the value: replacing it in
// place releases them, and the service, which nothing else owns, is
// collected.
func TestStoredPartDependenciesGoWithTheValue(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, service := storedPartServiceTestCloud(t)
	stored, err := cloud.SetStoredPart(ctx, r, storedPartNaming(service), 0)
	require.NoError(t, err)
	require.True(t, stored)
	_, err = cloud.CollectRemoteHoldings(ctx, cloud.ReleaseRemoteCache(ctx, "cache-b"))
	require.NoError(t, err)
	require.NotNil(t, storedBundleTestEntry(cloud, service))

	mergeTestExpire(cloud, storedBundleTestEntry(cloud, r))
	fresh, _ := mergeTestSource(t, "bundle-root", "new")
	_, err = cloud.MergeValues(ctx, "cache-c", fresh)
	require.NoError(t, err)
	cloud.egraphMu.RLock()
	replacements := storedBundleTestEntry(cloud, r).replacements
	cloud.egraphMu.RUnlock()
	require.Equal(t, uint64(1), replacements, "replaced in place")
	require.NotContains(t, storedPartTestDeps(cloud, r), service)
	require.Nil(t, storedBundleTestEntry(cloud, service), "the service is collected")
}

// A part that names an entry the cache doesn't have, through its owner or a
// service, or one that reaches the storing entry, which would close a cycle,
// is not stored and adds no dependency.
func TestSetStoredPartRefusesWhatItCannotDependOn(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	ownerOnly := testLiveOffer()
	ownerOnly.Owner.DependencyIDs = []uint64{12345}
	serviceOnly := testLiveOffer()
	serviceOnly.Value.Services = []TransferredServiceBinding{{ServiceResultID: 12345, Hostname: "db"}}
	for _, tc := range []struct {
		name  string
		entry uint64
		part  PersistedPartOffer
	}{
		{"an owner entry the cache doesn't have", r, ownerOnly},
		{"a service the cache doesn't have", r, serviceOnly},
		{"the entry's own dependent", p, storedPartNaming(r)},
		{"the entry itself", r, storedPartNaming(r)},
	} {
		deps := storedPartTestDeps(cloud, tc.entry)
		stored, err := cloud.SetStoredPart(ctx, tc.entry, tc.part, 0)
		require.NoError(t, err, tc.name)
		require.False(t, stored, tc.name)
		cloud.egraphMu.RLock()
		parts := storedBundleTestEntry(cloud, tc.entry).storedParts
		cloud.egraphMu.RUnlock()
		require.Empty(t, parts, tc.name)
		require.Equal(t, deps, storedPartTestDeps(cloud, tc.entry), tc.name)
	}
}
