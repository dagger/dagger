package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestNamespaceSourceMap(t *testing.T) {
	mod := &Module{NameField: "mymod"}

	t.Run("sets module name when SDK does not provide source map", func(t *testing.T) {
		result := mod.namespaceSourceMap("sub", dagql.Null[*SourceMap]())
		require.True(t, result.Valid)
		require.Equal(t, "mymod", result.Value.Module)
	})
}
