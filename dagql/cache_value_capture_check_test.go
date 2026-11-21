package dagql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/engine/snapshots/config"
)

// An export checks each record it carries in full with each of its
// normalized payloads walked for its references once: the walk that
// normalizes the record's references also gives the references its
// validation checks against the record's dependencies, and the payload's
// codec validates it without walking its references again
// (ReferencesVisitedApart). The record as it was copied is still walked
// first, for references to rows that aren't its dependencies.
func TestExportChecksANormalizedPayloadsReferencesOnce(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	child := persistedListTestResult(t, ctx, c, srv, "checked-once-child", String("child"))
	root := persistedListTestResult(t, ctx, c, srv, "checked-once-root", &transferTestValue{Text: "checked-once-payload", Child: rowNumber(child)})
	transferTestDependency(c, ctx, root, child)

	var mu sync.Mutex
	var visits, validates int
	var apart []bool
	steps := func(step string, v PersistedPayloadVisit) {
		if !bytes.Contains(v.Payload, []byte("checked-once-payload")) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch step {
		case "visit":
			visits++
		case "validate":
			validates++
			apart = append(apart, v.ReferencesVisitedApart)
		}
	}
	transferTestCodecSteps.Store(&steps)
	defer transferTestCodecSteps.Store(nil)
	require.NoError(t, c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{root}}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		require.Len(t, values.Bundle.Values, 2)
		return nil
	}))
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 2, visits, "the copied record's references, then the normalized one's, each walked once")
	require.Equal(t, 1, validates)
	require.Equal(t, []bool{true}, apart, "the codec validates the payload without walking its references again")
}

// normalizeChildValue is a value whose codec's normalization points its child
// reference at Child, for a record whose references change when it is
// normalized.
type normalizeChildValue struct {
	Text  string `json:"text"`
	Child uint64 `json:"child,omitempty"`
	// Normalized is the child the normalized payload names.
	Normalized uint64 `json:"normalized,omitempty"`
}

func (*normalizeChildValue) Type() *ast.Type {
	return &ast.Type{NamedType: "NormalizeChildValue", NonNull: true}
}

func (v *normalizeChildValue) EncodePersistedObject(context.Context, *PersistEncodeContext) (PersistedObjectEncoding, error) {
	raw, err := json.Marshal(v)
	return PersistedObjectEncoding{JSON: raw}, err
}

func (*normalizeChildValue) DecodePersistedObject(_ context.Context, _ *PersistDecodeContext, raw json.RawMessage) (Typed, error) {
	v := new(normalizeChildValue)
	return v, json.Unmarshal(raw, v)
}

func (*normalizeChildValue) PersistedOutputRevision() (OutputRevision, error) { return 0, nil }

type normalizeChildCodec struct{}

func (normalizeChildCodec) VisitPersistedReferences(v PersistedPayloadVisit, visit PersistedRefVisitor) (json.RawMessage, error) {
	value := new(normalizeChildValue)
	if err := json.Unmarshal(v.Payload, value); err != nil {
		return nil, err
	}
	if value.Child != 0 {
		if _, err := VisitPersistedRow(visit, PersistedRefChild, v.Path.Field("child"), &value.Child); err != nil {
			return nil, err
		}
	}
	return json.Marshal(value)
}

func (normalizeChildCodec) NormalizeForeign(v PersistedPayloadVisit) (ForeignPayload, error) {
	value := new(normalizeChildValue)
	if err := json.Unmarshal(v.Payload, value); err != nil {
		return ForeignPayload{}, err
	}
	if value.Normalized != 0 {
		value.Child = value.Normalized
	}
	raw, err := json.Marshal(value)
	return ForeignPayload{JSON: raw}, err
}

func (normalizeChildCodec) ValidateForeign(PersistedPayloadVisit) error { return nil }

func (normalizeChildCodec) MapSnapshotParts(PersistedPayloadVisit) ([]CapturedCodecOutput, error) {
	return nil, nil
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{
		Name: "dagql_test.NormChild", Typed: (*normalizeChildValue)(nil),
		Visitor: normalizeChildCodec{}, Transfer: normalizeChildCodec{},
	})
}

// A record whose normalized form names a row that isn't its dependency is
// refused once it is normalized, as a refusal, with validation's error
// naming the payload the reference is in, and the other roots go.
func TestExportRefusesARecordWhoseNormalizedFormNamesAnotherRow(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	srv.InstallObject(NewClass(srv, ClassOpts[*normalizeChildValue]{}))
	ready := persistedListTestResult(t, ctx, c, srv, "normalized-ready", String("ready"))
	child := persistedListTestResult(t, ctx, c, srv, "normalized-child", String("child"))
	other := persistedListTestResult(t, ctx, c, srv, "normalized-other", String("other"))
	bad := persistedListTestResult(t, ctx, c, srv, "normalized-bad", &normalizeChildValue{Text: "bad", Child: rowNumber(child), Normalized: rowNumber(other)})
	transferTestDependency(c, ctx, bad, child)

	var values *ExportedValues
	require.NoError(t, c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{bad, ready}}, config.RefConfig{}, func(_ context.Context, exported *ExportedValues) error {
		values = exported
		return nil
	}))
	require.Len(t, values.LeftOut, 1)
	left := values.LeftOut[0]
	require.Equal(t, 0, left.Index)
	require.Equal(t, rowNumber(bad), left.Row)
	require.Empty(t, left.Kind, "a refusal has no busy kind")
	require.NotErrorIs(t, left.Err, ErrPersistStateNotReady)
	require.EqualError(t, left.Err, fmt.Sprintf(`row %d: visit persisted dagql_test.NormChild payload at "": reference objectJSON.child to %d is not a direct dependency`, rowNumber(bad), rowNumber(other)))
	require.Equal(t, map[uint64]bool{rowNumber(ready): true}, senderNumbers(values.Bundle))
}
