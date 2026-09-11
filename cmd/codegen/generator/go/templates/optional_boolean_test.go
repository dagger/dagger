package templates

import (
	"testing"

	"github.com/dagger/dagger/cmd/codegen/generator"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/stretchr/testify/require"
)

func TestOptionalBooleanInputType(t *testing.T) {
	boolean := &introspection.TypeRef{Kind: introspection.TypeKindScalar, Name: "Boolean"}
	falseDefault := "false"
	for _, tc := range []struct {
		name, version string
		ref           *introspection.TypeRef
		defaultValue  *string
		want          string
	}{
		{"nullable", "v1.0.0-beta.12", boolean, nil, "*bool"},
		{"development", "", boolean, nil, "*bool"},
		{"prerelease build", "v1.0.0-beta.12-123-gabcdef", boolean, nil, "*bool"},
		{"historical build", "v1.0.0-beta.11-123-gabcdef", boolean, nil, "bool"},
		{"historical", "v1.0.0-beta.11", boolean, nil, "bool"},
		{"default", "v1.0.0-beta.12", boolean, &falseDefault, "bool"},
		{"required", "v1.0.0-beta.12", &introspection.TypeRef{Kind: introspection.TypeKindNonNull, OfType: boolean}, nil, "bool"},
		{"list", "v1.0.0-beta.12", &introspection.TypeRef{Kind: introspection.TypeKindList, OfType: boolean}, nil, "[]bool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			funcs := goTemplateFuncs{CommonFunctions: generator.NewCommonFunctions(tc.version, &FormatTypeFunc{}), schemaVersion: tc.version}
			got, err := funcs.FormatInputType(introspection.InputValue{TypeRef: tc.ref, DefaultValue: tc.defaultValue})
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
