package schema

import (
	"testing"

	"github.com/dagger/dagger/core"
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

			_, _, configurable := typeInfoFromTypeDef(tt.typeDef)
			require.Equal(t, tt.configurable, configurable)
		})
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
