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
