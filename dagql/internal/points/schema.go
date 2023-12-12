package points

import (
	"context"
	"math"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql"
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

type Line struct {
	From Point
	To   Point
}

func (Line) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Line",
		NonNull:   true,
	}
}

type Direction string

var Directions = dagql.EnumValues[Direction]{
	"UP",
	"DOWN",
	"LEFT",
	"RIGHT",
}

func (Direction) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Direction",
		NonNull:   true,
	}
}

func Install[R dagql.Typed](srv *dagql.Server) {
	dagql.Fields[R]{
		"point": dagql.Func(func(ctx context.Context, self R, args struct {
			X dagql.Int `default:"0"`
			Y dagql.Int `default:"0"`
		}) (Point, error) {
			return Point{
				X: int(args.X.Value),
				Y: int(args.Y.Value),
			}, nil
		}),
		"loadPointFromID": dagql.Func(func(ctx context.Context, self R, args struct {
			ID dagql.ID[Point]
		}) (dagql.Identified[Point], error) { // TODO Object instead of just Identified?
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
		"line": dagql.Func[Point](func(ctx context.Context, self Point, args struct {
			To dagql.ID[Point]
		}) (Line, error) {
			to, err := args.To.Load(ctx, srv)
			if err != nil {
				return Line{}, err
			}
			return Line{self, to.Value().(Point)}, nil // TODO
		}),
	}.Install(srv)

	Directions.Install(srv)

	dagql.Fields[Line]{
		"length": dagql.Func(func(ctx context.Context, self Line, _ any) (dagql.Float, error) {
			// well this got more complicated than I planned
			// √((x2 - x1)2 + (y2 - y1)2)
			return dagql.NewFloat(
				math.Sqrt(
					math.Pow(float64(self.To.X-self.From.X), 2) +
						math.Pow(float64(self.To.Y-self.From.Y), 2)),
			), nil
		}),
		"direction": dagql.Func(func(ctx context.Context, self Line, _ any) (Direction, error) {
			switch {
			case self.From.X < self.To.X:
				return "RIGHT", nil
			case self.From.X > self.To.X:
				return "LEFT", nil
			case self.From.Y < self.To.Y:
				return "DOWN", nil
			case self.From.Y > self.To.Y:
				return "UP", nil
			default:
				return "", nil
			}
		}),
	}.Install(srv)
}
