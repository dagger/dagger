package core

import (
	"encoding/json"
	"math"
	"testing"

	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func TestJSONNullRoundTripPreservesNil(t *testing.T) {
	t.Parallel()

	var original JSON
	payload, err := json.Marshal(original)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(string(payload), "null"))

	var decoded JSON
	err = json.Unmarshal(payload, &decoded)
	assert.NilError(t, err)
	assert.Check(t, decoded == nil)
}

func TestJSONStringRoundTripPreservesBytes(t *testing.T) {
	t.Parallel()

	original := JSON(`["pkg"]`)
	payload, err := json.Marshal(original)
	assert.NilError(t, err)

	var decoded JSON
	err = json.Unmarshal(payload, &decoded)
	assert.NilError(t, err)
	assert.Check(t, is.Equal(string(decoded), `["pkg"]`))
}

func TestJSONDecodeInputRawValues(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		val  any
		want string
	}{
		{name: "object", val: map[string]any{"b": int64(1), "a": "x"}, want: `{"a":"x","b":1}`},
		{name: "empty object", val: map[string]any{}, want: `{}`},
		{name: "list", val: []any{int64(1), "two", true, nil}, want: `[1,"two",true,null]`},
		{name: "empty list literal", val: []any(nil), want: `[]`},
		{name: "int literal", val: int64(42), want: `42`},
		{name: "int", val: 42, want: `42`},
		{name: "float literal", val: 1.5, want: `1.5`},
		{name: "variable number", val: json.Number("12345678901234567890"), want: `12345678901234567890`},
		{name: "true", val: true, want: `true`},
		{name: "false", val: false, want: `false`},
		{
			name: "nested",
			val: map[string]any{
				"list":  []any{map[string]any{"deep": []any(nil)}, []any{json.Number("1.0")}},
				"obj":   map[string]any{"null": nil, "empty": []any(nil)},
				"html":  "<a & b>",
				"quote": `say "hi"`,
			},
			want: `{"html":"<a & b>","list":[{"deep":[]},[1.0]],"obj":{"empty":[],"null":null},"quote":"say \"hi\""}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input, err := JSON{}.DecodeInput(tc.val)
			assert.NilError(t, err)
			decoded, ok := input.(JSON)
			assert.Assert(t, ok, "decoded %T", input)
			assert.Check(t, is.Equal(decoded.String(), tc.want))
			assert.NilError(t, decoded.Validate())
			// raw values are stored as JSON text, so they keep producing the
			// same string literal as the equivalent string input
			fromString, err := JSON{}.DecodeInput(tc.want)
			assert.NilError(t, err)
			assert.Check(t, is.DeepEqual(decoded.ToLiteral().ToInput(), fromString.ToLiteral().ToInput()))
		})
	}
}

func TestJSONDecodeInputStringsAreJSONText(t *testing.T) {
	t.Parallel()

	for _, text := range []string{`{"a": 1}`, `[1, 2]`, `"str"`, `3`, `not json`} {
		input, err := JSON{}.DecodeInput(text)
		assert.NilError(t, err)
		assert.Check(t, is.Equal(input.(JSON).String(), text), "strings are taken as JSON text, byte for byte")
	}

	input, err := JSON{}.DecodeInput("")
	assert.NilError(t, err)
	assert.Check(t, input == nil)

	input, err = JSON{}.DecodeInput([]byte(`{"a":1}`))
	assert.NilError(t, err)
	assert.Check(t, is.Equal(input.(JSON).String(), `{"a":1}`))

	input, err = JSON{}.DecodeInput(json.RawMessage(`[true]`))
	assert.NilError(t, err)
	assert.Check(t, is.Equal(input.(JSON).String(), `[true]`))
}

func TestJSONDecodeInputRejects(t *testing.T) {
	t.Parallel()

	_, err := JSON{}.DecodeInput(nil)
	assert.ErrorContains(t, err, "cannot convert <nil> to JSON")
	_, err = JSON{}.DecodeInput(struct{}{})
	assert.ErrorContains(t, err, "cannot convert struct {} to JSON")
	_, err = JSON{}.DecodeInput(math.NaN())
	assert.ErrorContains(t, err, "cannot convert float64 to JSON")
}
