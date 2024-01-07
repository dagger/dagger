package points

import (
	"context"
	"math"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql"
	"github.com/vito/dagql/idproto"
)

type Point struct {
	X int `field:"true"`
	Y int `field:"true"`
}

func (*Point) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Point",
		NonNull:   true,
	}
}

func (*Point) TypeDescription() string {
	return "A point in 2D space."
}

type Line struct {
	From *Point
	To   *Point
}

func (*Line) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Line",
		NonNull:   true,
	}
}

func (*Line) TypeDescription() string {
	return "A line connecting two points."
}

type Direction string

var Directions = dagql.NewEnum[Direction]()

var (
	DirectionUp    = Directions.Register("UP", "Upward.")
	DirectionDown  = Directions.Register("DOWN", "Downward.")
	DirectionLeft  = Directions.Register("LEFT", "Leftward.")
	DirectionRight = Directions.Register("RIGHT", "Rightward.")
	DirectionInert = Directions.Register("INERT", "No direction.")
)

var _ dagql.Input = Direction("")

func (Direction) Decoder() dagql.InputDecoder {
	return Directions
}

func (d Direction) ToLiteral() *idproto.Literal {
	return Directions.Literal(d)
}

func (Direction) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Direction",
		NonNull:   true,
	}
}

func (Direction) TypeDescription() string {
	return "A direction relative to an initial position."
}

func Install[R dagql.Typed](srv *dagql.Server) {
	dagql.Fields[R]{
		dagql.Func("point", func(ctx context.Context, self R, args struct {
			X dagql.Int `default:"0"`
			Y dagql.Int `default:"0"`
		}) (*Point, error) {
			return &Point{
				X: int(args.X.Int()),
				Y: int(args.Y.Int()),
			}, nil
		}),
	}.Install(srv)

	dagql.Fields[*Point]{
		dagql.Func("self", func(ctx context.Context, self *Point, _ struct{}) (*Point, error) {
			return self, nil
		}),
		dagql.Func("shiftLeft", func(ctx context.Context, self *Point, args struct {
			Amount dagql.Int `default:"1"`
		}) (*Point, error) {
			shifted := *self
			shifted.X -= args.Amount.Int()
			return &shifted, nil
		}), // TODO @deprecate
		dagql.Func("shift", func(ctx context.Context, self *Point, args struct {
			Direction Direction
			Amount    dagql.Int `default:"1"`
		}) (*Point, error) {
			shifted := *self
			switch args.Direction {
			case DirectionUp:
				shifted.Y += args.Amount.Int()
			case DirectionDown:
				shifted.Y -= args.Amount.Int()
			case DirectionLeft:
				shifted.X -= args.Amount.Int()
			case DirectionRight:
				shifted.X += args.Amount.Int()
			}
			return &shifted, nil
		}),
		dagql.Func("neighbors", func(ctx context.Context, self *Point, _ struct{}) (dagql.Array[*Point], error) {
			return []*Point{
				{X: self.X - 1, Y: self.Y},
				{X: self.X + 1, Y: self.Y},
				{X: self.X, Y: self.Y - 1},
				{X: self.X, Y: self.Y + 1},
			}, nil
		}),
		dagql.Func("line", func(ctx context.Context, self *Point, args struct {
			To dagql.ID[*Point]
		}) (*Line, error) {
			to, err := args.To.Load(ctx, srv)
			if err != nil {
				return nil, err
			}
			return &Line{self, to.Self}, nil
		}),
	}.Install(srv)

	Directions.Install(srv)

	dagql.Fields[*Line]{
		dagql.Func("length", func(ctx context.Context, self *Line, _ struct{}) (dagql.Float, error) {
			// well this got more complicated than I planned
			// √((x2 - x1)2 + (y2 - y1)2)
			return dagql.NewFloat(
				math.Sqrt(
					math.Pow(float64(self.To.X-self.From.X), 2) +
						math.Pow(float64(self.To.Y-self.From.Y), 2)),
			), nil
		}),
		dagql.Func("direction", func(ctx context.Context, self *Line, _ struct{}) (Direction, error) {
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
