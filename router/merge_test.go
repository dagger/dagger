package router

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeObjects(t *testing.T) {
	merged, err := Merge("",
		&staticSchema{
			schema: `
			type TypeA {
				fieldA: String
			}
			`,
			operations: `
			query QueryA {}
			`,
			resolvers: Resolvers{
				"TypeA": ObjectResolver{
					"fieldA": nil,
				},
			},
		},

		&staticSchema{
			schema: `
			type TypeB {
				fieldB: String
			}
			`,
			operations: `
			query QueryB {}
			`,
			resolvers: Resolvers{
				"TypeB": ObjectResolver{
					"fieldB": nil,
				},
			},
		},
	)
	require.NoError(t, err)

	require.Contains(t, merged.Schema(), "TypeA")
	require.Contains(t, merged.Schema(), "TypeB")

	require.Contains(t, merged.Operations(), "QueryA")
	require.Contains(t, merged.Operations(), "QueryB")

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
	merged, err := Merge("",
		&staticSchema{
			schema: `
			type TypeA {
				fieldA: String
			}
			`,
			operations: ``,
			resolvers: Resolvers{
				"TypeA": ObjectResolver{
					"fieldA": nil,
				},
			},
		},

		&staticSchema{
			schema: `
			extend Type A {
				fieldB: String
			}
			`,
			operations: ``,
			resolvers: Resolvers{
				"TypeA": ObjectResolver{
					"fieldB": nil,
				},
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, merged)

	require.Contains(t, merged.Resolvers(), "TypeA")
	require.IsType(t, merged.Resolvers()["TypeA"], ObjectResolver{})
	require.Contains(t, merged.Resolvers()["TypeA"], "fieldA")
	require.Contains(t, merged.Resolvers()["TypeA"], "fieldB")
}

func TestMergeFieldConflict(t *testing.T) {
	_, err := Merge("",
		&staticSchema{
			schema: `
			type TypeA {
				fieldA: String
			}
			`,
			operations: ``,
			resolvers: Resolvers{
				"TypeA": ObjectResolver{
					"fieldA": nil,
				},
			},
		},

		&staticSchema{
			schema: `
			extend TypeA {
				fieldA: String
			}
			`,
			operations: ``,
			resolvers: Resolvers{
				"TypeA": ObjectResolver{
					"fieldA": nil,
				},
			},
		},
	)
	require.ErrorIs(t, err, ErrMergeFieldConflict)
}

func TestMergeTypeConflict(t *testing.T) {
	_, err := Merge("",
		&staticSchema{
			schema: `
			type TypeA {
				fieldA: String
			}
			`,
			resolvers: Resolvers{
				"TypeA": ObjectResolver{
					"fieldA": nil,
				},
			},
		},

		&staticSchema{
			schema: `
			scalar TypeA
			`,
			resolvers: Resolvers{
				"TypeA": ScalarResolver{},
			},
		},
	)
	require.ErrorIs(t, err, ErrMergeTypeConflict)
}

func TestMergeScalars(t *testing.T) {
	merged, err := Merge("",
		&staticSchema{
			schema: `
			scalar TypeA
			`,
			resolvers: Resolvers{
				"TypeA": ScalarResolver{},
			},
		},

		&staticSchema{
			schema: `
			scalar TypeB
			`,
			resolvers: Resolvers{
				"TypeB": ScalarResolver{},
			},
		},
	)
	require.NoError(t, err)

	require.Contains(t, merged.Resolvers(), "TypeA")
	require.IsType(t, merged.Resolvers()["TypeA"], ScalarResolver{})

	require.Contains(t, merged.Resolvers(), "TypeB")
	require.IsType(t, merged.Resolvers()["TypeB"], ScalarResolver{})
}

func TestMergeScalarConflict(t *testing.T) {
	_, err := Merge("",
		&staticSchema{
			schema: `scalar TypeA`,
			resolvers: Resolvers{
				"TypeA": ScalarResolver{},
			},
		},

		&staticSchema{
			schema: `scalar TypeA`,
			resolvers: Resolvers{
				"TypeA": ScalarResolver{},
			},
		},
	)
	require.ErrorIs(t, err, ErrMergeScalarConflict)
}
