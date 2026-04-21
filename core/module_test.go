package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestNamespaceSourceMap(t *testing.T) {
	mod := &Module{NameField: "mymod"}

	t.Run("leaves empty source map unchanged", func(t *testing.T) {
		result, err := mod.namespaceSourceMap(context.Background(), "sub", dagql.Null[dagql.ObjectResult[*SourceMap]]())
		require.NoError(t, err)
		require.False(t, result.Valid)
	})
}
