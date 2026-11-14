package dagql

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// markTestPayload is a test family's payload: its parts, pending unless
// done; the part whose route is a recorded delegation; the part whose route
// fails; and whether mapping its parts fails.
type markTestPayload struct {
	Parts      []string `json:"parts"`
	Done       []string `json:"done,omitempty"`
	Delegate   string   `json:"delegate,omitempty"`
	FailRoute  string   `json:"failRoute,omitempty"`
	FailMapped bool     `json:"failMapped,omitempty"`
}

type markSingleValue struct{}
type markBatchValue struct{}

func (markSingleValue) Type() *ast.Type { return nil }
func (markBatchValue) Type() *ast.Type  { return nil }

// markSingleCodec routes one part per call; markBatchCodec routes a
// payload's parts together. Each counts its routing calls and payloads.
type markSingleCodec struct{ calls *markTestCalls }
type markBatchCodec struct{ markSingleCodec }
type markTestCalls struct{ routeParts, routeEach, routedParts atomic.Int64 }

var markSingleCalls, markBatchCalls markTestCalls

func (markSingleCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	return ForeignPayload{JSON: slices.Clone(v.Payload)}, nil
}
func (markSingleCodec) ValidateForeign(PersistedPayloadVisit) error { return nil }
func (markSingleCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	var p markTestPayload
	if err := json.Unmarshal(v.Payload, &p); err != nil {
		return nil, err
	}
	if p.FailMapped {
		return nil, fmt.Errorf("mark test: mapping fails")
	}
	var out []CapturedCodecOutput
	for _, part := range p.Parts {
		state := "pending"
		if slices.Contains(p.Done, part) {
			state = "completed"
		}
		out = append(out, CapturedCodecOutput{Address: PersistedPartAddress{OutputPath: v.Path, Part: PartKey(part)}, State: state, ValueKind: "directory"})
	}
	return out, nil
}
func markTestRoute(v PersistedPayloadVisit, part PartKey) (LazyOperationRoute, error) {
	var p markTestPayload
	if err := json.Unmarshal(v.Payload, &p); err != nil {
		return LazyOperationRoute{}, err
	}
	switch string(part) {
	case p.FailRoute:
		return LazyOperationRoute{}, fmt.Errorf("mark test: route of %s fails", part)
	case p.Delegate:
		return LazyOperationRoute{Delegation: &PartDelegation{ParentResultID: 1, Address: PersistedPartAddress{Part: part}}}, nil
	}
	return LazyOperationRoute{}, nil
}
func (c markSingleCodec) RouteParts(v PersistedPayloadVisit, part PartKey) (LazyOperationRoute, error) {
	c.calls.routeParts.Add(1)
	c.calls.routedParts.Add(1)
	return markTestRoute(v, part)
}
func (c markBatchCodec) RouteEachPart(v PersistedPayloadVisit, parts []PartKey) ([]LazyOperationRoute, []error) {
	c.calls.routeEach.Add(1)
	c.calls.routedParts.Add(int64(len(parts)))
	routes, errs := make([]LazyOperationRoute, len(parts)), make([]error, len(parts))
	for i, part := range parts {
		routes[i], errs[i] = markTestRoute(v, part)
	}
	return routes, errs
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.MarkSingle", Typed: markSingleValue{}, Visitor: PersistedNoReferences{}, Transfer: markSingleCodec{calls: &markSingleCalls}})
	RegisterPersistedObjectFamily(PersistedObjectFamily{Name: "dagql_test.MarkBatch", Typed: markBatchValue{}, Visitor: PersistedNoReferences{}, Transfer: markBatchCodec{markSingleCodec{calls: &markBatchCalls}}})
}

// Decoding a row from its record marks it for foreground part acquisition
// when a pending part is a recorded delegation. Each payload's pending parts
// are routed together, one call per payload, so the payload is decoded once
// rather than once per part; a family that routes one part at a time is
// routed part by part, as before. A part whose route fails doesn't keep the
// others from marking, and a payload whose parts can't be mapped keeps the
// whole record from marking.
func TestMarkRestoredPartDelegationRoutesEachPayloadOnce(t *testing.T) {
	object := func(family string, p markTestPayload) PersistedResultEnvelope {
		raw, err := json.Marshal(p)
		require.NoError(t, err)
		return PersistedResultEnvelope{Kind: persistedResultKindObject, ObjectCodec: family, ObjectJSON: raw}
	}
	list := func(items ...PersistedResultEnvelope) PersistedResultEnvelope {
		return PersistedResultEnvelope{Kind: persistedResultKindList, Items: items}
	}
	for _, tc := range []struct {
		name       string
		envelope   PersistedResultEnvelope
		calls      *markTestCalls
		marked     bool
		routeEach  int64
		routeParts int64
		routed     int64
	}{
		{
			name:     "a payload's pending parts are routed in one call",
			envelope: object("dagql_test.MarkBatch", markTestPayload{Parts: []string{"a", "b", "c", "d"}, Done: []string{"d"}, Delegate: "c", FailRoute: "b"}),
			calls:    &markBatchCalls, marked: true, routeEach: 1, routed: 3,
		},
		{
			name:     "no delegation, no mark",
			envelope: object("dagql_test.MarkBatch", markTestPayload{Parts: []string{"a", "b"}}),
			calls:    &markBatchCalls, routeEach: 1, routed: 2,
		},
		{
			name:     "a completed delegated part doesn't mark",
			envelope: object("dagql_test.MarkBatch", markTestPayload{Parts: []string{"a", "c"}, Done: []string{"c"}, Delegate: "c"}),
			calls:    &markBatchCalls, routeEach: 1, routed: 1,
		},
		{
			name:     "a family without batches is routed part by part",
			envelope: object("dagql_test.MarkSingle", markTestPayload{Parts: []string{"a", "b", "c"}, Delegate: "c", FailRoute: "b"}),
			calls:    &markSingleCalls, marked: true, routeParts: 3, routed: 3,
		},
		{
			name: "one call per payload of a list",
			envelope: list(
				object("dagql_test.MarkBatch", markTestPayload{Parts: []string{"a", "b"}}),
				object("dagql_test.MarkBatch", markTestPayload{Parts: []string{"a", "b", "c"}, Delegate: "b"}),
			),
			calls: &markBatchCalls, marked: true, routeEach: 2, routed: 5,
		},
		{
			name: "a payload whose parts can't be mapped keeps the record from marking",
			envelope: list(
				object("dagql_test.MarkBatch", markTestPayload{Parts: []string{"a"}, Delegate: "a"}),
				object("dagql_test.MarkBatch", markTestPayload{Parts: []string{"a"}, FailMapped: true}),
			),
			calls: &markBatchCalls, routeEach: 1, routed: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, calls := range []*markTestCalls{&markSingleCalls, &markBatchCalls} {
				calls.routeParts.Store(0)
				calls.routeEach.Store(0)
				calls.routedParts.Store(0)
			}
			row := &sharedResult{}
			markRestoredPartDelegation(row, tc.envelope, nil, nil)
			require.Equal(t, tc.marked, row.partGate.restoredDelegation.Load())
			require.Equal(t, tc.routeEach, tc.calls.routeEach.Load(), "batch calls")
			require.Equal(t, tc.routeParts, tc.calls.routeParts.Load(), "single-part calls")
			require.Equal(t, tc.routed, tc.calls.routedParts.Load(), "parts routed")
		})
	}
}
