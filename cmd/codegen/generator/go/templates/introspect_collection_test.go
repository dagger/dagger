package templates

import (
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/stretchr/testify/require"
)

func TestModuleIntrospectionCollection(t *testing.T) {
	funcs := buildTestFuncs(t, "test", map[string]string{"main.go": `package main
type Test struct{}
func (*Test) Items() *Items { return nil }
// +collection
type Items struct {
  // +keys
  Names []string
  Hidden string
}
// +get
func (*Items) Lookup(name string) *Item { return nil }
func (*Items) Selected() []string { return nil }
type Item struct { Name string }
`})
	encoded, err := funcs.ModuleIntrospectionJSON("test")
	require.NoError(t, err)
	var response introspection.Response
	require.NoError(t, json.Unmarshal(encoded, &response))
	collection := response.Schema.Types.Get("TestItems")
	require.NotNil(t, collection)
	fields := map[string]*introspection.Field{}
	for _, field := range collection.Fields {
		fields[field.Name] = field
	}
	require.Len(t, fields, 6)
	for _, name := range []string{"keys", "list", "get", "subset", "selected", "id"} {
		require.Contains(t, fields, name)
	}
	require.Equal(t, "key", fields["get"].Args[0].Name)
	require.Equal(t, "TestItem", fields["get"].TypeRef.OfType.Name)
	require.Equal(t, fields["keys"].TypeRef, fields["subset"].Args[0].TypeRef)
	require.NotContains(t, fields, "batch")
	require.Nil(t, response.Schema.Types.Get("TestItems_Batch"))
}
