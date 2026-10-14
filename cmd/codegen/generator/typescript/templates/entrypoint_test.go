package templates

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func renderEntrypoint(t *testing.T, module *TypedefModule) string {
	t.Helper()
	tmpl := NewEntrypoint(module, EntrypointOptions{ModuleRoot: "/src"})
	var b bytes.Buffer
	require.NoError(t, tmpl.ExecuteTemplate(&b, "entrypoint", module))
	return b.String()
}

func entrypointModule(withInterface bool) *TypedefModule {
	str := &TypedefType{Kind: KindString}
	module := &TypedefModule{
		Name: "Pond",
		Objects: map[string]*TypedefObject{
			"Pond": {
				Name:       "Pond",
				Kind:       "class",
				IsExported: true,
				Location:   &TypedefLocation{Filepath: "/src/src/index.ts"},
				Methods: map[string]*TypedefFunction{
					"hello": {Name: "hello", ReturnType: str},
				},
				Properties: map[string]*TypedefProperty{},
			},
		},
		Enums:      map[string]*TypedefEnum{},
		Interfaces: map[string]*TypedefInterface{},
	}
	if withInterface {
		module.Interfaces["HttpFetcher"] = &TypedefInterface{
			Name: "HttpFetcher",
			Functions: map[string]*TypedefFunction{
				"getUrl": {
					Name:       "getUrl",
					ReturnType: str,
					Arguments:  []*TypedefArgument{{Name: "comUrl", Type: str}},
				},
			},
		}
	}
	return module
}

// Calls through the module's interfaces use the names the engine gives them,
// resolved at runtime, with the names the runtime always used as fallback.
func TestEntrypointInterfaceSchemaNames(t *testing.T) {
	got := renderEntrypoint(t, entrypointModule(true))

	require.Contains(t, got, "SchemaNames as __SchemaNames, resolveSchemaNames as __resolveSchemaNames")
	require.Contains(t, got, `let __schemaNames = new __SchemaNames()`)
	require.Contains(t, got, `await __resolveSchemaNames(
    "Pond",
    [{"name":"HttpFetcher","functions":[{"name":"getUrl","args":["comUrl"]}]}],
  )`)
	require.Contains(t, got, "await __loadSchemaNames()\n      const result = await invoke(")

	require.Contains(t, got, `selectNode(id, __schemaNames.interfaceName("HttpFetcher", "PondHttpFetcher"))`)
	require.Contains(t, got, `__args[__schemaNames.fieldName("comUrl")] = comUrl`)
	require.Contains(t, got, `this._ctx.select(__schemaNames.fieldName("getUrl"), __args)`)
	// the TS method keeps its name
	require.Contains(t, got, "async getUrl(comUrl: any)")
}

func TestEntrypointWithoutInterfaces(t *testing.T) {
	got := renderEntrypoint(t, entrypointModule(false))

	require.NotContains(t, got, "SchemaNames")
	require.NotContains(t, got, "__loadSchemaNames")
}
