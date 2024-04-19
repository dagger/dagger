package moby_buildkit_v1_sourcepolicy //nolint:revive

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestActionJSON(t *testing.T) {
	for i, s := range PolicyAction_name {
		// marshals to string form
		data, err := json.Marshal(PolicyAction(i))
		require.NoError(t, err)
		require.Equal(t, string(data), `"`+s+`"`)

		// unmarshals froms string form
		var a PolicyAction
		err = json.Unmarshal(data, &a)
		require.NoError(t, err)
		require.Equal(t, a, PolicyAction(i))

		// unmarshals froms number form
		data, err = json.Marshal(i)
		require.NoError(t, err)

		var a2 PolicyAction
		err = json.Unmarshal(data, &a2)
		require.NoError(t, err)
		require.Equal(t, a, a2)
	}
}

func TestAttrMatchJSON(t *testing.T) {
	for i, s := range AttrMatch_name {
		// marshals to string form
		data, err := json.Marshal(AttrMatch(i))
		require.NoError(t, err)
		require.Equal(t, string(data), `"`+s+`"`)

		// unmarshals froms string form
		var a AttrMatch
		err = json.Unmarshal(data, &a)
		require.NoError(t, err)
		require.Equal(t, a, AttrMatch(i))

		// unmarshals froms number form
		data, err = json.Marshal(i)
		require.NoError(t, err)

		var a2 AttrMatch
		err = json.Unmarshal(data, &a2)
		require.NoError(t, err)
		require.Equal(t, a, a2)
	}
}

func TestMatchTypeJSON(t *testing.T) {
	for i, s := range MatchType_name {
		// marshals to string form
		data, err := json.Marshal(MatchType(i))
		require.NoError(t, err)
		require.Equal(t, `"`+s+`"`, string(data))

		// unmarshals froms string form
		var a MatchType
		err = json.Unmarshal(data, &a)
		require.NoError(t, err)
		require.Equal(t, a, MatchType(i))

		// unmarshals froms number form
		data, err = json.Marshal(i)
		require.NoError(t, err)

		var a2 MatchType
		err = json.Unmarshal(data, &a2)
		require.NoError(t, err)
		require.Equal(t, a, a2)
	}
}
