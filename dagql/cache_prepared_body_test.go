package dagql

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// readPayloadValue's family records the payload each codec read was shown,
// for a test of what a payload read copies.
type readPayloadValue struct{}

func (readPayloadValue) Type() *ast.Type { return nil }

type readPayloadCodec struct{ seen *readPayloadSeen }

type readPayloadSeen struct {
	mu   sync.Mutex
	data []*byte
}

func (s *readPayloadSeen) note(payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = append(s.data, unsafe.SliceData(payload))
}

var readPayloadSeenBy readPayloadSeen

func (c readPayloadCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	return ForeignPayload{JSON: v.Payload}, nil
}

func (c readPayloadCodec) ValidateForeign(v PersistedPayloadVisit) error {
	c.seen.note(v.Payload)
	return nil
}

func (c readPayloadCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	c.seen.note(v.Payload)
	return nil, nil
}

func (c readPayloadCodec) DescribeParts(v PersistedPayloadVisit) ([]PartProbe, error) {
	c.seen.note(v.Payload)
	return nil, nil
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{
		Name: "dagql_test.ReadPayload", Typed: readPayloadValue{},
		Visitor: PersistedNoReferences{}, Transfer: readPayloadCodec{seen: &readPayloadSeenBy},
	})
}

// TestAPayloadReadIsShownTheRecordsOwnBytes checks that the walks a merge's
// preparation reads a record's payloads with, its validation, its part
// mapping and its part description, show each codec the record's own
// payload, not a copy: a read of a record copies none of its payloads.
func TestAPayloadReadIsShownTheRecordsOwnBytes(t *testing.T) {
	t.Parallel()
	payload := json.RawMessage(`{"text":"` + strings.Repeat("x", 1<<16) + `"}`)
	item := PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindObject, TypeName: "ReadPayload", ObjectCodec: "dagql_test.ReadPayload", ObjectJSON: payload}
	rec := PersistedRecord{ResultID: 1, Envelope: PersistedResultEnvelope{Version: persistedResultEnvelopeVersion, Kind: persistedResultKindList, Items: []PersistedResultEnvelope{item}}}
	before := len(readPayloadSeenBy.data)
	require.NoError(t, validateMemoTransferPayloads(rec, payloadMemos{}))
	_, err := mapMemoTransferredOutputs(rec, payloadMemos{})
	require.NoError(t, err)
	_, err = describeMemoPartRecord(rec, payloadMemos{})
	require.NoError(t, err)
	readPayloadSeenBy.mu.Lock()
	defer readPayloadSeenBy.mu.Unlock()
	require.GreaterOrEqual(t, len(readPayloadSeenBy.data)-before, 3, "each of the three reads is shown the payload")
	own := 0
	for _, data := range readPayloadSeenBy.data[before:] {
		if data == unsafe.SliceData(payload) {
			own++
		}
	}
	require.Equal(t, 3, own, "each of the three reads is shown the record's own payload, not a copy")
	require.Equal(t, unsafe.SliceData(payload), unsafe.SliceData(rec.Envelope.Items[0].ObjectJSON), "the record keeps its payload")
}

// TestCloudHoldsAMergedRecordCompressedFromItsCommit checks that a record a
// Cloud merge installs is held compressed from its commit on, before the
// body worker runs: in the body its preparation made, with the entry's frame
// still decoded beside it. Once the worker has added each frame to its body,
// the Cloud holds every record byte for byte as a Cloud that installed the
// records whole and compressed them after holds them.
func TestCloudHoldsAMergedRecordCompressedFromItsCommit(t *testing.T) {
	t.Parallel()
	bundle := storedBodyTestExport(t)
	ctx := cacheTestContext(t.Context())
	open := func(whole bool) *Cache {
		c, err := NewCache(ctx, "", nil, nil, WithBlobStore())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
		// The body worker stays stopped until the test runs it: what the
		// commit leaves is read as the commit left it. The whole arm
		// installs every record whole, as a merge does a record its commit
		// relocates under the lock.
		storedTestHoldWhole(c)
		c.testMergesInstallWhole = whole
		reply, err := c.MergeValues(ctx, "cache-a", bundle)
		require.NoError(t, err)
		require.True(t, reply.Committed)
		return c
	}
	prepared, whole := open(false), open(true)

	byRecipe := func(c *Cache) map[string]*sharedResult {
		out := map[string]*sharedResult{}
		for _, res := range c.resultsByID {
			if res.persistedEnvelope != nil && len(res.recipeKeys) > 0 {
				out[res.recipeKeys[0].String()] = res
			}
		}
		return out
	}

	stored := 0
	func() {
		prepared.egraphMu.RLock()
		defer prepared.egraphMu.RUnlock()
		for _, res := range prepared.resultsByID {
			if res.persistedEnvelope == nil {
				continue
			}
			stored++
			require.NotNil(t, res.stored, "entry %d holds its record compressed from its commit", res.id)
			require.False(t, res.stored.holdsFrame(), "entry %d's body waits for the body worker to add its frame", res.id)
			require.NotNil(t, res.loadResultCall(), "entry %d keeps its decoded frame until then", res.id)
			require.Same(t, res.loadResultCall(), res.stored.call, "entry %d's body is made with the entry's frame", res.id)
			require.Same(t, sharedLightStoredEnvelope(res.persistedEnvelope), res.persistedEnvelope, "entry %d keeps its record's shared light envelope", res.id)
		}
	}()
	require.Equal(t, storedBodyTestRecords, stored)

	storedTestCompress(prepared)
	storedTestCompress(whole)
	requireFramesHeldCompressed(t, prepared)
	requireFramesHeldCompressed(t, whole)

	prepared.egraphMu.RLock()
	defer prepared.egraphMu.RUnlock()
	whole.egraphMu.RLock()
	defer whole.egraphMu.RUnlock()
	got, want := byRecipe(prepared), byRecipe(whole)
	require.Len(t, got, storedBodyTestRecords)
	require.Len(t, want, storedBodyTestRecords)
	for recipe, w := range want {
		g := got[recipe]
		require.NotNil(t, g, "recipe %s is stored", recipe)
		require.Equal(t, w.stored.bytes, g.stored.bytes, "recipe %s: the same body, envelope and frame", recipe)
		require.Equal(t, w.stored.envelopeLen, g.stored.envelopeLen)
		require.Equal(t, w.stored.envelopeBytes, g.stored.envelopeBytes)
		require.Equal(t, w.stored.frameBytes, g.stored.frameBytes)
		require.Equal(t, w.stored.refs.list(), g.stored.refs.list(), "recipe %s: the same references", recipe)
		require.Equal(t, w.stored.recipe, g.stored.recipe)
		require.Same(t, w.persistedEnvelope, g.persistedEnvelope, "recipe %s: the same light envelope", recipe)
		require.Equal(t, w.partFacts, g.partFacts, "recipe %s: the same parts", recipe)
		require.Equal(t, w.storedRecordBytes, g.storedRecordBytes, "recipe %s: the same size", recipe)
		require.Equal(t, w.payloadBytes.Load(), g.payloadBytes.Load(), "recipe %s: the same payload bytes", recipe)
	}
}

// A size measured of a record held in the body its merge's preparation made
// is kept when the body worker adds the record's frame to that body: a value
// measures the same with its frame decoded or held in its body.
func TestCloudKeepsAPreparedBodysSizeWhenItsFrameIsAdded(t *testing.T) {
	t.Parallel()
	bundle := storedBodyTestExportOf(t, 8)
	ctx := cacheTestContext(t.Context())
	cloud, err := NewCache(ctx, "", nil, nil, WithBlobStore())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cloud.CloseDiscardingPersistence()) })
	// The body worker stays stopped until the test runs it.
	cloud.closeStoredBodies()
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	var roots []uint64
	for _, value := range reply.Imported() {
		roots = append(roots, value.ResultID)
	}
	require.Len(t, roots, 8)
	_, err = cloud.StoredSizes(ctx, roots, time.Now().Unix(), nil)
	require.NoError(t, err)
	storedTestCompress(cloud)
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	for _, number := range roots {
		res := cloud.resultsByID[sharedResultID(number)]
		require.True(t, res.stored.holdsFrame(), "entry %d's body holds its frame", number)
		require.NotNil(t, res.stored.size.Load(), "entry %d keeps the size measured before its frame went into its body", number)
	}
}
