package dagql

import (
	"context"
	"testing"

	"github.com/dagger/dagql/idproto"
	"github.com/stretchr/testify/require"
)

func TestBasic(t *testing.T) {
	ctx := context.Background()

	type Point struct {
		X int
		Y int
	}

	queryFields := map[string]Field[*Query]{
		"point": {
			Spec: FieldSpec{
				Name: "point",
				Args: []ArgSpec{
					{"x", Type{Named: "Int"}},
					{"y", Type{Named: "Int"}},
				},
				Type: Type{
					Named: "Point",
				},
			},
			Func: func(ctx context.Context, self *Query, args map[string]Literal) (any, error) {
				return &Point{
					X: int(args["x"].GetInt()),
					Y: int(args["y"].GetInt()),
				}, nil
			},
		},
	}

	pointFields := map[string]Field[*Point]{
		"x": {
			Spec: FieldSpec{
				Name: "x",
				Type: Type{
					Named: "Int",
				},
			},
			Func: func(ctx context.Context, self *Point, args map[string]Literal) (any, error) {
				return self.X, nil
			},
		},
		"y": {
			Spec: FieldSpec{
				Name: "y",
				Type: Type{
					Named: "Int",
				},
			},
			Func: func(ctx context.Context, self *Point, args map[string]Literal) (any, error) {
				return self.Y, nil
			},
		},
	}

	srv := Server{
		Resolvers: map[string]func(*idproto.ID, any) (Node, error){
			"Query": func(id *idproto.ID, val any) (Node, error) {
				return ObjectResolver[*Query]{
					Constructor: id,
					Self:        val.(*Query),
					Fields:      queryFields,
				}, nil
			},
			"Point": func(id *idproto.ID, val any) (Node, error) {
				return ObjectResolver[*Point]{
					Constructor: id,
					Self:        val.(*Point),
					Fields:      pointFields,
				}, nil
			},
		},
	}

	res, err := srv.Resolve(ctx, ObjectResolver[*Query]{
		Constructor: idproto.New("Query"),
		Self:        &Query{},
		Fields:      queryFields,
	}, Query{
		Selections: []Selection{
			{
				Selector: Selector{
					Field: "point",
					Args: map[string]Literal{
						"x": {idproto.LiteralValue(1)},
						"y": {idproto.LiteralValue(2)},
					},
				},
				Subselections: []Selection{
					{
						Alias: "ecks",
						Selector: Selector{
							Field: "x",
						},
					},
					{
						Alias: "why",
						Selector: Selector{
							Field: "y",
						},
					},
				},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"point": map[string]any{
			"ecks": 1,
			"why":  2,
		},
	}, res)
}
