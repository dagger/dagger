package dagql

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

type quiescentPersistTestValue struct{ seen []bool }

func (*quiescentPersistTestValue) Type() *ast.Type {
	return &ast.Type{NamedType: "QuiescentPersistTestValue", NonNull: true}
}

func (v *quiescentPersistTestValue) EncodePersistedObject(_ context.Context, enc *PersistEncodeContext) (PersistedObjectEncoding, error) {
	v.seen = append(v.seen, enc.Quiescent())
	return PersistedObjectEncoding{JSON: json.RawMessage(`{}`)}, nil
}

func init() {
	RegisterPersistedObjectFamily(PersistedObjectFamily{
		Name: "dagql_test.QuiescentPersistTestValue", Typed: (*quiescentPersistTestValue)(nil), Visitor: persistTestSnapshotRoleVisitor{},
	})
}

func TestPersistedEncodingQuiescenceReachesInlineObjects(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "object"
		if nested {
			name = "nested-inline-list"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cache, _ := persistedListTestCache(t, "")
			probe := &quiescentPersistTestValue{}
			var value Typed = probe
			if nested {
				value = Array[Array[*quiescentPersistTestValue]]{{probe}}
			}
			frame := persistCodecFrame("quiescence", value)
			res, err := NewResultForCall(value, frame)
			require.NoError(t, err)
			before, err := DefaultPersistedSelfCodec.EncodeResult(ctx, nil, res)
			require.NoError(t, err)
			require.Equal(t, []bool{false}, probe.seen)
			after, err := cache.persistResultEnvelope(ctx, &persistResultSnapshot{
				self: value, hasValue: true, isObject: res.cacheSharedResult().isObject, frame: frame,
			})
			require.NoError(t, err)
			require.Equal(t, []bool{false, true}, probe.seen)
			require.Equal(t, before, after, "the shutdown context must not affect persisted bytes")
			_, err = DefaultPersistedSelfCodec.EncodeResult(ctx, nil, res)
			require.NoError(t, err)
			require.Equal(t, []bool{false, true, false}, probe.seen, "shutdown mode must not escape its encode")
		})
	}
}
