package templates

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
)

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
