package core

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestForeignModuleContextReaders(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "foreign-context")
	ctx, _, srv := env.open(t)
	src := &ModuleSource{Kind: ModuleSourceKindLocal, ModuleName: "probe", SourceRootSubpath: "module", Local: &LocalModuleSource{Foreign: true, ContextDirectoryPath: "/producer/checkout"}}
	tests := map[string]func() error{
		"directory":  func() error { _, err := src.LoadContextDir(ctx, srv, ".", CopyFilter{}); return err },
		"file":       func() error { _, err := src.LoadContextFile(ctx, srv, "notes.md"); return err },
		"git":        func() error { _, err := src.LoadContextGit(ctx, srv); return err },
		"env":        func() error { _, err := src.innerEnvFile(ctx); return err },
		"dependency": func() error { _, err := ResolveDepToSource(ctx, nil, srv, src, "./dep", "", ""); return err },
		"stat":       func() error { _, _, err := NewModuleSourceFS(nil, src).Stat(ctx, "notes.md"); return err },
		"exists": func() error {
			_, exists, err := NewModuleSourceFS(nil, src).Exists(ctx, "notes.md")
			require.False(t, exists)
			return err
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			err := run()
			require.ErrorIs(t, err, ErrForeignModuleContext)
			require.NotErrorIs(t, err, os.ErrNotExist)
			require.Contains(t, err.Error(), "probe")
		})
	}
	require.Equal(t, "/producer/checkout/module", src.AsString())
	require.True(t, src.Clone().Local.Foreign)
	native := src.Clone()
	native.Local.Foreign = false
	path, err := native.LocalContextDirectoryPath()
	require.NoError(t, err)
	require.Equal(t, "/producer/checkout", path)
	for _, kind := range []ModuleSourceKind{ModuleSourceKindGit, ModuleSourceKindDir} {
		_, err := (&ModuleSource{Kind: kind}).LocalContextDirectoryPath()
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrForeignModuleContext)
	}
}
