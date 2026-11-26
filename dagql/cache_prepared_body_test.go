package dagql

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
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
