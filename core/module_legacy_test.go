package core

import (
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/stretchr/testify/require"
)

func TestApplyLegacyCustomizationsToTypeDefs(t *testing.T) {
	dirType := (&TypeDef{}).WithObject("Directory", "", nil, nil)
	stringType := &TypeDef{Kind: TypeDefKindString}

	mainObj := (&TypeDef{}).WithObject("toolchain", "", nil, nil)

	ctor := NewFunction("new", mainObj).
		WithArg("config", dirType, "", nil, "", "", nil, nil, nil)
	var err error
	mainObj, err = mainObj.WithObjectConstructor(ctor)
	require.NoError(t, err)

	configuredObj := (&TypeDef{}).WithObject("configured", "", nil, nil)

	check := NewFunction("check", stringType).
		WithArg("version", stringType, "", nil, "", "", nil, nil, nil)
	configuredObj, err = configuredObj.WithFunction(check)
	require.NoError(t, err)

	configure := NewFunction("configure", configuredObj)
	mainObj, err = mainObj.WithFunction(configure)
	require.NoError(t, err)

	mod := &Module{
		NameField:    "toolchain",
		OriginalName: "toolchain",
		ObjectDefs:   []*TypeDef{mainObj, configuredObj},
	}

	mod.ApplyLegacyCustomizationsToTypeDefs([]*modules.ModuleConfigArgument{
		{
			Argument:    "config",
			DefaultPath: "./custom-config.txt",
			Ignore:      []string{"node_modules"},
		},
		{
			Function: []string{"configure", "check"},
			Argument: "version",
			Default:  "1.24.1",
		},
	})

	mainObject, ok := mod.MainObject()
	require.True(t, ok)
	require.True(t, mainObject.Constructor.Valid)

	configArg, ok := lookupFunctionArg(mainObject.Constructor.Value, "config")
	require.True(t, ok)
	require.Equal(t, "./custom-config.txt", configArg.DefaultPath)
	require.Equal(t, []string{"node_modules"}, configArg.Ignore)
	require.True(t, configArg.TypeDef.Optional)

	configured, ok := mod.ObjectByOriginalName("configured")
	require.True(t, ok)
	checkFn, ok := functionByOriginalName(configured, "check")
	require.True(t, ok)

	versionArg, ok := lookupFunctionArg(checkFn, "version")
	require.True(t, ok)
	require.Equal(t, `"1.24.1"`, versionArg.DefaultValue.String())
}
