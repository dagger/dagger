package dagql_test

import (
	"context"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql"
	"github.com/vito/dagql/idproto"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

type Point struct {
	X int
	Y int
}

func (Point) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Point",
		NonNull:   true,
	}
}

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

	dagql.Fields[Query]{
		"point": dagql.Func(func(ctx context.Context, self Query, args struct {
			X dagql.Int `default:"0"`
			Y dagql.Int `default:"0"`
		}) (Point, error) {
			return Point{
				X: int(args.X.Value),
				Y: int(args.Y.Value),
			}, nil
		}),
		"loadPointFromID": dagql.Func(func(ctx context.Context, self Query, args struct {
			ID dagql.ID[Point]
		}) (dagql.Identified[Point], error) {
			return args.ID.Load(ctx, srv)
		}),
	}.Install(srv)

	dagql.Fields[Point]{
		"x": dagql.Func(func(ctx context.Context, self Point, _ any) (dagql.Int, error) {
			return dagql.NewInt(self.X), nil
		}),
		"y": dagql.Func(func(ctx context.Context, self Point, _ any) (dagql.Int, error) {
			return dagql.NewInt(self.Y), nil
		}),
		"self": dagql.Func(func(ctx context.Context, self Point, _ any) (Point, error) {
			return self, nil
		}),
		"shiftLeft": dagql.Func(func(ctx context.Context, self Point, args struct {
			Amount dagql.Int `default:"1"`
		}) (Point, error) {
			self.X -= args.Amount.Value
			return self, nil
		}),
		"neighbors": dagql.Func[Point](func(ctx context.Context, self Point, _ any) (dagql.Array[Point], error) {
			return []Point{
				{X: self.X - 1, Y: self.Y},
				{X: self.X + 1, Y: self.Y},
				{X: self.X, Y: self.Y - 1},
				{X: self.X, Y: self.Y + 1},
			}, nil
		}),
	}.Install(srv)

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
	expectedEnc, err := dagql.ID[Point]{ID: expectedID}.Encode()
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
