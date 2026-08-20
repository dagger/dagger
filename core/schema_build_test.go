package core

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

func TestWorkspaceSchemaCacheIsQueryRootOwned(t *testing.T) {
	rootA := &Query{}
	rootB := &Query{}
	depsA := NewSchemaBuilder(rootA, nil)
	depsB := NewSchemaBuilder(rootB, nil)
	layeredA := NewSchemaBuilder(rootA, nil)

	cloneKey := newWorkspaceSchemaCacheKey(depsA.Clone(), "identity")
	require.Equal(t, newWorkspaceSchemaCacheKey(depsA, "identity"), cloneKey)
	changedKey := newWorkspaceSchemaCacheKey(depsA.Replacing(), "identity")
	require.NotEqual(t, cloneKey, changedKey,
		"composition-changing builders must mint distinct non-zero-size identities")

	keyA := newWorkspaceSchemaCacheKey(depsA, "same-overlay")
	cacheWorkspaceSchema(depsA, keyA, layeredA)
	cached, ok := cachedWorkspaceSchema(depsA.Clone(), newWorkspaceSchemaCacheKey(depsA.Clone(), "same-overlay"))
	require.True(t, ok, "read-only builder clones must reuse the root-owned cache")
	require.Same(t, layeredA, cached)
	_, ok = cachedWorkspaceSchema(depsB, newWorkspaceSchemaCacheKey(depsB, "same-overlay"))
	require.False(t, ok, "distinct query roots must not share schema capabilities")

	for i := range workspaceSchemaCacheLimit - 1 {
		key := newWorkspaceSchemaCacheKey(depsA, fmt.Sprintf("overlay-%d", i))
		cacheWorkspaceSchema(depsA, key, layeredA)
	}
	require.Len(t, rootA.workspaceSchemaCache, workspaceSchemaCacheLimit)
	keyB := newWorkspaceSchemaCacheKey(depsB, "root-b")
	cacheWorkspaceSchema(depsB, keyB, NewSchemaBuilder(rootB, nil))
	require.Len(t, rootB.workspaceSchemaCache, 1)

	cacheWorkspaceSchema(depsA, newWorkspaceSchemaCacheKey(depsA, "overflow"), layeredA)
	require.Len(t, rootA.workspaceSchemaCache, 1, "the cap resets only the full root")
	_, ok = cachedWorkspaceSchema(depsA, keyA)
	require.False(t, ok)
	_, ok = cachedWorkspaceSchema(depsB, keyB)
	require.True(t, ok, "one root reaching its cap must not reset another root")
}

func TestSchemaJSONFileSelectorHiddenFieldsAffectCallIdentity(t *testing.T) {
	hiddenTypes, hiddenFields := moduleIntrospectionScrubConfig()
	clientSelector := schemaJSONFileSelector("v1.0.0", nil, nil)
	moduleSelector := schemaJSONFileSelector("v1.0.0", hiddenTypes, hiddenFields)

	require.Equal(t, []string{
		"Query.currentWorkspace",
		"Query.engineVolume",
		"Query.sshfsVolume",
		"Address.volume",
	}, hiddenFields)
	require.Contains(t, hiddenTypes, "Host")
	require.NotEqual(t, selectorCallID(clientSelector).Digest(), selectorCallID(moduleSelector).Digest())

	hiddenFieldsInput, ok := dagql.Inputs(moduleSelector.Args).Lookup("hiddenFields")
	require.True(t, ok)
	require.Equal(t, `["Query.currentWorkspace","Query.engineVolume","Query.sshfsVolume","Address.volume"]`, hiddenFieldsInput.ToLiteral().Display())
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
