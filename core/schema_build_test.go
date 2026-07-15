package core

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

func TestSchemaJSONFileSelectorHiddenFieldsAffectCallIdentity(t *testing.T) {
	withoutHiddenFields := schemaJSONFileSelector("v1.0.0", []string{"Host"}, nil)
	withHiddenField := schemaJSONFileSelector("v1.0.0", []string{"Host"}, []string{"Query.currentWorkspace"})

	require.NotEqual(t, selectorCallID(withoutHiddenFields).Digest(), selectorCallID(withHiddenField).Digest())

	hiddenFields, ok := dagql.Inputs(withHiddenField.Args).Lookup("hiddenFields")
	require.True(t, ok)
	require.Equal(t, `["Query.currentWorkspace"]`, hiddenFields.ToLiteral().Display())
}

func selectorCallID(selector dagql.Selector) *call.ID {
	args := make([]*call.Argument, 0, len(selector.Args))
	for _, arg := range selector.Args {
		args = append(args, call.NewArgument(arg.Name, arg.Value.ToLiteral(), false))
	}
	return call.New().Append(
		&ast.Type{NamedType: "File", NonNull: true},
		selector.Field,
		call.WithArgs(args...),
		call.WithView(selector.View),
	)
}
