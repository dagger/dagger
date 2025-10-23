package templates

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestObjectOptionalArgsDeprecatedNoDescription(t *testing.T) {
	schemaJSON := `
    {
      "description": "Container with deprecated args",
      "fields": [
        {
          "args": [
            {
              "defaultValue": null,
              "description": "",
              "isDeprecated": true,
              "deprecationReason": "Templates are expanded automatically.",
              "name": "expand",
              "type": {
                "kind": "SCALAR",
                "name": "Boolean"
              }
            }
          ],
          "deprecationReason": null,
          "description": "Apply configuration to the container",
          "isDeprecated": false,
          "name": "apply",
          "type": {
            "kind": "NON_NULL",
            "ofType": {
              "kind": "OBJECT",
              "name": "Container"
            }
          }
        }
      ],
      "kind": "OBJECT",
      "name": "Container"
    }
`

	schema, object := loadSchemaFromTypeJSON(t, schemaJSON)
	tmpl := parseTemplateFiles(t, schema, "_types/object.go.tmpl")
	require.NotNil(t, tmpl)

	got := renderTemplate(t, tmpl, object)

	want := updateAndGetFixture(t, "testdata/object_optional_args_deprecated_no_description.golden", got)

	require.Equal(t, want, got)
}
