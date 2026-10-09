package templates

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

// reevaluateSchemaJSON has a field reevaluated on every call (fresh), one
// reevaluated when its noCache argument is true (file), and an interface whose
// method returns an object.
const reevaluateSchemaJSON = `
[
  {"kind": "SCALAR", "name": "ID"},
  {"kind": "SCALAR", "name": "String"},
  {"kind": "SCALAR", "name": "Boolean"},
  {
    "kind": "OBJECT", "name": "File", "description": "",
    "fields": [
      {"name": "id", "description": "", "args": [], "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "ID"}}}
    ]
  },
  {
    "kind": "OBJECT", "name": "Host", "description": "",
    "fields": [
      {"name": "id", "description": "", "args": [], "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "ID"}}},
      {
        "name": "file", "description": "",
        "args": [
          {"name": "path", "description": "", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}},
          {"name": "noCache", "description": "", "defaultValue": "false", "type": {"kind": "SCALAR", "name": "Boolean"}}
        ],
        "type": {"kind": "NON_NULL", "ofType": {"kind": "OBJECT", "name": "File"}},
        "directives": [{"name": "reevaluate", "args": [{"name": "when", "value": "[\"noCache\"]"}]}]
      },
      {
        "name": "fresh", "description": "", "args": [],
        "type": {"kind": "NON_NULL", "ofType": {"kind": "OBJECT", "name": "File"}},
        "directives": [{"name": "reevaluate", "args": []}]
      },
      {
        "name": "cached", "description": "", "args": [],
        "type": {"kind": "NON_NULL", "ofType": {"kind": "OBJECT", "name": "File"}}
      }
    ]
  },
  {
    "kind": "INTERFACE", "name": "Source", "description": "",
    "fields": [
      {"name": "id", "description": "", "args": [], "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "ID"}}},
      {"name": "file", "description": "", "args": [], "type": {"kind": "NON_NULL", "ofType": {"kind": "OBJECT", "name": "File"}}}
    ]
  }
]
`

func loadReevaluateSchema(t *testing.T, declared bool) *introspection.Schema {
	t.Helper()
	var types introspection.Types
	require.NoError(t, json.Unmarshal([]byte(reevaluateSchemaJSON), &types))
	schema := &introspection.Schema{Types: types}
	if declared {
		schema.Directives = []*introspection.DirectiveDef{{Name: "reevaluate"}}
	}
	generator.SetSchemaParents(schema)
	generator.SetSchema(schema)
	t.Cleanup(func() { generator.SetSchema(nil) })
	return schema
}

func TestReevaluatedFieldsRefetchIDs(t *testing.T) {
	schema := loadReevaluateSchema(t, true)
	host := renderTemplate(t, parseTemplateFiles(t, schema, "_types/object.go.tmpl"), schema.Types.Get("Host"))

	// file: reevaluated only when noCache is true.
	require.Contains(t, host, "refetchID := r.refetchID")
	require.Contains(t, host, "refetchID = true")
	require.Contains(t, host, "refetchID: refetchID,")
	// fresh: always reevaluated; cached: inherits its receiver's flag.
	require.Contains(t, host, "refetchID: true,")
	require.Contains(t, host, "refetchID: r.refetchID,")
	// ID: remembered unless the object must be reevaluated.
	require.Contains(t, host, "if r.refetchID {")
	require.Contains(t, host, "memoizedID(ctx, r.query,")

	// An interface method may dispatch to an implementation that must be
	// reevaluated, so objects it returns always refetch their ID.
	funcs := GoTemplateFuncs(t.Context(), schema, nil, "v0.0.0", generator.Config{}, nil, nil, 0)
	ifaceTmpl, err := lookupTemplate(funcs, "_types/interface.go.tmpl")
	require.NoError(t, err)
	source := renderTemplate(t, ifaceTmpl, schema.Types.Get("Source"))
	require.Contains(t, source, "refetchID: true,")
	require.NotContains(t, source, "refetchID: r.refetchID,")
}

func TestIDsNotRememberedWithoutReevaluateDirective(t *testing.T) {
	schema := loadReevaluateSchema(t, false)
	host := renderTemplate(t, parseTemplateFiles(t, schema, "_types/object.go.tmpl"), schema.Types.Get("Host"))
	require.NotContains(t, host, "refetchID")
	require.NotContains(t, host, "memoizedID")
}
