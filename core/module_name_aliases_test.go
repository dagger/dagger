package core

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

// A module named by the new rules keeps the names the legacy rules gave its
// declarations as aliases, accepted in every view: its names don't depend on
// its caller's view.
func TestModuleNameAliases(t *testing.T) {
	t.Parallel()

	aliases := LatestNamer.legacyNameAliases("getURL", "getUrl")
	require.Equal(t, []string{"getUrl"}, aliases.Names)
	require.Nil(t, aliases.View)

	require.Empty(t, LatestNamer.legacyNameAliases("getURL", "getURL").Names, "unchanged names have none")
	require.Empty(t, LatestNamer.legacyNameAliases("getURL", "").Names)
	require.Empty(t, LegacyNamer.legacyNameAliases("getUrl", "getURL").Names, "legacy modules have none")

	fn := LatestNamer.NewFunction("MakeSecretID", dagql.ObjectResult[*TypeDef]{})
	require.Equal(t, "makeSecretID", fn.Name)
	require.Equal(t, []string{"makeSecretId"}, fn.NameAliases(LatestNamer).Names)
	require.Empty(t, LegacyNamer.NewFunction("MakeSecretID", dagql.ObjectResult[*TypeDef]{}).NameAliases(LegacyNamer).Names)

	// The legacy rules keep loadFooFromID's spelling.
	load := LatestNamer.NewFunction("loadFooFromID", dagql.ObjectResult[*TypeDef]{})
	require.Empty(t, load.NameAliases(LatestNamer).Names)

	arg := LatestNamer.NewFunctionArg("legacyJSON", dagql.ObjectResult[*TypeDef]{}, "", nil, "", "", nil, nil)
	require.Equal(t, "legacyJSON", arg.Name)
	require.Equal(t, []string{"legacyJson"}, arg.NameAliases(LatestNamer).Names)

	field := LatestNamer.NewFieldTypeDef("BaseURL", dagql.ObjectResult[*TypeDef]{}, "", nil)
	require.Equal(t, "baseURL", field.Name)
	require.Equal(t, []string{"baseUrl"}, field.NameAliases(LatestNamer).Names)
}

// A module's constructor and types keep the names the legacy rules gave them
// as aliases.
func TestModuleTypeNameAliases(t *testing.T) {
	t.Parallel()

	mod := &Module{NameField: "dagger-cli", OriginalName: "dagger-cli"}
	require.False(t, mod.Namer().Legacy())

	require.Equal(t, "daggerCLI", mod.ConstructorName())
	require.Equal(t, []string{"daggerCli"}, mod.ConstructorNameAliases().Names)

	name := mod.Namer().NamespaceObject("DaggerCli", mod.Name(), mod.OriginalName)
	require.Equal(t, "DaggerCLI", name)
	require.Equal(t, []string{"DaggerCli"}, mod.TypeNameAliases(name, "DaggerCli").Names)

	name = mod.Namer().NamespaceObject("HTTPClient", mod.Name(), mod.OriginalName)
	require.Equal(t, "DaggerCLIHTTPClient", name)
	require.Equal(t, []string{"DaggerCliHttpclient"}, mod.TypeNameAliases(name, "HTTPClient").Names)

	plain := &Module{NameField: "my-mod", OriginalName: "my-mod"}
	require.Empty(t, plain.ConstructorNameAliases().Names)
	name = plain.Namer().NamespaceObject("Thing", plain.Name(), plain.OriginalName)
	require.Equal(t, "MyModThing", name)
	require.Empty(t, plain.TypeNameAliases(name, "Thing").Names)
}
