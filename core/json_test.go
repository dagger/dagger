package core

import (
	"encoding/json"
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
