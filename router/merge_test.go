package router

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeObjects(t *testing.T) {
	merged, err := MergeExecutableSchemas("",
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
	merged, err := MergeExecutableSchemas("",
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
			extend Type A {
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
	_, err := MergeExecutableSchemas("",
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
	_, err := MergeExecutableSchemas("",
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
	merged, err := MergeExecutableSchemas("",
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
	_, err := MergeExecutableSchemas("",
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
