package dagql_test

import (
	"context"
	"testing"

	"github.com/dagger/dagql"
	"github.com/dagger/dagql/idproto"
	"google.golang.org/protobuf/testing/protocmp"
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
	ctx := context.Background()

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

	res, err := srv.Resolve(ctx, srv.Root, dagql.Query{
		Selections: []dagql.Selection{
			{
				Selector: dagql.Selector{
					Field: "point",
					Args: map[string]dagql.Literal{
						"x": {idproto.LiteralValue(6)},
						"y": {idproto.LiteralValue(7)},
					},
				},
				Subselections: []dagql.Selection{
					{
						Selector: dagql.Selector{
							Field: "shiftLeft",
							Args: map[string]dagql.Literal{
								"amount": {idproto.LiteralValue(2)},
							},
						},
						Subselections: []dagql.Selection{
							{
								Alias: "ecks",
								Selector: dagql.Selector{
									Field: "x",
								},
							},
							{
								Alias: "why",
								Selector: dagql.Selector{
									Field: "y",
								},
							},
						},
					},
				},
			},
		},
	})
	assert.NilError(t, err)
	assert.DeepEqual(t, map[string]any{
		"point": map[string]any{
			"shiftLeft": map[string]any{
				"ecks": dagql.Int{Value: 4},
				"why":  dagql.Int{Value: 7},
			},
		},
	}, res)

	res, err = srv.Resolve(ctx, srv.Root, dagql.Query{
		Selections: []dagql.Selection{
			{
				Selector: dagql.Selector{
					Field: "point",
					Args: map[string]dagql.Literal{
						"x": {idproto.LiteralValue(6)},
						"y": {idproto.LiteralValue(7)},
					},
				},
				Subselections: []dagql.Selection{
					{
						Selector: dagql.Selector{
							Field: "shiftLeft",
							Args: map[string]dagql.Literal{
								"amount": {idproto.LiteralValue(2)},
							},
						},
						Subselections: []dagql.Selection{
							{
								Selector: dagql.Selector{
									Field: "id",
								},
							},
						},
					},
				},
			},
		},
	})

	expectedID := idproto.New("Point")
	expectedID.Append("point", idproto.Arg("x", 6), idproto.Arg("y", 7))
	expectedID.Append("shiftLeft", idproto.Arg("amount", 2))

	assert.NilError(t, err)
	assert.DeepEqual(t, map[string]any{
		"point": map[string]any{
			"shiftLeft": map[string]any{
				"id": expectedID,
			},
		},
	}, res, protocmp.Transform())
}
