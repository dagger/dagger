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
	merged, err := mergeExecutableSchemas(
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
	merged, err := mergeExecutableSchemas(
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
	_, err := mergeExecutableSchemas(
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
	_, err := mergeExecutableSchemas(
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
	merged, err := mergeExecutableSchemas(
		StaticSchema(StaticSchemaParams{
			Schema: `
			scalar TypeA
			`,
			Resolvers: Resolvers{
				"TypeA": ScalarResolver{},
			},
		}),

		StaticSchema(StaticSchemaParams{
			Schema: `
			scalar TypeB
			`,
			Resolvers: Resolvers{
				"TypeB": ScalarResolver{},
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
	_, err := mergeExecutableSchemas(
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
