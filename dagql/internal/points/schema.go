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

type Direction struct {
	dagql.Scalar
}

var Directions = dagql.NewEnum[Direction]()

var (
	DirectionUp    = Directions.Register("UP")
	DirectionDown  = Directions.Register("DOWN")
	DirectionLeft  = Directions.Register("LEFT")
	DirectionRight = Directions.Register("RIGHT")
	DirectionInert = Directions.Register("INERT")
)

var _ dagql.Scalar = Direction{}

func (d Direction) As(value dagql.Scalar) Direction {
	d.Scalar = value
	return d
}

func (Direction) Class() dagql.ScalarClass {
	return Directions
}

func (Direction) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Direction",
		NonNull:   true,
	}
}

func Install[R dagql.Typed](srv *dagql.Server) {
	dagql.Fields[R]{
		dagql.Func("point", func(ctx context.Context, self R, args struct {
			X dagql.Int `default:"0"`
			Y dagql.Int `default:"0"`
		}) (Point, error) {
			return Point{
				X: int(args.X.Value),
				Y: int(args.Y.Value),
			}, nil
		}),
		dagql.Func("loadPointFromID", func(ctx context.Context, self R, args struct {
			ID dagql.ID[Point]
		}) (dagql.Object[Point], error) {
			return args.ID.Load(ctx, srv)
		}),
	}.Install(srv)

	dagql.Fields[Point]{
		dagql.Func("x", func(ctx context.Context, self Point, _ any) (dagql.Int, error) {
			return dagql.NewInt(self.X), nil
		}),
		dagql.Func("y", func(ctx context.Context, self Point, _ any) (dagql.Int, error) {
			return dagql.NewInt(self.Y), nil
		}),
		dagql.Func("self", func(ctx context.Context, self Point, _ any) (Point, error) {
			return self, nil
		}),
		dagql.Func("shiftLeft", func(ctx context.Context, self Point, args struct {
			Amount dagql.Int `default:"1"`
		}) (Point, error) {
			self.X -= args.Amount.Value
			return self, nil
		}), // TODO @deprecate
		dagql.Func("shift", func(ctx context.Context, self Point, args struct {
			Direction Direction
			Amount    dagql.Int `default:"1"`
		}) (Point, error) {
			switch args.Direction {
			case DirectionUp:
				self.Y += args.Amount.Value
			case DirectionDown:
				self.Y -= args.Amount.Value
			case DirectionLeft:
				self.X -= args.Amount.Value
			case DirectionRight:
				self.X += args.Amount.Value
			}
			return self, nil
		}),
		dagql.Func("neighbors", func(ctx context.Context, self Point, _ any) (dagql.Array[Point], error) {
			return []Point{
				{X: self.X - 1, Y: self.Y},
				{X: self.X + 1, Y: self.Y},
				{X: self.X, Y: self.Y - 1},
				{X: self.X, Y: self.Y + 1},
			}, nil
		}),
		dagql.Func("line", func(ctx context.Context, self Point, args struct {
			To dagql.ID[Point]
		}) (Line, error) {
			to, err := args.To.Load(ctx, srv)
			if err != nil {
				return Line{}, err
			}
			return Line{self, to.Self}, nil
		}),
	}.Install(srv)

	Directions.Install(srv)

	dagql.Fields[Line]{
		dagql.Func("length", func(ctx context.Context, self Line, _ any) (dagql.Float, error) {
			// well this got more complicated than I planned
			// √((x2 - x1)2 + (y2 - y1)2)
			return dagql.NewFloat(
				math.Sqrt(
					math.Pow(float64(self.To.X-self.From.X), 2) +
						math.Pow(float64(self.To.Y-self.From.Y), 2)),
			), nil
		}),
		dagql.Func("direction", func(ctx context.Context, self Line, _ any) (Direction, error) {
			switch {
			case self.From.X < self.To.X:
				return DirectionRight, nil
			case self.From.X > self.To.X:
				return DirectionLeft, nil
			case self.From.Y < self.To.Y:
				return DirectionDown, nil
			case self.From.Y > self.To.Y:
				return DirectionUp, nil
			default:
				return DirectionInert, nil
			}
		}),
	}.Install(srv)
}
