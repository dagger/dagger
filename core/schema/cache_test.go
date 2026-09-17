package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/stretchr/testify/require"
)

func TestDirectoryModuleCacheSymbolicUsesStableProvenance(t *testing.T) {
	t.Parallel()

	symbolic := func(origin, subpath string) string {
		src := &core.ModuleSource{
			Kind:              core.ModuleSourceKindDir,
			SourceRootSubpath: subpath,
			DirSrc:            &core.DirModuleSource{ContextIdentity: origin},
		}
		got, err := directoryModuleCacheSymbolic(context.Background(), src)
		require.NoError(t, err)
		return got
	}

	first := symbolic("github.com/acme/repo", "tools/agent")
	// Provenance, not directory content, supplies the identity. Reconstructing a
	// source after workspace edits therefore returns the same namespace.
	require.Equal(t, first, symbolic("github.com/acme/repo", "tools/agent"))
	require.NotEqual(t, first, symbolic("github.com/other/repo", "tools/agent"))
	require.NotEqual(t, first, symbolic("github.com/acme/repo", "tools/other"))
	require.NotContains(t, first, "github.com")
}
