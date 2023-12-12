package dagql_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql"
	"github.com/vito/dagql/idproto"
	"github.com/vito/dagql/internal/points"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

type Query struct {
}

func (Query) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Query",
		NonNull:   true,
	}
}

func TestBasic(t *testing.T) {
	srv := dagql.NewServer(Query{})

	points.Install[Query](srv)

	gql := client.New(handler.NewDefaultServer(srv))

	var res struct {
		Point struct {
			X         int
			Y         int
			ShiftLeft struct {
				Id        string
				Ecks      int
				Why       int
				Neighbors []struct {
					Id string
					X  int
					Y  int
				}
			}
		}
	}
	gql.MustPost(`query {
		point(x: 6, y: 7) {
			x
			y
			shiftLeft {
				id
				ecks: x
				why: y
				neighbors {
					id
					x
					y
				}
			}
		}
	}`, &res)
	expectedID := idproto.New("Point")
	expectedID.Append("point", idproto.Arg("x", 6), idproto.Arg("y", 7))
	expectedID.Append("shiftLeft")
	expectedEnc, err := dagql.ID[points.Point]{ID: expectedID}.Encode()
	assert.NilError(t, err)
	assert.Equal(t, 6, res.Point.X)
	assert.Equal(t, 7, res.Point.Y)
	assert.Equal(t, 5, res.Point.ShiftLeft.Ecks)
	assert.Equal(t, 7, res.Point.ShiftLeft.Why)
	assert.Equal(t, expectedEnc, res.Point.ShiftLeft.Id)
	// assert.Equal(t, 4, res.Point.ShiftLeft.Neighbors[0].Id)
	assert.Assert(t, cmp.Len(res.Point.ShiftLeft.Neighbors, 4))
	assert.Equal(t, 4, res.Point.ShiftLeft.Neighbors[0].X)
	assert.Equal(t, 7, res.Point.ShiftLeft.Neighbors[0].Y)
	// assert.Equal(t, 4, res.Point.ShiftLeft.Neighbors[1].Id)
	assert.Equal(t, 6, res.Point.ShiftLeft.Neighbors[1].X)
	assert.Equal(t, 7, res.Point.ShiftLeft.Neighbors[1].Y)
	// assert.Equal(t, 4, res.Point.ShiftLeft.Neighbors[2].Id)
	assert.Equal(t, 5, res.Point.ShiftLeft.Neighbors[2].X)
	assert.Equal(t, 6, res.Point.ShiftLeft.Neighbors[2].Y)
	// assert.Equal(t, 4, res.Point.ShiftLeft.Neighbors[3].Id)
	assert.Equal(t, 5, res.Point.ShiftLeft.Neighbors[3].X)
	assert.Equal(t, 8, res.Point.ShiftLeft.Neighbors[3].Y)
}

func TestLoadingByID(t *testing.T) {
	srv := dagql.NewServer(Query{})

	points.Install[Query](srv)

	gql := client.New(handler.NewDefaultServer(srv))

	var res struct {
		Point struct {
			X         int
			Y         int
			ShiftLeft struct {
				Id        string
				Ecks      int
				Why       int
				Neighbors []struct {
					Id string
					X  int
					Y  int
				}
			}
		}
	}
	gql.MustPost(`query {
		point(x: 6, y: 7) {
			x
			y
			shiftLeft {
				id
				ecks: x
				why: y
				neighbors {
					id
					x
					y
				}
			}
		}
	}`, &res)
	for i, neighbor := range res.Point.ShiftLeft.Neighbors {
		var res struct {
			LoadPointFromID struct {
				Id string
				X  int
				Y  int
			}
		}
		gql.MustPost(`query {
			loadPointFromID(id: "`+neighbor.Id+`") {
				id
				x
				y
			}
		}`, &res)
		assert.Equal(t, neighbor.Id, res.LoadPointFromID.Id)
		assert.Equal(t, neighbor.X, res.LoadPointFromID.X)
		assert.Equal(t, neighbor.Y, res.LoadPointFromID.Y)
		switch i {
		case 0:
			assert.Equal(t, res.LoadPointFromID.X, 4)
			assert.Equal(t, res.LoadPointFromID.Y, 7)
		case 1:
			assert.Equal(t, res.LoadPointFromID.X, 6)
			assert.Equal(t, res.LoadPointFromID.Y, 7)
		case 2:
			assert.Equal(t, res.LoadPointFromID.X, 5)
			assert.Equal(t, res.LoadPointFromID.Y, 6)
		case 3:
			assert.Equal(t, res.LoadPointFromID.X, 5)
			assert.Equal(t, res.LoadPointFromID.Y, 8)
		}
	}
}

func TestIDsReflectQuery(t *testing.T) {
	srv := dagql.NewServer(Query{})
	points.Install[Query](srv)

	gql := client.New(handler.NewDefaultServer(srv))

	var res struct {
		Point struct {
			ShiftLeft struct {
				Id        string
				Neighbors []struct {
					Id string
				}
			}
		}
	}
	gql.MustPost(`query {
		point(x: 6, y: 7) {
			shiftLeft {
				id
				neighbors {
					id
				}
			}
		}
	}`, &res)

	expectedID := idproto.New("Point")
	expectedID.Append("point", idproto.Arg("x", 6), idproto.Arg("y", 7))
	expectedID.Append("shiftLeft")
	expectedEnc, err := dagql.ID[points.Point]{ID: expectedID}.Encode()
	assert.NilError(t, err)
	assert.Equal(t, expectedEnc, res.Point.ShiftLeft.Id)

	assert.Assert(t, cmp.Len(res.Point.ShiftLeft.Neighbors, 4))
	for i, neighbor := range res.Point.ShiftLeft.Neighbors {
		var res struct {
			LoadPointFromID struct {
				Id string
				X  int
				Y  int
			}
		}
		gql.MustPost(`query {
			loadPointFromID(id: "`+neighbor.Id+`") {
				id
				x
				y
			}
		}`, &res)
		assert.Equal(t, neighbor.Id, res.LoadPointFromID.Id)
		switch i {
		case 0:
			assert.Equal(t, res.LoadPointFromID.X, 4)
			assert.Equal(t, res.LoadPointFromID.Y, 7)
		case 1:
			assert.Equal(t, res.LoadPointFromID.X, 6)
			assert.Equal(t, res.LoadPointFromID.Y, 7)
		case 2:
			assert.Equal(t, res.LoadPointFromID.X, 5)
			assert.Equal(t, res.LoadPointFromID.Y, 6)
		case 3:
			assert.Equal(t, res.LoadPointFromID.X, 5)
			assert.Equal(t, res.LoadPointFromID.Y, 8)
		}
	}
}

func TestPassingObjectsAround(t *testing.T) {
	srv := dagql.NewServer(Query{})
	points.Install[Query](srv)

	gql := client.New(handler.NewDefaultServer(srv))

	var res struct {
		Point struct {
			Id string
		}
	}
	gql.MustPost(`query {
		point(x: 6, y: 7) {
			id
		}
	}`, &res)

	id67 := res.Point.Id

	var res2 struct {
		Point struct {
			Line struct {
				Length int
			}
		}
	}
	gql.MustPost(`query {
		point(x: -6, y: -7) {
			line(to: "`+id67+`") {
				length
			}
		}
	}`, &res2)
	assert.Equal(t, res2.Point.Line.Length, 18)
}

func TestEnums(t *testing.T) {
	srv := dagql.NewServer(Query{})
	points.Install[Query](srv)

	gql := client.New(handler.NewDefaultServer(srv))

	t.Run("outputs", func(t *testing.T) {
		var res struct {
			Point struct {
				Id string
			}
		}
		gql.MustPost(`query {
			point(x: 6, y: 7) {
				id
			}
		}`, &res)

		id67 := res.Point.Id

		var res2 struct {
			Point struct {
				Line struct {
					Direction string
				}
			}
		}
		gql.MustPost(`query {
			point(x: -6, y: -7) {
				line(to: "`+id67+`") {
					direction
				}
			}
		}`, &res2)
		assert.Equal(t, res2.Point.Line.Direction, "RIGHT")
	})

	t.Run("inputs", func(t *testing.T) {
		var res struct {
			Point struct {
				Inert points.Point
				Up    points.Point
				Down  points.Point
				Left  points.Point
				Right points.Point
			}
		}
		gql.MustPost(`query {
			point(x: 6, y: 7) {
				inert: shift(direction: INERT) {
					x
					y
				}
				up: shift(direction: UP) {
					x
					y
				}
				down: shift(direction: DOWN) {
					x
					y
				}
				left: shift(direction: LEFT) {
					x
					y
				}
				right: shift(direction: RIGHT) {
					x
					y
				}
			}
		}`, &res)
		assert.Equal(t, res.Point.Inert.X, 6)
		assert.Equal(t, res.Point.Inert.Y, 7)
		assert.Equal(t, res.Point.Up.X, 6)
		assert.Equal(t, res.Point.Up.Y, 8)
		assert.Equal(t, res.Point.Down.X, 6)
		assert.Equal(t, res.Point.Down.Y, 6)
		assert.Equal(t, res.Point.Left.X, 5)
		assert.Equal(t, res.Point.Left.Y, 7)
		assert.Equal(t, res.Point.Right.X, 7)
		assert.Equal(t, res.Point.Right.Y, 7)
	})

	t.Run("invalid inputs", func(t *testing.T) {
		var res struct {
			Point struct {
				Inert points.Point
			}
		}
		err := gql.Post(`query {
			point(x: 6, y: 7) {
				shift(direction: BOGUS) {
					x
					y
				}
			}
		}`, &res)
		assert.ErrorContains(t, err, "BOGUS")
	})

	t.Run("invalid defaults", func(t *testing.T) {
		dagql.Fields[points.Point]{
			"badShift": dagql.Func(func(ctx context.Context, self points.Point, args struct {
				Direction points.Direction `default:"BOGUS"`
				Amount    dagql.Int        `default:"1"`
			}) (points.Point, error) {
				return points.Point{}, fmt.Errorf("should not be called")
			}),
		}.Install(srv)
		var res struct {
			Point struct {
				Inert points.Point
			}
		}
		err := gql.Post(`query {
			point(x: 6, y: 7) {
				badShift {
					x
					y
				}
			}
		}`, &res)
		assert.ErrorContains(t, err, "BOGUS")
	})
}

type Defaults struct {
	Boolean bool
	Int     int
	String  string
	Float   float64
}

func (Defaults) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Defaults",
		NonNull:   true,
	}
}

func TestDefaults(t *testing.T) {
	srv := dagql.NewServer(Query{})
	points.Install[Query](srv)

	gql := client.New(handler.NewDefaultServer(srv))

	dagql.Fields[Defaults]{
		"boolean": dagql.Func(func(ctx context.Context, self Defaults, _ any) (dagql.Boolean, error) {
			return dagql.NewBoolean(self.Boolean), nil
		}),
		"int": dagql.Func(func(ctx context.Context, self Defaults, _ any) (dagql.Int, error) {
			return dagql.NewInt(self.Int), nil
		}),
		"string": dagql.Func(func(ctx context.Context, self Defaults, _ any) (dagql.String, error) {
			return dagql.NewString(self.String), nil
		}),
		"float": dagql.Func(func(ctx context.Context, self Defaults, _ any) (dagql.Float, error) {
			return dagql.NewFloat(self.Float), nil
		}),
	}.Install(srv)

	t.Run("invalid defaults", func(t *testing.T) {
		dagql.Fields[Query]{
			"defaults": dagql.Func(func(ctx context.Context, self Query, args struct {
				Boolean dagql.Boolean `default:"true"`
				Int     dagql.Int     `default:"42"`
				String  dagql.String  `default:"hello, world!"`
				Float   dagql.Float   `default:"3.14"`
			}) (Defaults, error) {
				return Defaults{
					Boolean: args.Boolean.Value,
					Int:     args.Int.Value,
					String:  args.String.Value,
					Float:   args.Float.Value,
				}, nil
			}),
		}.Install(srv)

		var res struct {
			Defaults
		}
		gql.MustPost(`query {
			defaults {
				boolean
				int
				string
				float
			}
		}`, &res)
		assert.Equal(t, true, res.Boolean)
		assert.Equal(t, 42, res.Int)
		assert.Equal(t, "hello, world!", res.String)
		assert.Equal(t, 3.14, res.Float)
	})
}
