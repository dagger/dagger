package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// JSONValue.fields is cached by call, so it must list the same fields in the
// same order every time. Reading map keys in iteration order did not.
func TestJSONValueFieldsSorted(t *testing.T) {
	value := &core.JSONValue{Data: []byte(`{"name":"codegen","source":".dagger","dependencies":[],"engineVersion":"v0.21.9","runtime":{"source":"dang"},"include":[],"codegen":{},"clients":[]}`)}
	want := []dagql.String{"clients", "codegen", "dependencies", "engineVersion", "include", "name", "runtime", "source"}
	for range 20 {
		got, err := jsonvalueSchema{}.fields(context.Background(), value, struct{}{})
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

func TestJSONValueWithFieldPreservesIndexedSource(t *testing.T) {
	ctx, srv, cache, _ := resolverOutputFixture(t)
	jsonvalueSchema{}.Install(srv)
	original := core.NewIndexedJSONValue([]byte(`{"nested":{"value":1},"unchanged":true}`))
	// A child shares its parent's immutable decoded subtree.
	child, err := original.FieldAt([]string{"nested"})
	require.NoError(t, err)
	replacement := resolverAttach(t, ctx, srv, cache, "replacement", core.NewIndexedJSONValue([]byte(`2`)))
	id, err := replacement.ID()
	require.NoError(t, err)
	updated, err := (jsonvalueSchema{}).withField(ctx, child, struct {
		Path  []dagql.String
		Value core.JSONValueID
	}{Path: []dagql.String{"value"}, Value: dagql.NewID[*core.JSONValue](id)})
	require.NoError(t, err)
	require.JSONEq(t, `{"value":2}`, string(updated.Data))
	before, err := original.FieldAt([]string{"nested", "value"})
	require.NoError(t, err)
	require.JSONEq(t, `1`, string(before.Data))
	before, err = child.FieldAt([]string{"value"})
	require.NoError(t, err)
	require.JSONEq(t, `1`, string(before.Data))
}
