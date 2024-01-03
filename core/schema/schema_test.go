package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/stretchr/testify/require"
)

func TestMergeObjects(t *testing.T) {
	t.Parallel()
	merged, err := mergeSchemaResolvers(
		StaticSchema(StaticSchemaParams{
			Schema: `
			type Query {
				typeA: TypeA
			}

			type TypeA {
				fieldA: String
			}
			`,
			Resolvers: Resolvers{
				"TypeA": ObjectResolver{
					"fieldA": nil,
				},
			},
		}),

		StaticSchema(StaticSchemaParams{
			Schema: `
			extend type Query {
				typeB: TypeB
			}

			type TypeB {
				fieldB: String
			}
			`,
			Resolvers: Resolvers{
				"TypeB": ObjectResolver{
					"fieldB": nil,
				},
			},
		}),
	)
	require.NoError(t, err)

	require.Contains(t, merged.Schema(), "TypeA")
	require.Contains(t, merged.Schema(), "TypeB")

	require.Contains(t, merged.Resolvers(), "TypeA")
	require.Contains(t, merged.Resolvers(), "TypeB")

	require.Contains(t, merged.Resolvers(), "TypeA")
	require.IsType(t, merged.Resolvers()["TypeA"], ObjectResolver{})
	require.Contains(t, merged.Resolvers()["TypeA"], "fieldA")

	require.Contains(t, merged.Resolvers(), "TypeB")
	require.IsType(t, merged.Resolvers()["TypeB"], ObjectResolver{})
	require.Contains(t, merged.Resolvers()["TypeB"], "fieldB")
}

func TestMergeFieldExtend(t *testing.T) {
	t.Parallel()
	merged, err := mergeSchemaResolvers(
		StaticSchema(StaticSchemaParams{
			Schema: `
			type Query {
				typeA: TypeA
			}

			type TypeA {
				fieldA: String
			}
			`,
			Resolvers: Resolvers{
				"TypeA": ObjectResolver{
					"fieldA": nil,
				},
			},
		}),

		StaticSchema(StaticSchemaParams{
			Schema: `
			extend type TypeA {
				fieldB: String
			}
			`,
			Resolvers: Resolvers{
				"TypeA": ObjectResolver{
					"fieldB": nil,
				},
			},
		}),
	)
	require.NoError(t, err)
	require.NotNil(t, merged)

	require.Contains(t, merged.Resolvers(), "TypeA")
	require.IsType(t, merged.Resolvers()["TypeA"], ObjectResolver{})
	require.Contains(t, merged.Resolvers()["TypeA"], "fieldA")
	require.Contains(t, merged.Resolvers()["TypeA"], "fieldB")
}

func TestMergeFieldConflict(t *testing.T) {
	t.Parallel()
	_, err := mergeSchemaResolvers(
		StaticSchema(StaticSchemaParams{
			Schema: `
			type TypeA {
				fieldA: String
			}
			`,
			Resolvers: Resolvers{
				"TypeA": ObjectResolver{
					"fieldA": nil,
				},
			},
		}),

		StaticSchema(StaticSchemaParams{
			Schema: `
			extend TypeA {
				fieldA: String
			}
			`,
			Resolvers: Resolvers{
				"TypeA": ObjectResolver{
					"fieldA": nil,
				},
			},
		}),
	)
	require.ErrorIs(t, err, ErrMergeFieldConflict)
}

func TestMergeTypeConflict(t *testing.T) {
	_, err := mergeSchemaResolvers(
		StaticSchema(StaticSchemaParams{
			Schema: `
			type TypeA {
				fieldA: String
			}
			`,
			Resolvers: Resolvers{
				"TypeA": ObjectResolver{
					"fieldA": nil,
				},
			},
		}),

		StaticSchema(StaticSchemaParams{
			Schema: `
			scalar TypeA
			`,
			Resolvers: Resolvers{
				"TypeA": ScalarResolver{},
			},
		}),
	)
	require.ErrorIs(t, err, ErrMergeTypeConflict)
}

func TestMergeScalars(t *testing.T) {
	t.Parallel()
	merged, err := mergeSchemaResolvers(
		StaticSchema(StaticSchemaParams{
			Schema: `
			type Query {
				typeA: TypeA
			}

			scalar TypeA
			`,
			Resolvers: Resolvers{
				"TypeA": stringResolver[string](),
			},
		}),

		StaticSchema(StaticSchemaParams{
			Schema: `
			extend type Query {
				typeB: TypeB
			}

			scalar TypeB
			`,
			Resolvers: Resolvers{
				"TypeB": jsonResolver,
			},
		}),
	)
	require.NoError(t, err)

	require.Contains(t, merged.Resolvers(), "TypeA")
	require.IsType(t, merged.Resolvers()["TypeA"], ScalarResolver{})

	require.Contains(t, merged.Resolvers(), "TypeB")
	require.IsType(t, merged.Resolvers()["TypeB"], ScalarResolver{})
}

func TestMergeScalarConflict(t *testing.T) {
	t.Parallel()
	_, err := mergeSchemaResolvers(
		StaticSchema(StaticSchemaParams{
			Schema: `scalar TypeA`,
			Resolvers: Resolvers{
				"TypeA": ScalarResolver{},
			},
		}),

		StaticSchema(StaticSchemaParams{
			Schema: `scalar TypeA`,
			Resolvers: Resolvers{
				"TypeA": ScalarResolver{},
			},
		}),
	)
	require.ErrorIs(t, err, ErrMergeScalarConflict)
}

func TestWithMountedCacheSeen(t *testing.T) {
	t.Parallel()

	cs := &containerSchema{
		APIServer: &APIServer{bk: &buildkit.Client{}},
	}

	cid, err := core.NewCache("test-seen").ID()
	require.NoError(t, err)

	_, err = cs.withMountedCache(
		context.Background(),
		&core.Container{},
		containerWithMountedCacheArgs{Path: "/foo", Cache: cid},
	)
	require.NoError(t, err)

	_, ok := core.SeenCacheKeys.Load("test-seen")
	require.True(t, ok)
}

func TestNamespaceObjects(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		testCase  string
		namespace string
		obj       string
		result    string
	}{
		{
			testCase:  "namespace",
			namespace: "Foo",
			obj:       "Bar",
			result:    "FooBar",
		},
		{
			testCase:  "namespace into camel case",
			namespace: "foo",
			obj:       "bar-baz",
			result:    "FooBarBaz",
		},
		{
			testCase:  "don't namespace when equal",
			namespace: "foo",
			obj:       "Foo",
			result:    "Foo",
		},
		{
			testCase:  "don't namespace when prefixed",
			namespace: "foo",
			obj:       "FooBar",
			result:    "FooBar",
		},
		{
			testCase:  "still namespace when prefixed if not full",
			namespace: "foo",
			obj:       "Foobar",
			result:    "FooFoobar",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.testCase, func(t *testing.T) {
			result := namespaceObject(tc.obj, tc.namespace)
			require.Equal(t, tc.result, result)
		})
	}
}

func TestCoreModTypeDefs(t *testing.T) {
	ctx := context.Background()
	api, err := New(ctx, InitializeArgs{})
	require.NoError(t, err)
	require.NotNil(t, api.core)

	typeDefs, err := api.core.TypeDefs(ctx)
	require.NoError(t, err)

	objByName := make(map[string]*core.TypeDef)
	for _, typeDef := range typeDefs {
		require.Equal(t, core.TypeDefKindObject, typeDef.Kind)
		objByName[typeDef.AsObject.Name] = typeDef
	}

	// just verify some subset of objects+functions as a sanity check

	// Container
	ctrTypeDef, ok := objByName["Container"]
	require.True(t, ok)
	ctrObj := ctrTypeDef.AsObject

	_, ok = ctrObj.FunctionByName("id")
	require.False(t, ok)

	fileFn, ok := ctrObj.FunctionByName("file")
	require.True(t, ok)
	require.Equal(t, core.TypeDefKindObject, fileFn.ReturnType.Kind)
	require.Equal(t, "File", fileFn.ReturnType.AsObject.Name)
	require.Len(t, fileFn.Args, 1)

	fileFnPathArg := fileFn.Args[0]
	require.Equal(t, "path", fileFnPathArg.Name)
	require.Equal(t, core.TypeDefKindString, fileFnPathArg.TypeDef.Kind)
	require.False(t, fileFnPathArg.TypeDef.Optional)

	withMountedDirectoryFn, ok := ctrObj.FunctionByName("withMountedDirectory")
	require.True(t, ok)

	withMountedDirectoryFnPathArg := withMountedDirectoryFn.Args[0]
	require.Equal(t, "path", withMountedDirectoryFnPathArg.Name)
	require.Equal(t, core.TypeDefKindString, withMountedDirectoryFnPathArg.TypeDef.Kind)
	require.False(t, withMountedDirectoryFnPathArg.TypeDef.Optional)

	withMountedDirectoryFnSourceArg := withMountedDirectoryFn.Args[1]
	require.Equal(t, "source", withMountedDirectoryFnSourceArg.Name)
	require.Equal(t, core.TypeDefKindObject, withMountedDirectoryFnSourceArg.TypeDef.Kind)
	require.Equal(t, "Directory", withMountedDirectoryFnSourceArg.TypeDef.AsObject.Name)
	require.False(t, withMountedDirectoryFnSourceArg.TypeDef.Optional)

	withMountedDirectoryFnOwnerArg := withMountedDirectoryFn.Args[2]
	require.Equal(t, "owner", withMountedDirectoryFnOwnerArg.Name)
	require.Equal(t, core.TypeDefKindString, withMountedDirectoryFnOwnerArg.TypeDef.Kind)
	require.True(t, withMountedDirectoryFnOwnerArg.TypeDef.Optional)

	// File
	fileTypeDef, ok := objByName["File"]
	require.True(t, ok)
	fileObj := fileTypeDef.AsObject

	_, ok = fileObj.FunctionByName("id")
	require.False(t, ok)

	exportFn, ok := fileObj.FunctionByName("export")
	require.True(t, ok)
	require.Equal(t, core.TypeDefKindBoolean, exportFn.ReturnType.Kind)
	require.Len(t, exportFn.Args, 2)

	exportFnPathArg := exportFn.Args[0]
	require.Equal(t, "path", exportFnPathArg.Name)
	require.Equal(t, core.TypeDefKindString, exportFnPathArg.TypeDef.Kind)
	require.False(t, exportFnPathArg.TypeDef.Optional)

	exportFnAllowParentDirPathArg := exportFn.Args[1]
	require.Equal(t, "allowParentDirPath", exportFnAllowParentDirPathArg.Name)
	require.Equal(t, core.TypeDefKindBoolean, exportFnAllowParentDirPathArg.TypeDef.Kind)
	require.True(t, exportFnAllowParentDirPathArg.TypeDef.Optional)
}
