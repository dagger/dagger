package templates

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormatParamName(t *testing.T) {
	// keywords (go/token)
	require.Equal(t, "go_", formatParamName("go"))
	require.Equal(t, "type_", formatParamName("type"))
	require.Equal(t, "range_", formatParamName("range"))
	// predeclared identifiers (types.Universe)
	require.Equal(t, "string_", formatParamName("string"))
	require.Equal(t, "nil_", formatParamName("nil"))
	require.Equal(t, "len_", formatParamName("len"))
	require.Equal(t, "comparable_", formatParamName("comparable"))
	// ordinary names pass through
	require.Equal(t, "name", formatParamName("name"))
	require.Equal(t, "stringValue", formatParamName("stringValue"))
	// "error" ships as a core parameter name (FunctionCall.returnError)
	// and never collides inside generated bodies.
	require.Equal(t, "error", formatParamName("error"))
}

// A module method with an unnamed parameter (`Hello(string)`) produces a
// GraphQL arg named "string"; the Go binding must not shadow the builtin
// type while keeping "string" as the wire name.
func TestObjectReservedIdentifiers(t *testing.T) {
	schemaJSON := `
    {
      "description": "Module object with reserved names",
      "fields": [
        {
          "args": [
            {
              "defaultValue": null,
              "description": "",
              "isDeprecated": false,
              "deprecationReason": null,
              "name": "string",
              "type": {
                "kind": "NON_NULL",
                "ofType": {
                  "kind": "SCALAR",
                  "name": "String"
                }
              }
            }
          ],
          "deprecationReason": null,
          "description": "",
          "isDeprecated": false,
          "name": "hello",
          "type": {
            "kind": "NON_NULL",
            "ofType": {
              "kind": "SCALAR",
              "name": "String"
            }
          }
        },
        {
          "args": [],
          "deprecationReason": null,
          "description": "",
          "isDeprecated": false,
          "name": "go",
          "type": {
            "kind": "NON_NULL",
            "ofType": {
              "kind": "SCALAR",
              "name": "String"
            }
          }
        }
      ],
      "kind": "OBJECT",
      "name": "Minimal"
    }
`

	schema, object := loadSchemaFromTypeJSON(t, schemaJSON)
	tmpl := parseTemplateFiles(t, schema, "_types/object.go.tmpl")

	got := renderTemplate(t, tmpl, object)

	// Parameter named after a predeclared type is escaped in the
	// signature and the query argument, but not on the wire.
	require.Contains(t, got, "string_ string")
	require.Contains(t, got, `q.Arg("string", string_)`)
	require.NotContains(t, got, `q.Arg("string_"`)

	// Scalar lazy-cache field named after a keyword is escaped.
	require.Regexp(t, `go_\s+\*string`, got)
	require.Contains(t, got, "if r.go_ != nil")
	require.Contains(t, got, `q := r.query.Select("go")`)
}
