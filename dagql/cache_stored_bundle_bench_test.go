package dagql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// storedBenchValue is the stored bundle benchmarks' value. Its transfer codec
// does the work core's foreign codecs do (core/value_transfer.go): normalizing
// a payload decodes it strictly, encodes it again and validates the result,
// and validating or mapping a payload decodes it strictly.
type storedBenchValue struct {
	Text   string              `json:"text"`
	Child  uint64              `json:"child,omitempty"`
	Fields []transferTestField `json:"fields,omitempty"`
}

func (*storedBenchValue) Type() *ast.Type {
	return &ast.Type{NamedType: "StoredBenchValue", NonNull: true}
}
func (v *storedBenchValue) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	raw, err := json.Marshal(v)
	return PersistedObjectEncoding{JSON: raw}, err
}
func (*storedBenchValue) DecodePersistedObject(_ context.Context, _ *PersistDecodeContext, raw json.RawMessage) (Typed, error) {
	value := new(storedBenchValue)
	return value, json.Unmarshal(raw, value)
}

type storedBenchCodec struct{}

// readStoredBenchPayload decodes raw as core's readForeignPayload does.
func readStoredBenchPayload(raw json.RawMessage) (*storedBenchValue, error) {
	value := new(storedBenchValue)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return nil, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("trailing payload data")
	}
	return value, nil
}
func (storedBenchCodec) VisitPersistedReferences(v PersistedPayloadVisit, visit PersistedRefVisitor) (json.RawMessage, error) {
	if err := VisitPersistedSnapshotRoles(visit, PersistedRefOutputRole, v.Path, v.SnapshotLinks); err != nil {
		return nil, err
	}
	value := new(storedBenchValue)
	if err := json.Unmarshal(v.Payload, value); err != nil {
		return nil, err
	}
	if _, err := VisitPersistedRow(visit, PersistedRefChild, v.Path.Field("child"), &value.Child); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func (codec storedBenchCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	value, err := readStoredBenchPayload(v.Payload)
	if err != nil {
		return ForeignPayload{}, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ForeignPayload{}, err
	}
	normalized := v
	normalized.Payload, normalized.SnapshotLinks = raw, nil
	if err := codec.ValidateForeign(normalized); err != nil {
		return ForeignPayload{}, err
	}
	return ForeignPayload{JSON: raw}, nil
}
func (codec storedBenchCodec) ValidateForeign(v PersistedPayloadVisit) error {
	if len(v.SnapshotLinks) != 0 {
		return fmt.Errorf("foreign payload carries local storage links")
	}
	if _, err := readStoredBenchPayload(v.Payload); err != nil {
		return err
	}
	_, err := codec.VisitPersistedReferences(v, func(*PersistedRef) error { return nil })
	return err
}
func (storedBenchCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	if _, err := readStoredBenchPayload(v.Payload); err != nil {
		return nil, err
	}
	return []CapturedCodecOutput{{Address: PersistedPartAddress{OutputPath: v.Path, Part: "snapshot"}, State: "pending", Role: "snapshot", Value: &SnapshotValue{Kind: "directory"}}}, nil
}
func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.StoredBench", Typed: (*storedBenchValue)(nil), Visitor: storedBenchCodec{}, Transfer: storedBenchCodec{}})
}

// The stored bundle benchmarks' pool has the shape of the remote cache
// service's merge bundles under load, which StoredBundle built: one batch of
// 64 roots, the service's first take, whose closures share most of their
// records. Every root sits on 3 records of its own over a shared chain of
// 1,024 records, as a module's calls sit on its runtime, so the batch's
// closure has 1,280 records. Each record is about 4 KB, the mean of the
// service's export replies under load, and names its dependency in its
// payload. Every root and every fourth record of the chain stores a part of 8
// layers, each with annotations and an address.
const (
	storedBenchRoots  = 64
	storedBenchOwn    = 3
	storedBenchChain  = 1_024
	storedBenchFields = 16
	storedBenchLayers = 8
)

// storedBenchPart is a stored part whose chain has storedBenchLayers layers,
// distinct for seed, as an engine offers a container's root filesystem.
func storedBenchPart(seed uint64) PersistedPartOffer {
	created := time.Unix(1_760_000_000, 0).UTC()
	part := PersistedPartOffer{
		Address: PersistedPartAddress{Part: "snapshot"},
		Value:   SnapshotValue{Kind: "directory", Path: "/"},
		Chain:   OfferedChain{Addresses: map[digest.Digest]BlobAddress{}, RenewalKey: fmt.Sprintf("renewal-%d", seed)},
	}
	for i := range storedBenchLayers {
		blob := digest.FromString(fmt.Sprintf("stored-bench-layer-%d-%d", seed, i))
		part.Chain.Layers = append(part.Chain.Layers, snapshots.ExportLayer{
			Descriptor: ocispecs.Descriptor{
				MediaType: ocispecs.MediaTypeImageLayerGzip,
				Digest:    blob,
				Size:      int64(1+i) << 20,
				Annotations: map[string]string{
					"containerd.io/uncompressed":        digest.FromString(fmt.Sprintf("uncompressed-%d-%d", seed, i)).String(),
					"buildkit/createdat":                created.Format(time.RFC3339Nano),
					"containerd.io/distribution.source": "registry.example/library/base",
				},
			},
			Description: fmt.Sprintf("layer %d of a container's root filesystem", i),
			CreatedAt:   &created,
		})
		part.Chain.Addresses[blob] = BlobAddress{URL: "https://blobs.example/sha256/" + blob.Encoded() + "?signature=0123456789abcdef", ExpiresAtUnix: created.Add(time.Hour).Unix()}
	}
	return part
}

// storedBenchCloud returns a blob-backed cache holding the pool, and the
// batch's roots by number. The pool is exported from an engine's cache and
// merged, as the service receives it.
func storedBenchCloud(b *testing.B) (context.Context, *Cache, []uint64) {
	b.Helper()
	actx := cacheTestContext(context.Background())
	a, err := NewCache(actx, filepath.Join(b.TempDir(), "a.db"), nil, nil)
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, a.CloseDiscardingPersistence()) })
	srv := newDagqlServerForTest(b, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*storedBenchValue]{}))
	actx = srvToContext(ContextWithCache(actx, a), srv)
	stored := map[uint64]bool{}
	publish := func(field string, child AnyResult, store bool) AnyResult {
		value := &storedBenchValue{Text: field, Fields: mergeBenchBody(field, storedBenchFields)}
		if child != nil {
			value.Child = uint64(child.cacheSharedResult().id)
		}
		frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(value.Type())}
		res, err := a.GetOrInitCall(actx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(value, frame)
		})
		require.NoError(b, err)
		if child != nil {
			transferTestDependency(a, actx, res, child)
		}
		stored[uint64(res.cacheSharedResult().id)] = store
		return res
	}
	var top AnyResult
	for i := range storedBenchChain {
		top = publish(fmt.Sprintf("chain-%d", i), top, i%4 == 0)
	}
	var roots []AnyResult
	for i := range storedBenchRoots {
		prev := top
		for j := range storedBenchOwn {
			prev = publish(fmt.Sprintf("own-%d-%d", i, j), prev, false)
		}
		roots = append(roots, publish(fmt.Sprintf("root-%d", i), prev, true))
	}
	var bundle ValueBundle
	require.NoError(b, a.WithExportedValues(actx, ValueSelection{Roots: roots}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))
	require.Len(b, bundle.Values, storedBenchChain+storedBenchRoots*(storedBenchOwn+1))

	ctx := cacheTestContext(context.Background())
	cloud, err := NewCache(ctx, "", nil, nil, WithBlobStore())
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, cloud.CloseDiscardingPersistence()) })
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(b, err)
	require.True(b, reply.Committed)
	numbers := map[TransferOrdinal]uint64{}
	for _, value := range reply.Values {
		numbers[value.Ordinal] = value.Number
	}
	for _, value := range bundle.Values {
		if stored[value.SenderNumber] {
			ok, err := cloud.SetStoredPart(ctx, numbers[value.Ordinal], storedBenchPart(value.SenderNumber), 0)
			require.NoError(b, err)
			require.True(b, ok)
		}
	}
	var batch []uint64
	for _, root := range bundle.Roots {
		batch = append(batch, numbers[root.Ordinal])
	}
	return ctx, cloud, batch
}

// storedBenchClosure walks the batch's closure as StoredBundle does, and
// returns it in dependency order with a build that has its ordinals.
func storedBenchClosure(b *testing.B, c *Cache, batch []uint64) ([]*sharedResult, *storedBundleBuild) {
	b.Helper()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	build := &storedBundleBuild{now: time.Now().Unix(), ordinals: map[sharedResultID]TransferOrdinal{}}
	visiting := map[sharedResultID]bool{}
	for _, number := range batch {
		require.NoError(b, c.storedBundleVisitLocked(build, c.resultsByID[sharedResultID(number)], visiting))
	}
	require.Len(b, build.order, storedBenchChain+storedBenchRoots*(storedBenchOwn+1))
	return build.order, build
}

// storedBenchSettle collects garbage outside a timed section, so that no
// section pays for another's allocations.
func storedBenchSettle(b *testing.B) {
	b.StopTimer()
	runtime.GC()
	b.StartTimer()
}

// BenchmarkStoredBundle builds the batch's bundle from the pool, as the
// service does for each merge it sends. It reports the build's time and the
// bundle's encoded size; the benchmarks below measure its parts. It also
// reports what a first build leaves on the heap against the closure's payload
// bytes: what the entries keep between builds.
func BenchmarkStoredBundle(b *testing.B) {
	ctx, cloud, batch := storedBenchCloud(b)
	order, _ := storedBenchClosure(b, cloud, batch)
	var payload int64
	for _, res := range order {
		payload += persistedEnvelopePayloadBytes(res.persistedEnvelope)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := cloud.StoredBundle(ctx, batch)
	require.NoError(b, err)
	runtime.GC()
	runtime.ReadMemStats(&after)
	kept := int64(after.HeapAlloc) - int64(before.HeapAlloc)

	var got StoredBundleResult
	b.ResetTimer()
	for range b.N {
		storedBenchSettle(b)
		var err error
		got, err = cloud.StoredBundle(ctx, batch)
		require.NoError(b, err)
	}
	b.StopTimer()
	require.Len(b, got.Bundle.Values, storedBenchChain+storedBenchRoots*(storedBenchOwn+1))
	require.Len(b, got.Bundle.Outputs, storedBenchChain/4+storedBenchRoots)
	encoded, err := json.Marshal(got.Bundle)
	require.NoError(b, err)
	b.ReportMetric(float64(len(got.Bundle.Values)), "records")
	b.ReportMetric(float64(len(encoded)), "bundle-bytes")
	b.ReportMetric(float64(payload), "payload-bytes")
	b.ReportMetric(float64(kept), "kept-bytes")
	b.ReportMetric(float64(kept)/float64(payload), "kept/payload")
}

// BenchmarkStoredBundleValues measures StoredBundle's copy of the closure's
// records, normalized for transfer and relocated to ordinals, against the
// relocation alone of records normalized beforehand. Normalizing depends only
// on the stored record and its call, so a build need not repeat it for a
// record that hasn't changed (in a profile of the remote cache service under
// load: 3.53 s of StoredBundle's 11.70 s, beside relocation's 1.60 s). The
// service builds the same records into bundle after bundle, so the benchmark
// measures the builds after a first one, whose cost it reports apart.
//
// Target: the copy costs at most 1.5 times the relocation.
func BenchmarkStoredBundleValues(b *testing.B) {
	_, cloud, batch := storedBenchCloud(b)
	order, build := storedBenchClosure(b, cloud, batch)
	normalized := make([]PersistedRecord, len(order))
	for i, res := range order {
		rec, err := normalizeTransferRecord(PersistedRecord{ResultID: uint64(res.id), Envelope: *res.persistedEnvelope, Call: res.loadResultCall()})
		require.NoError(b, err)
		rec.Envelope.PendingOffers = nil
		normalized[i] = rec
	}
	copyValues := func() {
		cloud.egraphMu.RLock()
		defer cloud.egraphMu.RUnlock()
		build.bundle.Values = nil
		for _, res := range order {
			if err := cloud.storedBundleValueLocked(build, res); err != nil {
				b.Fatal(err)
			}
		}
	}
	start := time.Now()
	copyValues()
	first := time.Since(start)
	var copying, relocating time.Duration
	b.ResetTimer()
	for range b.N {
		storedBenchSettle(b)
		start := time.Now()
		copyValues()
		copying += time.Since(start)
		storedBenchSettle(b)
		start = time.Now()
		cloud.egraphMu.RLock()
		for _, rec := range normalized {
			if _, err := VisitEncodedReferences(rec, build.relocate); err != nil {
				b.Fatal(err)
			}
		}
		cloud.egraphMu.RUnlock()
		relocating += time.Since(start)
	}
	b.StopTimer()
	require.Len(b, build.bundle.Values, len(order))
	perRecord := func(d time.Duration, n int) float64 { return float64(d.Nanoseconds()) / 1000 / float64(n*len(order)) }
	ratio := copying.Seconds() / relocating.Seconds()
	b.ReportMetric(perRecord(first, 1), "first-copy-us/record")
	b.ReportMetric(perRecord(copying, b.N), "copy-us/record")
	b.ReportMetric(perRecord(relocating, b.N), "relocation-us/record")
	b.ReportMetric(ratio, "copy/relocation")
	if ratio > 1.5 {
		b.Errorf("copying a record costs %.2f times relocating it (%.1f µs against %.1f µs a record, %.1f µs in a first build); the target is at most 1.5", ratio, perRecord(copying, b.N), perRecord(relocating, b.N), perRecord(first, 1))
	}
}

// BenchmarkStoredBundlePartCopies measures StoredBundle's deep copies of the
// closure's stored parts, which relocation and the removal of addresses and
// renewal keys then change, against copying the same parts by a JSON round
// trip, the way clonePartOffers copied them before (in a profile of the
// remote cache service under load: 1.75 s of StoredBundle's 11.70 s).
//
// Target: copying a part costs at most an eighth of the round trip.
func BenchmarkStoredBundlePartCopies(b *testing.B) {
	_, cloud, batch := storedBenchCloud(b)
	order, _ := storedBenchClosure(b, cloud, batch)
	var parts []PersistedPartOffer
	cloud.egraphMu.RLock()
	for _, res := range order {
		for _, part := range res.storedParts {
			parts = append(parts, part)
		}
	}
	cloud.egraphMu.RUnlock()
	require.Len(b, parts, storedBenchChain/4+storedBenchRoots)
	var copying, roundTrips time.Duration
	b.ResetTimer()
	for range b.N {
		storedBenchSettle(b)
		start := time.Now()
		for _, part := range parts {
			if _, err := clonePartOffers([]PersistedPartOffer{part}); err != nil {
				b.Fatal(err)
			}
		}
		copying += time.Since(start)
		storedBenchSettle(b)
		start = time.Now()
		for _, part := range parts {
			data, err := json.Marshal([]PersistedPartOffer{part})
			if err != nil {
				b.Fatal(err)
			}
			var copied []PersistedPartOffer
			if err := json.Unmarshal(data, &copied); err != nil {
				b.Fatal(err)
			}
		}
		roundTrips += time.Since(start)
	}
	b.StopTimer()
	perPart := func(d time.Duration) float64 { return float64(d.Nanoseconds()) / float64(b.N*len(parts)) }
	ratio := copying.Seconds() / roundTrips.Seconds()
	b.ReportMetric(perPart(copying), "copy-ns/part")
	b.ReportMetric(perPart(roundTrips), "round-trip-ns/part")
	b.ReportMetric(ratio, "copy/round-trip")
	if ratio > 0.125 {
		b.Errorf("copying a part costs %.3f times a JSON round trip of it (%.0f ns against %.0f ns a part); the target is at most 0.125", ratio, perPart(copying), perPart(roundTrips))
	}
}

// BenchmarkStoredBundleValidation measures the validation of the batch's
// finished bundle, which decodes every record's payload again, against
// encoding the bundle once, as sending it does (in a profile of the remote
// cache service under load: 3.81 s of StoredBundle's 11.70 s). It also
// reports the shape-only check, which keeps
// the bundle's assembly checks and decodes only the payloads of records with
// offers or outputs.
//
// Target: validating the bundle costs at most 1.5 times the encoding. The
// remote cache service builds bundles under its work lock, within a step, so
// a batch's build should fit a slice of a step like the service's 250 ms
// reconcile budget. The full validation, at 4 to 8 times the encoding, is the
// largest part of a build and keeps it from fitting.
func BenchmarkStoredBundleValidation(b *testing.B) {
	ctx, cloud, batch := storedBenchCloud(b)
	got, err := cloud.StoredBundle(ctx, batch)
	require.NoError(b, err)
	bundle := got.Bundle
	var validating, shape, encoding time.Duration
	b.ResetTimer()
	for range b.N {
		storedBenchSettle(b)
		start := time.Now()
		if _, err := validateValueBundle(bundle); err != nil {
			b.Fatal(err)
		}
		validating += time.Since(start)
		storedBenchSettle(b)
		start = time.Now()
		if _, err := validateValueBundleShape(bundle); err != nil {
			b.Fatal(err)
		}
		shape += time.Since(start)
		storedBenchSettle(b)
		start = time.Now()
		if _, err := json.Marshal(bundle); err != nil {
			b.Fatal(err)
		}
		encoding += time.Since(start)
	}
	b.StopTimer()
	perBuild := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 / float64(b.N) }
	ratio := validating.Seconds() / encoding.Seconds()
	b.ReportMetric(perBuild(validating), "validate-ms")
	b.ReportMetric(perBuild(shape), "shape-ms")
	b.ReportMetric(perBuild(encoding), "encode-ms")
	b.ReportMetric(ratio, "validate/encode")
	if ratio > 1.5 {
		b.Errorf("validating the bundle costs %.2f times encoding it (%.1f ms against %.1f ms, shape only %.1f ms); the target is at most 1.5", ratio, perBuild(validating), perBuild(encoding), perBuild(shape))
	}
}
