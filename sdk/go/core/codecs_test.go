package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCoreHandlesDecodeJSONIDs(t *testing.T) {
	var container *Container
	require.NoError(t, json.Unmarshal([]byte(`"container-id"`), &container))
	require.NotNil(t, container)
	require.NotNil(t, container.query)
	var object NodeClient
	require.NoError(t, json.Unmarshal([]byte(`"node-id"`), &object))
	require.NotNil(t, object.query)
	var objects []*Container
	require.NoError(t, json.Unmarshal([]byte(`["first",null,"second"]`), &objects))
	require.Len(t, objects, 3)
	require.NotNil(t, objects[0].query)
	require.Nil(t, objects[1])
	require.NotNil(t, objects[2].query)
	require.Error(t, json.Unmarshal([]byte(`{"id":"not-an-id-string"}`), &container))
}
