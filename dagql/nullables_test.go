package dagql

import (
	"encoding/json"
	"testing"

	"gotest.tools/v3/assert"
)

func TestOptionalJSONRoundTripSetsValid(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(Opt(NewString("hello")))
	assert.NilError(t, err)

	var out Optional[String]
	err = json.Unmarshal(payload, &out)
	assert.NilError(t, err)
	assert.Assert(t, out.Valid)
	assert.Equal(t, string(out.Value), "hello")

	out = Opt(NewString("stale"))
	err = json.Unmarshal([]byte("null"), &out)
	assert.NilError(t, err)
	assert.Assert(t, !out.Valid)
	assert.Equal(t, string(out.Value), "")
}

func TestNullableJSONRoundTripSetsValid(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(NonNull(NewString("hello")))
	assert.NilError(t, err)

	var out Nullable[String]
	err = json.Unmarshal(payload, &out)
	assert.NilError(t, err)
	assert.Assert(t, out.Valid)
	assert.Equal(t, string(out.Value), "hello")

	out = NonNull(NewString("stale"))
	err = json.Unmarshal([]byte("null"), &out)
	assert.NilError(t, err)
	assert.Assert(t, !out.Valid)
	assert.Equal(t, string(out.Value), "")
}

func TestDynamicOptionalJSONRoundTripSetsValid(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(DynamicOptional{
		Elem:  Int(0),
		Value: Int(5),
		Valid: true,
	})
	assert.NilError(t, err)

	out := DynamicOptional{Elem: Int(0)}
	err = json.Unmarshal(payload, &out)
	assert.NilError(t, err)
	assert.Assert(t, out.Valid)
	val, ok := out.Value.(Int)
	assert.Assert(t, ok)
	assert.Equal(t, int(val), 5)

	out = DynamicOptional{
		Elem:  Int(0),
		Value: Int(99),
		Valid: true,
	}
	err = json.Unmarshal([]byte("null"), &out)
	assert.NilError(t, err)
	assert.Assert(t, !out.Valid)
	assert.Assert(t, out.Value == nil)
	_, ok = out.Elem.(Int)
	assert.Assert(t, ok)
}

func TestDynamicNullableJSONRoundTripSetsValid(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(DynamicNullable{
		Elem:  NewString(""),
		Value: NewString("hello"),
		Valid: true,
	})
	assert.NilError(t, err)

	out := DynamicNullable{Elem: NewString("")}
	err = json.Unmarshal(payload, &out)
	assert.NilError(t, err)
	assert.Assert(t, out.Valid)
	val, ok := out.Value.(String)
	assert.Assert(t, ok)
	assert.Equal(t, string(val), "hello")

	out = DynamicNullable{
		Elem:  NewString(""),
		Value: NewString("stale"),
		Valid: true,
	}
	err = json.Unmarshal([]byte("null"), &out)
	assert.NilError(t, err)
	assert.Assert(t, !out.Valid)
	assert.Assert(t, out.Value == nil)
	_, ok = out.Elem.(String)
	assert.Assert(t, ok)
}
