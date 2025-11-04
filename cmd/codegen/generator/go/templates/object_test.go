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

func TestObjectMethodDeprecated(t *testing.T) {
	schemaJSON := `
    {
      "description": "Container with deprecated method",
      "fields": [
        {
          "args": [],
          "deprecationReason": "Use ApplyV2 instead.",
          "description": "Apply configuration to the container",
          "isDeprecated": true,
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

	want := updateAndGetFixture(t, "testdata/object_method_deprecated.golden", got)

	require.Equal(t, want, got)
}

func TestObjectFieldDeprecated(t *testing.T) {
	schemaJSON := `
    {
      "description": "Test object with deprecated field",
      "fields": [
        {
          "args": [],
          "deprecationReason": "This field is deprecated and will be removed in future versions.",
          "description": "",
          "isDeprecated": true,
          "name": "legacyField",
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
      "name": "Test"
    }
`

	schema, object := loadSchemaFromTypeJSON(t, schemaJSON)
	tmpl := parseTemplateFiles(t, schema, "_types/object.go.tmpl")
	require.NotNil(t, tmpl)

	got := renderTemplate(t, tmpl, object)

	want := updateAndGetFixture(t, "testdata/object_field_deprecated.golden", got)

	require.Equal(t, want, got)
}

func TestInterfaceMethodOptionalArgDeprecated(t *testing.T) {
	schemaJSON := `
    {
      "description": "Test interface with deprecated method",
      "fields": [
        {
          "args": [
            {
              "defaultValue": null,
              "deprecationReason": "Not needed anymore.",
              "description": "",
              "isDeprecated": true,
              "name": "bar",
              "type": {
                "kind": "SCALAR",
                "name": "Int"
              }
            }
          ],
          "deprecationReason": "Use Bar instead.",
          "description": "",
          "isDeprecated": true,
          "name": "foo",
          "type": {
            "kind": "NON_NULL",
            "ofType": {
              "kind": "SCALAR",
              "name": "String"
            }
          }
        }
      ],
      "kind": "INTERFACE",
      "name": "TestFooer"
    }
`

	schema, iface := loadSchemaFromTypeJSON(t, schemaJSON)
	tmpl := parseTemplateFiles(t, schema, "_types/object.go.tmpl")
	require.NotNil(t, tmpl)

	got := renderTemplate(t, tmpl, iface)

	want := updateAndGetFixture(t, "testdata/interface_method_optional_arg_deprecated.golden", got)

	require.Equal(t, want, got)
}
