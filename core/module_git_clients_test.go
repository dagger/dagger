package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModuleTreeReaderWithoutTree(t *testing.T) {
	ctx := t.Context()
	for _, src := range []*ModuleSource{
		{Kind: ModuleSourceKindGit, Git: &GitModuleSource{}},
		{Kind: ModuleSourceKindDir, DirSrc: &DirModuleSource{}},
		{Kind: ModuleSourceKindGit},
		{Kind: ModuleSourceKindDir},
		{Kind: ModuleSourceKindLocal},
		{Kind: ModuleSourceKindLocal, Local: &LocalModuleSource{Foreign: true, ContextDirectoryPath: "/src"}},
		{Kind: ModuleSourceKind("")},
	} {
		read, err := moduleTreeReader(ctx, nil, src)
		require.NoError(t, err)
		require.Nil(t, read, src.Kind)
	}
}
