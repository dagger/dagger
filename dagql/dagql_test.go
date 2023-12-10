package dagql_test

import (
	"context"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/dagger/dagql"
	"github.com/dagger/dagql/idproto"
	"gotest.tools/v3/assert"
)

type Point struct {
	X int
	Y int
}

func (Point) TypeName() string {
	return "Point"
}

type Query struct {
}

func (Query) TypeName() string {
	return "Query"
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
	}.Install(srv)

	dagql.Fields[Point]{
		"x": dagql.Func(func(ctx context.Context, self Point, _ any) (dagql.Int, error) {
			return dagql.Int{self.X}, nil
		}),
		"y": dagql.Func(func(ctx context.Context, self Point, _ any) (dagql.Int, error) {
			return dagql.Int{self.Y}, nil
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
	}.Install(srv)

	gql := client.New(handler.NewDefaultServer(srv))

	var res struct {
		Point struct {
			ShiftLeft struct {
				Id   string
				Ecks int
				Why  int
			}
		}
	}
	gql.MustPost(`query {
		point(x: 6, y: 7) {
			shiftLeft {
				id
				ecks: x
				why: y
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
}
