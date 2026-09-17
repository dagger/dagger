package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceSettingHintTypeInfo(t *testing.T) {
	t.Parallel()

	// Object and list TypeDefs carry dagql identity in production, so build the
	// test values with the same shape instead of raw nested pointers.
	dag := workspaceSettingHintTypeTestDag(t)

	tests := []struct {
		name         string
		typeDef      *core.TypeDef
		configurable bool
		exampleValue string
	}{
		{
			name:         "primitive",
			typeDef:      &core.TypeDef{Kind: core.TypeDefKindString},
			configurable: true,
		},
		{
			name:         "address backed object",
			typeDef:      objectTypeDef(t, dag, "Secret"),
			configurable: true,
		},
		{
			name:         "volume",
			typeDef:      objectTypeDef(t, dag, "Volume"),
			configurable: true,
			exampleValue: `"engine-volume://data"`,
		},
		{
			name:         "workspace object",
			typeDef:      objectTypeDef(t, dag, "Workspace"),
			configurable: false,
		},
		{
			name:         "non address backed object",
			typeDef:      objectTypeDef(t, dag, "CacheVolume"),
			configurable: false,
		},
		{
			name:         "primitive list",
			typeDef:      listTypeDef(t, dag, &core.TypeDef{Kind: core.TypeDefKindString}),
			configurable: true,
		},
		{
			name:         "object list",
			typeDef:      listTypeDef(t, dag, objectTypeDef(t, dag, "Secret")),
			configurable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, exampleValue, configurable := typeInfoFromTypeDef(tt.typeDef)
			require.Equal(t, tt.configurable, configurable)
			if tt.exampleValue != "" {
				require.Equal(t, tt.exampleValue, exampleValue)
			}
		})
	}
}

func TestWriteSettingValueUsesSettingType(t *testing.T) {
	t.Parallel()

	base, err := workspace.WriteConfigValue(nil, "modules.go.source", "modules/go")
	require.NoError(t, err)

	tests := []struct {
		name   string
		hint   constructorArgHint
		value  string
		stored any
	}{
		{"string keeps a number-looking value", constructorArgHint{Name: "version", IsString: true}, "1.20", "1.20"},
		{"string keeps a bool-looking value", constructorArgHint{Name: "version", IsString: true}, "true", "true"},
		{"string keeps commas", constructorArgHint{Name: "version", IsString: true}, "a,b", "a,b"},
		{"string drops marking quotes", constructorArgHint{Name: "version", IsString: true}, `"1.27"`, "1.27"},
		{"address keeps a number-looking value", constructorArgHint{Name: "version", IsObject: true}, "0123", "0123"},
		{"list stores an array", constructorArgHint{Name: "version", IsList: true}, "1.20", []any{"1.20"}},
		{"int is typed from the value", constructorArgHint{Name: "version"}, "42", int64(42)},
		{"float is typed from the value", constructorArgHint{Name: "version"}, "1.5", 1.5},
		{"bool is typed from the value", constructorArgHint{Name: "version"}, "true", true},
		{"enum keeps its JSON quotes", constructorArgHint{Name: "version"}, `"FAST"`, `"FAST"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := writeSettingValue(base, "modules.go.settings.version", "go", tt.hint, tt.value)
			require.NoError(t, err)
			cfg, err := workspace.ParseConfig(data)
			require.NoError(t, err)
			require.Equal(t, tt.stored, cfg.Modules["go"].Settings["version"])
		})
	}

	_, err = writeSettingValue(base, "modules.go.settings.tags", "go", constructorArgHint{Name: "tags", IsList: true}, `["a" "b"]`)
	require.ErrorContains(t, err, `setting "tags" of module "go" is a list: `)
}

func TestWorkspaceSettingHintIsString(t *testing.T) {
	t.Parallel()

	dag := workspaceSettingHintTypeTestDag(t)
	for _, tt := range []struct {
		name     string
		typeDef  *core.TypeDef
		isString bool
	}{
		{"string", &core.TypeDef{Kind: core.TypeDefKindString}, true},
		{"integer", &core.TypeDef{Kind: core.TypeDefKindInteger}, false},
		{"float", &core.TypeDef{Kind: core.TypeDefKindFloat}, false},
		{"boolean", &core.TypeDef{Kind: core.TypeDefKindBoolean}, false},
		{"string list", listTypeDef(t, dag, &core.TypeDef{Kind: core.TypeDefKindString}), false},
		{"address backed object", objectTypeDef(t, dag, "Secret"), false},
	} {
		hint, ok := buildHintFromArg(&core.FunctionArg{Name: "arg", TypeDef: objectResult(t, dag, "arg-"+tt.name, tt.typeDef)})
		require.True(t, ok, tt.name)
		require.Equal(t, tt.isString, hint.IsString, tt.name)
	}
}

func workspaceSettingHintTypeTestDag(t *testing.T) *dagql.Server {
	t.Helper()

	dag, err := dagql.NewServer(t.Context(), &core.Query{})
	require.NoError(t, err)

	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.TypeDef]{Typed: &core.TypeDef{}}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.ListTypeDef]{Typed: &core.ListTypeDef{}}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.ObjectTypeDef]{Typed: &core.ObjectTypeDef{}}))
	return dag
}

func objectTypeDef(t *testing.T, dag *dagql.Server, name string) *core.TypeDef {
	t.Helper()

	obj := objectResult(t, dag, "object-"+name, core.NewObjectTypeDef(name, "", nil))
	return (&core.TypeDef{}).WithObject(obj)
}

func listTypeDef(t *testing.T, dag *dagql.Server, elem *core.TypeDef) *core.TypeDef {
	t.Helper()

	list := objectResult(t, dag, "list", &core.ListTypeDef{
		ElementTypeDef: objectResult(t, dag, "list-element", elem),
	})
	return (&core.TypeDef{}).WithListOf(list)
}

func objectResult[T dagql.Typed](t *testing.T, dag *dagql.Server, op string, self T) dagql.ObjectResult[T] {
	t.Helper()

	res, err := dagql.NewObjectResultForCall(self, dag, &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: "workspace-setting-hint-" + op,
		Type:        dagql.NewResultCallType(self.Type()),
	})
	require.NoError(t, err)
	return res
}
