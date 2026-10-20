package dangv2

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vito/dang/v2/pkg/dang"
	"github.com/vito/dang/v2/pkg/hm"
)

func TestConvertListPreservesElementType(t *testing.T) {
	t.Parallel()

	stringType := hm.NonNullType{Type: dang.StringType}
	for _, tc := range []struct {
		name     string
		values   []any
		elemType hm.Type
	}{
		{"strings", []any{"first", "second"}, stringType},
		{"empty", []any{}, stringType},
		{"nullable elements", []any{nil, "second"}, dang.StringType},
		{"nested", []any{[]any{"first"}, []any{}}, hm.NonNullType{Type: dang.ListType{Type: stringType}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fieldType := dang.ListType{Type: tc.elemType}
			value, err := (dangConverter{}).convert(t.Context(), tc.values, fieldType)
			require.NoError(t, err)
			require.Equal(t, (hm.NonNullType{Type: fieldType}).String(), value.Type().String())

			wantJSON, err := json.Marshal(tc.values)
			require.NoError(t, err)
			gotJSON, err := json.Marshal(value)
			require.NoError(t, err)
			require.JSONEq(t, string(wantJSON), string(gotJSON))
		})
	}
}
