package templates

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnumDeprecated(t *testing.T) {
	schemaJSON := `
    {
      "description": "Modes available for the test",
      "enumValues": [
        {
          "description": "",
          "isDeprecated": true,
          "deprecationReason": "alpha is deprecated; use zeta instead",
          "name": "ALPHA"
        },
        {
          "description": "",
          "isDeprecated": true,
          "deprecationReason": "beta is deprecated; use zeta instead",
          "name": "BETA"
        },
        {
          "description": "",
          "isDeprecated": false,
          "deprecationReason": null,
          "name": "ZETA"
        }
      ],
      "kind": "ENUM",
      "name": "Mode"
    }
`

	schema, enumType := loadSchemaFromTypeJSON(t, schemaJSON)
	tmpl := parseTemplateFiles(t, schema, "_types/enum.go.tmpl")
	require.NotNil(t, tmpl)

	got := renderTemplate(t, tmpl, enumType)

	want := updateAndGetFixture(t, "testdata/enum_deprecated.golden", got)

	require.Equal(t, want, got)
}
