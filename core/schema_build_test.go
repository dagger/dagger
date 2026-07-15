package core

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

func TestSchemaJSONFileSelectorHiddenFieldsAffectCallIdentity(t *testing.T) {
	hiddenTypes, hiddenFields := moduleIntrospectionScrubConfig()
	clientSelector := schemaJSONFileSelector("v1.0.0", nil, nil)
	moduleSelector := schemaJSONFileSelector("v1.0.0", hiddenTypes, hiddenFields)

	require.Equal(t, []string{"Query.currentWorkspace"}, hiddenFields)
	require.Contains(t, hiddenTypes, "Host")
	require.NotEqual(t, selectorCallID(clientSelector).Digest(), selectorCallID(moduleSelector).Digest())

	hiddenFieldsInput, ok := dagql.Inputs(moduleSelector.Args).Lookup("hiddenFields")
	require.True(t, ok)
	require.Equal(t, `["Query.currentWorkspace"]`, hiddenFieldsInput.ToLiteral().Display())
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
