package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

// describedPartValue has one part, "snapshot", complete once done. It
// describes its parts from its live value, as its family's codec describes its
// encoding, and counts its encodings.
type describedPartValue struct {
	done  bool
	links []PersistedSnapshotRefLink
	// cant makes the live description defer to the encoding.
	cant bool
	// fail fails both the encoding and the live description.
	fail error
	// changes advances the value's output revision while it is described.
	changes bool
	rev     atomic.Uint64
	encodes atomic.Int32
}

type persistedDescribedPartValue struct {
	Done bool `json:"done"`
}

func (*describedPartValue) Type() *ast.Type {
	return &ast.Type{NamedType: "DescribedPartValue", NonNull: true}
}

func (v *describedPartValue) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	v.encodes.Add(1)
	if v.fail != nil {
		return PersistedObjectEncoding{}, v.fail
	}
	if v.changes {
		v.rev.Add(1)
	}
	raw, err := json.Marshal(persistedDescribedPartValue{Done: v.done})
	return PersistedObjectEncoding{JSON: raw, SnapshotLinks: cloneSnapshotRefLinks(v.links)}, err
}

func (v *describedPartValue) DescribePersistedParts(_ context.Context, enc *PersistEncodeContext) (PersistedPartsDescription, bool, error) {
	if v.cant {
		return PersistedPartsDescription{}, false, nil
	}
	if v.fail != nil {
		return PersistedPartsDescription{}, false, v.fail
	}
	if v.changes {
		v.rev.Add(1)
	}
	desc := PersistedPartsDescription{SnapshotLinks: cloneSnapshotRefLinks(v.links)}
	if v.done {
		desc.Complete = []PersistedPartAddress{{OutputPath: enc.path, Part: "snapshot"}}
	}
	return desc, true, nil
}

func (*describedPartValue) DecodePersistedObject(_ context.Context, _ *PersistDecodeContext, raw json.RawMessage) (Typed, error) {
	var persisted persistedDescribedPartValue
	if err := json.Unmarshal(raw, &persisted); err != nil {
		return nil, err
	}
	return &describedPartValue{done: persisted.Done}, nil
}

func (v *describedPartValue) PersistedOutputRevision() (OutputRevision, error) {
	return OutputRevision(v.rev.Load()), nil
}

type describedPartCodec struct{}

func (describedPartCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	return ForeignPayload{JSON: slices.Clone(v.Payload)}, nil
}

func (describedPartCodec) ValidateForeign(PersistedPayloadVisit) error { return nil }

func (describedPartCodec) MapSnapshotParts(v PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	var persisted persistedDescribedPartValue
	if err := json.Unmarshal(v.Payload, &persisted); err != nil {
		return nil, err
	}
	out := CapturedCodecOutput{Address: PersistedPartAddress{OutputPath: v.Path, Part: "snapshot"}, State: "pending", ValueKind: "directory"}
	if persisted.Done {
		out.State = "completed"
	}
	return []CapturedCodecOutput{out}, nil
}

func (describedPartCodec) DescribeParts(v PersistedPayloadVisit) ([]PartProbe, error) {
	outputs, err := (describedPartCodec{}).MapSnapshotParts(v)
	if err != nil {
		return nil, err
	}
	probes := make([]PartProbe, 0, len(outputs))
	for _, out := range outputs {
		probes = append(probes, PartProbe{Descriptor: PartDescriptor{Address: out.Address}, LocalComplete: out.State == "completed"})
	}
	return probes, nil
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{
		Name: "dagql_test.DescribedPart", Typed: (*describedPartValue)(nil),
		Visitor: PersistedNoReferences{}, Transfer: describedPartCodec{},
	})
}

// recordPartKeys is what completePartKeys reported before objects described
// their own parts: the complete parts of the row's captured record, or the
// parts its gate settled when the record couldn't be captured or described.
func recordPartKeys(ctx context.Context, c *Cache, row *sharedResult) []string {
	record, err := c.captureRowRecord(ctx, row)
	if err != nil {
		return row.settledPartKeys()
	}
	probes, err := describePartRecord(record)
	if err != nil {
		return row.settledPartKeys()
	}
	return partAddressKeys(completePartAddresses(probes))
}

// A row's complete parts are the same whether its objects describe them from
// their live values or its record is captured and described, in every case
// the record's capture and description handle: a value that can't describe
// itself, a description that fails, links the record would refuse, an output
// that changes while it is read, and inline list items. A value that
// describes itself isn't encoded.
func TestCompletePartsDescribedFromLiveValues(t *testing.T) {
	t.Parallel()
	key := func(path PersistedRefPath) string {
		key, err := partAddressKey(PersistedPartAddress{OutputPath: path, Part: "snapshot"})
		require.NoError(t, err)
		return key
	}
	duplicate := []PersistedSnapshotRefLink{{RefKey: "a", Role: "snapshot"}, {RefKey: "b", Role: "snapshot"}}
	for _, test := range []struct {
		name    string
		value   func() Typed
		want    []string
		encodes int32
	}{
		{name: "complete", value: func() Typed { return &describedPartValue{done: true} }, want: []string{key(nil)}},
		{name: "pending", value: func() Typed { return &describedPartValue{} }},
		{name: "with its snapshot link", value: func() Typed {
			return &describedPartValue{done: true, links: []PersistedSnapshotRefLink{{RefKey: "ref", Role: "snapshot"}}}
		}, want: []string{key(nil)}},
		{name: "deferring to its encoding", value: func() Typed { return &describedPartValue{done: true, cant: true} }, want: []string{key(nil)}, encodes: 1},
		{name: "failing", value: func() Typed { return &describedPartValue{done: true, fail: errors.New("boom")} }},
		{name: "with a role declared twice", value: func() Typed { return &describedPartValue{done: true, links: duplicate} }},
		{name: "with a link inside itself", value: func() Typed {
			return &describedPartValue{done: true, links: []PersistedSnapshotRefLink{{OutputPath: PersistedRefPath{}.Field("items").Index(0), RefKey: "ref", Role: "snapshot"}}}
		}},
		{name: "changing while read", value: func() Typed { return &describedPartValue{done: true, changes: true} }},
		{name: "inline list items", value: func() Typed {
			return Array[*describedPartValue]{{done: true}, {}, {done: true, cant: true}}
		}, want: sortedStrings([]string{key(PersistedRefPath{}.Field("items").Index(0)), key(PersistedRefPath{}.Field("items").Index(2))}), encodes: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, c, srv := statePartCache(t, "")
			t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
			srv.InstallObject(NewClass(srv, ClassOpts[*describedPartValue]{}))
			value := test.value()
			res := persistedListTestResult(t, ctx, c, srv, "described", value)
			row := res.cacheSharedResult()
			encodes := func() int32 {
				switch value := value.(type) {
				case *describedPartValue:
					return value.encodes.Load()
				case Array[*describedPartValue]:
					var n int32
					for _, item := range value {
						n += item.encodes.Load()
					}
					return n
				}
				return 0
			}

			before := encodes()
			keys, version, err := c.describeRowParts(ctx, row)
			described := encodes() - before
			if err != nil {
				keys = row.settledPartKeys()
			} else {
				require.NotNil(t, version)
			}
			require.Equal(t, test.want, keys)
			require.Equal(t, test.encodes, described, "objects encoded while described")
			require.Equal(t, recordPartKeys(ctx, c, row), keys)
			require.Equal(t, test.want, c.completePartKeys(ctx, row))
		})
	}
}
