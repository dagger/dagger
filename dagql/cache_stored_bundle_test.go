package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
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

// A stored bundle carries an entry's current record and call: a build after a
// content digest changes the call, or after a merge replaces the record,
// carries the new one, though an earlier build normalized the old.
func TestStoredBundleCarriesTheCurrentRecord(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "old")
	rootRecord := func() PersistedRecord {
		t.Helper()
		got, err := cloud.StoredBundle(ctx, []uint64{r})
		require.NoError(t, err)
		for _, value := range got.Bundle.Values {
			if value.SenderNumber == r {
				return value.Record
			}
		}
		t.Fatal("the bundle has no record of R")
		return PersistedRecord{}
	}
	require.Contains(t, string(rootRecord().Envelope.ObjectJSON), "old")

	content := testDigest("current-record-content")
	require.NoError(t, cloud.TeachContentDigest(ctx, Result[Typed]{shared: storedBundleTestEntry(cloud, r)}, content))
	require.Contains(t, rootRecord().Call.ExtraDigests, call.ExtraDigest{Digest: content, Label: call.ExtraDigestLabelContent}, "the new call")

	mergeTestExpire(cloud, storedBundleTestEntry(cloud, r))
	fresh, _ := mergeTestSource(t, "bundle-root", "new")
	_, err := cloud.MergeValues(ctx, "cache-b", fresh)
	require.NoError(t, err)
	require.False(t, storedBundleTestKept(cloud, r), "the replacement releases the old record")
	require.Contains(t, string(rootRecord().Envelope.ObjectJSON), "new", "the new record")
}

// storedBundleTestKept reports whether the entry with the number keeps a
// normalized record.
func storedBundleTestKept(c *Cache, number uint64) bool {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	return c.resultsByID[sharedResultID(number)].storedTransfer.Load() != nil
}

// A prune that drops a value releases the normalized record a build kept for
// it, and the record the entry later takes again is the one a build carries.
func TestStoredBundleRecordGoesWithThePrunedValue(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	_, err := cloud.StoredBundle(ctx, []uint64{r})
	require.NoError(t, err)
	require.True(t, storedBundleTestKept(cloud, r))
	require.True(t, storedBundleTestKept(cloud, p))

	_, removed, dropped, err := cloud.removePrunedEdge(ctx, sharedResultID(r))
	require.NoError(t, err)
	require.True(t, removed)
	require.Equal(t, 2, dropped)
	require.False(t, storedBundleTestKept(cloud, r), "released with R's value")
	require.False(t, storedBundleTestKept(cloud, p), "released with P's value")
	got, err := cloud.StoredBundle(ctx, []uint64{r})
	require.NoError(t, err)
	require.Equal(t, []uint64{r}, got.NoValue)

	actx, a, asrv := transferTestCache(t)
	root := persistedListTestResult(t, actx, a, asrv, "bundle-root", &transferTestValue{Text: "again"})
	dep := persistedListTestResult(t, actx, a, asrv, "bundle-dep", &transferTestValue{Text: "dep"})
	transferTestDependency(a, actx, root, dep)
	reply, err := cloud.MergeValues(ctx, "cache-b", exportTestBundle(t, actx, a, root))
	require.NoError(t, err)
	require.Equal(t, r, reply.Imported()[0].ResultID, "the same entry")
	got, err = cloud.StoredBundle(ctx, []uint64{r})
	require.NoError(t, err)
	for _, value := range got.Bundle.Values {
		if value.SenderNumber == r {
			require.Contains(t, string(value.Record.Envelope.ObjectJSON), "again")
		}
	}
}

// A restored Cloud builds the bundle it built before it was saved: a restore
// starts with no normalized record and normalizes the restored ones.
func TestStoredBundleAfterARestore(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cloud.db")
	ctx, cloud := storedPersistenceTestCloud(t, path)
	actx, a, asrv := transferTestCache(t)
	r := persistedListTestResult(t, actx, a, asrv, "restored-root", &transferTestValue{Text: "root"})
	p := persistedListTestResult(t, actx, a, asrv, "restored-dep", &transferTestValue{Text: "dep"})
	transferTestDependency(a, actx, r, p)
	root := storedPersistenceTestMerge(t, ctx, cloud, exportTestBundle(t, actx, a, r))[0]
	stored, err := cloud.SetStoredPart(ctx, root, storedBenchPart(1), 0)
	require.NoError(t, err)
	require.True(t, stored)
	before, err := cloud.StoredBundle(ctx, []uint64{root})
	require.NoError(t, err)
	want, err := json.Marshal(before)
	require.NoError(t, err)
	require.NoError(t, cloud.Close(ctx))

	ctx, cloud = storedPersistenceTestCloud(t, path)
	defer func() { require.NoError(t, cloud.Close(ctx)) }()
	require.False(t, storedBundleTestKept(cloud, root))
	after, err := cloud.StoredBundle(ctx, []uint64{root})
	require.NoError(t, err)
	got, err := json.Marshal(after)
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got))
}

// A stored record that names an entry of its closure that isn't one of its
// dependencies is refused, though relocation finds that entry an ordinal: a
// build checks every reference against the record's dependencies.
func TestStoredBundleRefusesAReferenceToAnIndirectEntry(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	root := persistedListTestResult(t, actx, a, asrv, "indirect-root", &transferTestValue{Text: "root"})
	mid := persistedListTestResult(t, actx, a, asrv, "indirect-mid", &transferTestValue{Text: "mid"})
	leaf := persistedListTestResult(t, actx, a, asrv, "indirect-leaf", &transferTestValue{Text: "leaf"})
	transferTestDependency(a, actx, root, mid)
	transferTestDependency(a, actx, mid, leaf)
	bundle := exportTestBundle(t, actx, a, root)
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	numbers := map[TransferOrdinal]uint64{}
	for _, value := range reply.Values {
		numbers[value.Ordinal] = value.Number
	}
	var r, leafNumber uint64
	for _, value := range bundle.Values {
		switch value.SenderNumber {
		case uint64(root.cacheSharedResult().id):
			r = numbers[value.Ordinal]
		case uint64(leaf.cacheSharedResult().id):
			leafNumber = numbers[value.Ordinal]
		}
	}
	require.NotZero(t, r)
	require.NotZero(t, leafNumber)
	_, err = cloud.StoredBundle(ctx, []uint64{r})
	require.NoError(t, err)

	// R's record now names the leaf, which only its dependency depends on.
	payload, err := json.Marshal(&transferTestValue{Text: "root", Child: leafNumber})
	require.NoError(t, err)
	cloud.egraphMu.Lock()
	res := cloud.resultsByID[sharedResultID(r)]
	env := *res.persistedEnvelope
	env.ObjectJSON = payload
	res.persistedEnvelope = &env
	cloud.egraphMu.Unlock()
	_, err = cloud.StoredBundle(ctx, []uint64{r})
	require.ErrorContains(t, err, "is not a direct dependency")
}

// storedCheckValue's family has a codec that normalizes a payload without
// validating it, as a family's codec may: only its validation refuses a
// payload whose text is "invalid", only its output mapping one whose text is
// "unmapped", and it maps one whose text is "unaddressed" to an output with
// no part. The cache never decodes its records.
type storedCheckValue struct{}

func (*storedCheckValue) Type() *ast.Type {
	return &ast.Type{NamedType: "StoredCheckValue", NonNull: true}
}

type storedCheckCodec struct{}

func (storedCheckCodec) VisitPersistedReferences(v PersistedPayloadVisit, _ PersistedRefVisitor) (json.RawMessage, error) {
	return v.Payload, nil
}
func (storedCheckCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	return ForeignPayload{JSON: slices.Clone(v.Payload)}, nil
}
func (storedCheckCodec) ValidateForeign(v PersistedPayloadVisit) error {
	if strings.Contains(string(v.Payload), `"invalid"`) {
		return fmt.Errorf("invalid payload")
	}
	return nil
}
func (storedCheckCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	if strings.Contains(string(v.Payload), `"unmapped"`) {
		return nil, fmt.Errorf("unmapped payload")
	}
	if strings.Contains(string(v.Payload), `"unaddressed"`) {
		return []CapturedCodecOutput{{Address: PersistedPartAddress{OutputPath: v.Path}, State: "pending"}}, nil
	}
	return nil, nil
}
func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.StoredCheck", Typed: (*storedCheckValue)(nil), Visitor: storedCheckCodec{}, Transfer: storedCheckCodec{}})
}

// A stored record whose payload its codec refuses, can't map to its outputs,
// or maps to an output with no part, is refused, though the codec normalizes
// it: a build validates and maps each record's payloads when it normalizes
// the record.
func TestStoredBundleRefusesAPayloadItsCodecRefuses(t *testing.T) {
	t.Parallel()
	for text, refusal := range map[string]string{"invalid": "invalid payload", "unmapped": "unmapped payload", "unaddressed": "empty part"} {
		ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
		_, err := cloud.StoredBundle(ctx, []uint64{r})
		require.NoError(t, err, text)
		cloud.egraphMu.Lock()
		res := cloud.resultsByID[sharedResultID(r)]
		env := *res.persistedEnvelope
		env.ObjectCodec = "dagql_test.StoredCheck"
		env.ObjectJSON = json.RawMessage(`{"text":"` + text + `"}`)
		res.persistedEnvelope = &env
		cloud.egraphMu.Unlock()
		_, err = cloud.StoredBundle(ctx, []uint64{r})
		require.ErrorContains(t, err, refusal, text)
	}
}

// A payload its codec validated stays valid once its family's visitor
// relocates its references, which validateStoredBundle relies on: a stored
// bundle's records are validated before they are relocated. Every family the
// dagql tests register with a transfer codec has a sample, and the samples
// with references have them moved.
func TestRelocatedTransferPayloadsStayValid(t *testing.T) {
	samples := map[string]struct {
		payload string
		refs    int
	}{
		"dagql_test.Transfer":      {`{"text":"relocated","child":5}`, 1},
		"dagql_test.StoredBench":   {`{"text":"relocated","child":5,"fields":[{"name":"f","description":"d","type":"String!"}]}`, 1},
		"dagql_test.InlineCapture": {`{"text":"relocated"}`, 0},
		"dagql_test.StatePart":     {`{}`, 0},
		"dagql_test.StateTwoPart":  {`{}`, 0},
		"dagql_test.Share":         {`{"name":"relocated"}`, 0},
		"dagql_test.FixtureDemand": {`{"name":"relocated"}`, 0},
		"dagql_test.StoredCheck":   {`{"text":"relocated"}`, 0},
	}
	for _, family := range PersistedObjectFamilies() {
		// core's families, which this package's external tests register,
		// are core's test's to cover.
		if family.Transfer == nil || !strings.HasPrefix(family.Name, "dagql_test.") {
			continue
		}
		sample, ok := samples[family.Name]
		require.True(t, ok, "family %s has no sample", family.Name)
		v := PersistedPayloadVisit{Version: persistedResultEnvelopeVersion, Payload: json.RawMessage(sample.payload)}
		foreign, err := family.Transfer.NormalizeForeign(v)
		require.NoError(t, err, family.Name)
		v.Payload = foreign.JSON
		require.NoError(t, family.Transfer.ValidateForeign(v), family.Name)
		moved := 0
		relocated, err := family.Visitor.VisitPersistedReferences(v, func(ref *PersistedRef) error {
			if ref.RecipeID == nil && ref.ResultID != 0 {
				ref.ResultID += 1000
				moved++
			}
			return nil
		})
		require.NoError(t, err, family.Name)
		require.Equal(t, sample.refs, moved, family.Name)
		if moved > 0 {
			require.NotEqual(t, string(v.Payload), string(relocated), family.Name)
		}
		v.Payload = relocated
		require.NoError(t, family.Transfer.ValidateForeign(v), "%s, relocated", family.Name)
	}
}

// A stored bundle is its caller's copy: changing its records and outputs in
// place reaches neither the stored parts it was built from, which keep their
// addresses and renewal keys, nor the next bundle built from the same entries.
// The root depends on a scalar, whose payload relocation passes through.
func TestStoredBundleIsTheCallersCopy(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	root := persistedListTestResult(t, actx, a, asrv, "copied-root", &transferTestValue{Text: "root"})
	transferTestDependency(a, actx, root, persistedListTestResult(t, actx, a, asrv, "copied-scalar", String("scalar")))
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(ctx, "cache-a", exportTestBundle(t, actx, a, root))
	require.NoError(t, err)
	r := reply.Imported()[0].ResultID
	part := storedBenchPart(1)
	stored, err := cloud.SetStoredPart(ctx, r, part, 0)
	require.NoError(t, err)
	require.True(t, stored)
	first, err := cloud.StoredBundle(ctx, []uint64{r})
	require.NoError(t, err)
	require.Len(t, first.Bundle.Outputs, 1)
	want, err := json.Marshal(first.Bundle)
	require.NoError(t, err)

	scribbleTestValue(reflect.ValueOf(&first.Bundle).Elem(), map[uintptr]bool{})
	second, err := cloud.StoredBundle(ctx, []uint64{r})
	require.NoError(t, err)
	got, err := json.Marshal(second.Bundle)
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got))
	cloud.egraphMu.RLock()
	kept := storedBundleTestEntry(cloud, r).storedParts
	cloud.egraphMu.RUnlock()
	key, err := partAddressKey(part.Address)
	require.NoError(t, err)
	require.Equal(t, map[string]PersistedPartOffer{key: part}, kept)
}

// scribbleTestValue overwrites, in place, every exported field v reaches: the
// elements of its slices, the values of its maps and the targets of its
// pointers.
func scribbleTestValue(v reflect.Value, seen map[uintptr]bool) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		scribbleTestValue(v.Elem(), seen)
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				scribbleTestValue(v.Field(i), seen)
			}
		}
	case reflect.Slice:
		for i := range v.Len() {
			scribbleTestValue(v.Index(i), seen)
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			elem := reflect.New(v.Type().Elem()).Elem()
			elem.Set(v.MapIndex(key))
			scribbleTestValue(elem, seen)
			v.SetMapIndex(key, elem)
		}
	case reflect.String:
		v.SetString("scribbled")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(v.Uint() + 1)
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
