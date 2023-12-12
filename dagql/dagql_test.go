package dagql_test

import (
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
}
