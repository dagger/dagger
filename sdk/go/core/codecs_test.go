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

func TestWorkspaceProvenanceHandlesDecodeJSONIDs(t *testing.T) {
	var inputs struct {
		Provenance *WorkspaceProvenance `json:"provenance"`
		Selectors  []*WorkspaceSelector `json:"selectors"`
	}
	require.NoError(t, json.Unmarshal([]byte(`{"provenance":"provenance-id","selectors":["selector-id",null]}`), &inputs))
	require.NotNil(t, inputs.Provenance)
	query, err := inputs.Provenance.query.Select("selectors").Build(t.Context())
	require.NoError(t, err)
	require.Contains(t, query, `"provenance-id"`)
	require.Contains(t, query, "... on WorkspaceProvenance")
	require.Len(t, inputs.Selectors, 2)
	require.Nil(t, inputs.Selectors[1])
	query, err = inputs.Selectors[0].query.Select("path").Build(t.Context())
	require.NoError(t, err)
	require.Contains(t, query, `"selector-id"`)
	require.Contains(t, query, "... on WorkspaceSelector")
}
