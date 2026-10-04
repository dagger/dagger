package gogenerator

import (
	"testing"

	dagger "dagger.io/dagger"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
)

func TestNewClientGoModUsesSDKDependencies(t *testing.T) {
	sdkMod, err := modfile.Parse("go.mod", dagger.GoMod, nil)
	require.NoError(t, err)
	mod, err := newClientGoMod("example.com/app/dagger")
	require.NoError(t, err)
	require.Equal(t, "example.com/app/dagger", mod.Module.Mod.Path)
	require.Equal(t, sdkMod.Go.Version, mod.Go.Version)
	want := map[string]string{}
	for _, req := range sdkMod.Require {
		want[req.Mod.Path] = req.Mod.Version
	}
	got := map[string]string{}
	for _, req := range mod.Require {
		got[req.Mod.Path] = req.Mod.Version
	}
	require.Equal(t, want, got)
}
