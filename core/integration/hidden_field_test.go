package core

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// selectHidden selects field(args) on the object with ID id, a field the SDK
// does not generate, and returns the ID of its result.
func selectHidden(ctx context.Context, t *testctx.T, c *dagger.Client, id core.ID, typ, field string, args map[string]any) core.ID {
	t.Helper()
	decls, params := []string{"$id: ID!"}, []string{}
	vars := map[string]any{"id": id}
	for _, name := range slices.Sorted(maps.Keys(args)) {
		var argType string
		switch args[name].(type) {
		case core.ID:
			argType = "ID!"
		case []core.ID:
			argType = "[ID!]!"
		case string:
			argType = "String!"
		default:
			t.Fatalf("argument %s has unsupported type %T", name, args[name])
		}
		decls = append(decls, "$"+name+": "+argType)
		params = append(params, name+": $"+name)
		vars[name] = args[name]
	}
	var result struct {
		Node map[string]struct{ ID core.ID }
	}
	query := fmt.Sprintf("query(%s) { node(id: $id) { ... on %s { %s(%s) { id } } } }", strings.Join(decls, ", "), typ, field, strings.Join(params, ", "))
	require.NoError(t, c.Do(ctx, &dagger.Request{Query: query, Variables: vars}, &dagger.Response{Data: &result}), field)
	require.NotEmpty(t, result.Node[field].ID, field)
	return result.Node[field].ID
}
