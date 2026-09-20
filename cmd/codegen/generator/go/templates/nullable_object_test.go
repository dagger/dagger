package templates

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

func TestNullableInputObjectField(t *testing.T) {
	schema, input := loadSchemaFromTypeJSON(t, `{
		"kind": "INPUT_OBJECT", "name": "MediaInput", "inputFields": [
			{"name": "file", "type": {"kind": "SCALAR", "name": "ID"},
			 "directives": [{"name": "expectedType", "args": [{"name": "name", "value": "\"File\""}]}]},
			{"name": "kind", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}},
			{"name": "count", "type": {"kind": "SCALAR", "name": "Int"}},
			{"name": "enabled", "type": {"kind": "SCALAR", "name": "Boolean"}},
			{"name": "caption", "type": {"kind": "SCALAR", "name": "String"}},
			{"name": "content", "defaultValue": "[]", "type": {"kind": "LIST", "ofType": {"kind": "NON_NULL", "ofType": {"kind": "INPUT_OBJECT", "name": "MediaInput"}}}}
		]
	}`)
	result := renderTemplate(t, parseTemplateFiles(t, schema, "_types/input.go.tmpl"), input)
	// In particular, nil object inputs must be omitted before querybuilder
	// tries to resolve their GraphQL IDs.
	require.Contains(t, result, "File *File `json:\"file,omitempty\"`")
	require.Contains(t, result, "`json:\"kind\"`")
	// Optional scalar zero values must still be forwarded, not omitted.
	require.Contains(t, result, "Count int `json:\"count\"`")
	require.Contains(t, result, "Enabled bool `json:\"enabled\"`")
	require.Contains(t, result, "Caption string `json:\"caption\"`")
	require.Contains(t, result, "Content []MediaInput `json:\"content,omitempty\"`")
}

func TestNullableObjectFieldFunction(t *testing.T) {
	field := introspection.Field{
		Name:         "latestVersion",
		TypeRef:      &introspection.TypeRef{Kind: introspection.TypeKindObject, Name: "GitRef"},
		ParentObject: &introspection.Type{Name: "GitRepository"},
	}

	for _, test := range []struct {
		name          string
		schemaVersion string
		want          string
	}{
		{
			name:          "current engine",
			schemaVersion: "v1.0.0-beta.10",
			want:          "func (r *GitRepository) LatestVersion(ctx context.Context) (*GitRef, error)",
		},
		{
			name:          "older engine",
			schemaVersion: "v1.0.0-beta.9",
			want:          "func (r *GitRepository) LatestVersion() *GitRef",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			funcs := goTemplateFuncs{
				CommonFunctions: generator.NewCommonFunctions(test.schemaVersion, &FormatTypeFunc{}),
				schemaVersion:   test.schemaVersion,
			}

			signature, err := funcs.fieldFunction(field, false, true)
			require.NoError(t, err)
			require.Equal(t, test.want, signature)
		})
	}
}
