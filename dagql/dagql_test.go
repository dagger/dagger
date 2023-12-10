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

	dagql.Install(srv, dagql.ObjectFields[Query]{
		"point": dagql.Func(func(ctx context.Context, self Query, args struct {
			X dagql.Int `default:"0"`
			Y dagql.Int `default:"0"`
		}) (Point, error) {
			return Point{
				X: int(args.X.Value),
				Y: int(args.Y.Value),
			}, nil
		}),
	})

	// TODO: error handling would be nice.
	//
	// maybe dagql.Install() should just take a type and go over its public methods?

	dagql.Install(srv, dagql.ObjectFields[Point]{
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
	})

	gql := client.New(handler.NewDefaultServer(srv))

	t.Run("aliases", func(t *testing.T) {
		var res struct {
			Point struct {
				ShiftLeft struct {
					Ecks int
					Why  int
				}
			}
		}
		gql.MustPost(`query {
			point(x: 6, y: 7) {
				shiftLeft(amount: 2) {
					ecks: x
					why: y
				}
			}
		}`, &res)
		assert.Equal(t, 4, res.Point.ShiftLeft.Ecks)
		assert.Equal(t, 7, res.Point.ShiftLeft.Why)
	})

	t.Run("IDs", func(t *testing.T) {
		var res struct {
			Point struct {
				ShiftLeft struct {
					Id string
				}
			}
		}
		gql.MustPost(`query {
			point(x: 6, y: 7) {
				shiftLeft(amount: 2) {
					id
				}
			}
		}`, &res)
		expectedID := idproto.New("Point")
		expectedID.Append("point", idproto.Arg("x", 6), idproto.Arg("y", 7))
		expectedID.Append("shiftLeft", idproto.Arg("amount", 2))
		expectedEnc, err := dagql.ID[Point]{ID: expectedID}.Encode()
		assert.NilError(t, err)
		assert.Equal(t, expectedEnc, res.Point.ShiftLeft.Id)
	})
}
