package core

import (
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestApplyLegacyCustomizationsToTypeDefs(t *testing.T) {
	dag := newTypeDefTestDag()

	dirObj := NewObjectTypeDef("Directory", "", nil)
	dirObjRes := newTypeDefDetachedResult(t, dag, "legacyDirectoryObjectTypeDef", dirObj)
	dirType := newTypeDefDetachedResult(t, dag, "legacyDirectoryTypeDef", (&TypeDef{}).WithObjectTypeDef(dirObjRes))
	stringType := newTypeDefDetachedResult(t, dag, "legacyStringTypeDef", (&TypeDef{}).WithKind(TypeDefKindString))

	mainObj := NewObjectTypeDef("toolchain", "", nil)
	mainObjRes := newTypeDefDetachedResult(t, dag, "legacyMainObjectTypeDef", mainObj)
	mainType := newTypeDefDetachedResult(t, dag, "legacyMainTypeDef", (&TypeDef{}).WithObjectTypeDef(mainObjRes))

	configArg := newTypeDefDetachedResult(t, dag, "legacyConfigArg", NewFunctionArg("config", dirType, "", nil, "", "", nil, nil))
	ctor := NewFunction("new", mainType).WithArg(configArg)
	mainObj.Constructor = dagql.NonNull(newTypeDefDetachedResult(t, dag, "legacyConstructor", ctor))

	configuredObj := NewObjectTypeDef("configured", "", nil)
	configuredObjRes := newTypeDefDetachedResult(t, dag, "legacyConfiguredObjectTypeDef", configuredObj)
	configuredType := newTypeDefDetachedResult(t, dag, "legacyConfiguredTypeDef", (&TypeDef{}).WithObjectTypeDef(configuredObjRes))

	versionArg := newTypeDefDetachedResult(t, dag, "legacyVersionArg", NewFunctionArg("version", stringType, "", nil, "", "", nil, nil))
	check := NewFunction("check", stringType).WithArg(versionArg)
	configuredObj.Functions = append(configuredObj.Functions, newTypeDefDetachedResult(t, dag, "legacyCheckFunction", check))

	configure := NewFunction("configure", configuredType)
	mainObj.Functions = append(mainObj.Functions, newTypeDefDetachedResult(t, dag, "legacyConfigureFunction", configure))

	mod := &Module{
		NameField:    "toolchain",
		OriginalName: "toolchain",
		ObjectDefs: dagql.ObjectResultArray[*TypeDef]{
			mainType,
			configuredType,
		},
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

	configArgSelf, ok := lookupFunctionArg(mainObject.Constructor.Value.Self(), "config")
	require.True(t, ok)
	require.Equal(t, "./custom-config.txt", configArgSelf.DefaultPath)
	require.Equal(t, []string{"node_modules"}, configArgSelf.Ignore)
	require.True(t, configArgSelf.TypeDef.Self().Optional)

	configured, ok := mod.ObjectByOriginalName("configured")
	require.True(t, ok)
	checkFn, ok := functionByOriginalName(configured, "check")
	require.True(t, ok)

	versionArgSelf, ok := lookupFunctionArg(checkFn, "version")
	require.True(t, ok)
	require.Equal(t, `"1.24.1"`, versionArgSelf.DefaultValue.String())
}
