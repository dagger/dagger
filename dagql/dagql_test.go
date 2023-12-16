package dagql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql"
	"github.com/vito/dagql/idproto"
	"github.com/vito/dagql/internal/pipes"
	"github.com/vito/dagql/internal/points"
	"github.com/vito/progrock"
	"github.com/vito/progrock/console"
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

func req(t *testing.T, gql *client.Client, query string, res any) {
	t.Helper()
	err := gql.Post(query, res)
	assert.NilError(t, err)
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
	req(t, gql, `query {
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

	expectedID := idproto.New(points.Point{}.Type())
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

func TestNullableResults(t *testing.T) {
	srv := dagql.NewServer(Query{})

	points.Install[Query](srv)

	dagql.Fields[Query]{
		dagql.Func("nullableInt", func(ctx context.Context, self Query, args struct {
			Value dagql.Optional[dagql.Int]
		}) (dagql.Optional[dagql.Int], error) {
			return args.Value, nil
		}),
		dagql.Func("nullablePoint", func(ctx context.Context, self Query, args struct {
			Point dagql.Optional[dagql.ID[points.Point]]
		}) (dagql.Nullable[points.Point], error) {
			return dagql.MapOpt(args.Point, func(id dagql.ID[points.Point]) (points.Point, error) {
				point, err := id.Load(ctx, srv)
				return point.Self, err
			})
		}),
		dagql.Func("nullableScalarArray", func(ctx context.Context, self Query, args struct {
			Array dagql.Optional[dagql.ArrayInput[dagql.Int]]
		}) (dagql.Nullable[dagql.Array[dagql.Int]], error) {
			return dagql.MapOpt(args.Array, func(id dagql.ArrayInput[dagql.Int]) (dagql.Array[dagql.Int], error) {
				return id.ToArray(), nil
			})
		}),
		dagql.Func("nullableArrayOfPoints", func(ctx context.Context, self Query, args struct {
			Array dagql.Optional[dagql.ArrayInput[dagql.ID[points.Point]]]
		}) (dagql.Nullable[dagql.Array[points.Point]], error) {
			return dagql.MapOpt(args.Array, func(id dagql.ArrayInput[dagql.ID[points.Point]]) (dagql.Array[points.Point], error) {
				return dagql.MapArrayInput(id, func(id dagql.ID[points.Point]) (points.Point, error) {
					point, err := id.Load(ctx, srv)
					return point.Self, err
				})
			})
		}),
		dagql.Func("arrayOfNullableInts", func(ctx context.Context, self Query, args struct {
			Array dagql.ArrayInput[dagql.Optional[dagql.Int]]
		}) (dagql.Array[dagql.Optional[dagql.Int]], error) {
			return args.Array.ToArray(), nil
		}),
		dagql.Func("arrayOfNullablePoints", func(ctx context.Context, self Query, args struct {
			Array dagql.ArrayInput[dagql.Optional[dagql.ID[points.Point]]]
		}) (dagql.Array[dagql.Nullable[points.Point]], error) {
			return dagql.MapArrayInput(args.Array, func(id dagql.Optional[dagql.ID[points.Point]]) (dagql.Nullable[points.Point], error) {
				return dagql.MapOpt(id, func(id dagql.ID[points.Point]) (points.Point, error) {
					point, err := id.Load(ctx, srv)
					return point.Self, err
				})
			})
		}),
	}.Install(srv)

	gql := client.New(handler.NewDefaultServer(srv))

	t.Run("nullable scalars", func(t *testing.T) {
		var res struct {
			Present     *int
			NotPresent  *int
			NullPresent *int
		}
		req(t, gql, `query {
			present: nullableInt(value: 42)
			notPresent: nullableInt
			nullPresent: nullableInt(value: null)
		}`, &res)
		assert.Assert(t, res.Present != nil)
		assert.Equal(t, 42, *res.Present)
		assert.Assert(t, res.NotPresent == nil)
		assert.Assert(t, res.NullPresent == nil)
	})

	t.Run("nullable objects", func(t *testing.T) {
		var getPoint struct {
			Point struct {
				Id string
			}
		}
		req(t, gql, `query {
			point(x: 6, y: 7) {
				id
			}
		}`, &getPoint)
		var res struct {
			Present    *points.Point
			NotPresent *points.Point
		}
		req(t, gql, `query {
			present: nullablePoint(point: "`+getPoint.Point.Id+`") {
				x
				y
			}
			notPresent: nullablePoint {
				x
				y
			}
		}`, &res)
		assert.Assert(t, res.Present != nil)
		assert.Equal(t, points.Point{X: 6, Y: 7}, *res.Present)
		assert.Assert(t, res.NotPresent == nil)
	})

	t.Run("nullable arrays of scalars", func(t *testing.T) {
		var res struct {
			Present     []int
			NotPresent  []int
			NullPresent []int
		}
		req(t, gql, `query {
			present: nullableScalarArray(array: [6, 7])
			notPresent: nullableScalarArray
			nullPresent: nullableScalarArray(array: null)
		}`, &res)
		assert.Assert(t, res.Present != nil)
		assert.DeepEqual(t, []int{6, 7}, res.Present)
		assert.Assert(t, res.NotPresent == nil)
		assert.Assert(t, res.NullPresent == nil)
	})

	t.Run("non-null arrays with nullable scalars", func(t *testing.T) {
		var res struct {
			ArrayOfNullableInts []*int
		}
		req(t, gql, `query {
			arrayOfNullableInts(array: [6, null, 7])
		}`, &res)
		assert.DeepEqual(t, []*int{ptr(6), nil, ptr(7)}, res.ArrayOfNullableInts)
	})

	t.Run("nullable arrays with nullable elements", func(t *testing.T) {
		var getPoints struct {
			Point struct {
				Neighbors []struct {
					Id string
				}
			}
		}
		req(t, gql, `query {
			point(x: 6, y: 7) {
				neighbors {
					id
				}
			}
		}`, &getPoints)
		ids := []*string{}
		for _, neighbor := range getPoints.Point.Neighbors {
			id := neighbor.Id
			ids = append(ids, &id)
			ids = append(ids, nil)
		}
		payload, err := json.Marshal(ids)
		assert.NilError(t, err)
		var res struct {
			ArrayOfNullablePoints []*points.Point
		}
		req(t, gql, `query {
			arrayOfNullablePoints(array: `+string(payload)+`) {
				x
				y
			}
		}`, &res)
		assert.DeepEqual(t, []*points.Point{
			{X: 5, Y: 7},
			nil,
			{X: 7, Y: 7},
			nil,
			{X: 6, Y: 6},
			nil,
			{X: 6, Y: 8},
			nil,
		}, res.ArrayOfNullablePoints)
	})
}

func ptr[T any](v T) *T {
	return &v
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
					Id        string
					X         int
					Y         int
					Neighbors []struct {
						Id string
						X  int
						Y  int
					}
				}
			}
		}
	}
	req(t, gql, `query {
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
					neighbors {
						id
						x
						y
					}
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
		req(t, gql, `query {
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

		for _, neighbor := range neighbor.Neighbors {
			var res struct {
				LoadPointFromID struct {
					Id string
					X  int
					Y  int
				}
			}
			req(t, gql, `query {
				loadPointFromID(id: "`+neighbor.Id+`") {
					id
					x
					y
				}
			}`, &res)

			assert.Equal(t, neighbor.Id, res.LoadPointFromID.Id)
			assert.Equal(t, neighbor.X, res.LoadPointFromID.X)
			assert.Equal(t, neighbor.Y, res.LoadPointFromID.Y)
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
	req(t, gql, `query {
		point(x: 6, y: 7) {
			shiftLeft {
				id
				neighbors {
					id
				}
			}
		}
	}`, &res)

	expectedID := idproto.New(points.Point{}.Type())
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
		req(t, gql, `query {
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
	req(t, gql, `query {
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
	req(t, gql, `query {
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
		req(t, gql, `query {
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
		req(t, gql, `query {
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
		req(t, gql, `query {
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
			dagql.Func("badShift", func(ctx context.Context, self points.Point, args struct {
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

type MyInput struct {
	Boolean dagql.Boolean `default:"true"`
	Int     dagql.Int     `default:"42"`
	String  dagql.String  `default:"hello, world!"`
	Float   dagql.Float   `default:"3.14"`
}

func (MyInput) TypeName() string {
	return "MyInput"
}

func TestInputObjects(t *testing.T) {
	srv := dagql.NewServer(Query{})
	gql := client.New(handler.NewDefaultServer(srv))

	dagql.MustInputSpec(MyInput{}).Install(srv)

	InstallDefaults(srv)

	dagql.Fields[Query]{
		dagql.Func("myInput", func(ctx context.Context, self Query, args struct {
			Input dagql.InputObject[MyInput]
		}) (Defaults, error) {
			return Defaults(args.Input.Value), nil
		}),
	}.Install(srv)

	type values struct {
		Boolean bool
		Int     int
		String  string
		Float   float64
	}

	t.Run("inputs and defaults", func(t *testing.T) {
		var res struct {
			NotDefaults values
			Defaults    values
		}
		req(t, gql, `query {
			defaults: myInput(input: {}) {
				boolean
				int
				string
				float
			}
			notDefaults: myInput(input: {boolean: false, int: 21, string: "goodbye, world!", float: 6.28}) {
				boolean
				int
				string
				float
			}
		}`, &res)

		assert.Equal(t, values{true, 42, "hello, world!", 3.14}, res.Defaults)
		assert.Equal(t, values{false, 21, "goodbye, world!", 6.28}, res.NotDefaults)
	})

	t.Run("nullable inputs", func(t *testing.T) {
		dagql.Fields[Query]{
			dagql.Func("myOptionalInput", func(ctx context.Context, self Query, args struct {
				Input dagql.Optional[dagql.InputObject[MyInput]]
			}) (dagql.Nullable[dagql.Boolean], error) {
				return dagql.MapOpt(args.Input, func(input dagql.InputObject[MyInput]) (dagql.Boolean, error) {
					return input.Value.Boolean, nil
				})
			}),
		}.Install(srv)

		var res struct {
			ProvidedFalse *bool
			ProvidedTrue  *bool
			NotProvided   *bool
		}
		req(t, gql, `query {
			providedFalse: myOptionalInput(input: {boolean: false})
			providedTrue: myOptionalInput(input: {boolean: true})
			notProvided: myOptionalInput
		}`, &res)

		assert.DeepEqual(t, ptr(false), res.ProvidedFalse)
		assert.DeepEqual(t, ptr(true), res.ProvidedTrue)
		assert.DeepEqual(t, (*bool)(nil), res.NotProvided)
	})

	t.Run("arrays of inputs", func(t *testing.T) {
		dagql.Fields[Query]{
			dagql.Func("myArrayInput", func(ctx context.Context, self Query, args struct {
				Input dagql.ArrayInput[dagql.InputObject[MyInput]]
			}) (dagql.Array[dagql.Boolean], error) {
				return dagql.MapArrayInput(args.Input, func(input dagql.InputObject[MyInput]) (dagql.Boolean, error) {
					return input.Value.Boolean, nil
				})
			}),
		}.Install(srv)

		var res struct {
			MyArrayInput []bool
		}
		req(t, gql, `query {
			myArrayInput(input: [{boolean: false}, {boolean: true}, {}])
		}`, &res)

		assert.DeepEqual(t, []bool{false, true, true}, res.MyArrayInput)
	})
}

type Defaults struct {
	Boolean dagql.Boolean `default:"true"`
	Int     dagql.Int     `default:"42"`
	String  dagql.String  `default:"hello, world!"`
	Float   dagql.Float   `default:"3.14"`
}

func (Defaults) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Defaults",
		NonNull:   true,
	}
}

func InstallDefaults(srv *dagql.Server) {
	dagql.Fields[Defaults]{
		dagql.Func("boolean", func(ctx context.Context, self Defaults, _ any) (dagql.Boolean, error) {
			return self.Boolean, nil
		}),
		dagql.Func("int", func(ctx context.Context, self Defaults, _ any) (dagql.Int, error) {
			return self.Int, nil
		}),
		dagql.Func("string", func(ctx context.Context, self Defaults, _ any) (dagql.String, error) {
			return self.String, nil
		}),
		dagql.Func("float", func(ctx context.Context, self Defaults, _ any) (dagql.Float, error) {
			return self.Float, nil
		}),
	}.Install(srv)
}

func TestDefaults(t *testing.T) {
	srv := dagql.NewServer(Query{})
	gql := client.New(handler.NewDefaultServer(srv))

	InstallDefaults(srv)

	t.Run("builtin scalar types", func(t *testing.T) {
		dagql.Fields[Query]{
			dagql.Func("defaults", func(ctx context.Context, self Query, args Defaults) (Defaults, error) {
				return args, nil // cute
			}),
		}.Install(srv)

		var res struct {
			Defaults struct {
				Boolean bool
				Int     int
				String  string
				Float   float64
			}
		}
		req(t, gql, `query {
			defaults {
				boolean
				int
				string
				float
			}
		}`, &res)

		assert.Equal(t, true, res.Defaults.Boolean)
		assert.Equal(t, 42, res.Defaults.Int)
		assert.Equal(t, "hello, world!", res.Defaults.String)
		assert.Equal(t, 3.14, res.Defaults.Float)
	})

	t.Run("invalid defaults", func(t *testing.T) {
		dagql.Fields[Query]{
			dagql.Func("badBool", func(ctx context.Context, self Query, args struct {
				Boolean dagql.Boolean `default:"yessir"`
			}) (Defaults, error) {
				panic("should not be called")
			}),
			dagql.Func("badInt", func(ctx context.Context, self Query, args struct {
				Int dagql.Int `default:"forty-two"`
			}) (Defaults, error) {
				panic("should not be called")
			}),
			dagql.Func("badFloat", func(ctx context.Context, self Query, args struct {
				Float dagql.Float `default:"float on"`
			}) (Defaults, error) {
				panic("should not be called")
			}),
		}.Install(srv)

		var res struct {
			Defaults struct {
				Boolean bool
				Int     int
				String  string
				Float   float64
			}
		}
		err := gql.Post(`query {
			badBool {
				boolean
			}
			badInt {
				int
			}
			badFloat {
				float
			}
		}`, &res)
		t.Logf("error (expected): %s", err)
		assert.ErrorContains(t, err, "yessir")
		assert.ErrorContains(t, err, "forty-two")
		assert.ErrorContains(t, err, "float on")
	})
}

func TestParallelism(t *testing.T) {
	srv := dagql.NewServer(Query{})
	cons := console.NewWriter(newTWriter(t))
	srv.RecordTo(progrock.NewRecorder(cons))
	gql := client.New(handler.NewDefaultServer(srv))

	pipes.Install[Query](srv)

	t.Run("simple synchronous case", func(t *testing.T) {
		var res struct {
			Pipe struct {
				Write any
				Read  string
			}
		}
		req(t, gql, `query {
			pipe {
				write(message: "hello, world!") {
					id
				}
				read
			}
		}`, &res)

		assert.Equal(t, res.Pipe.Read, "hello, world!")
	})

	// I'm not sure if this is actually necessary to define, but...
	t.Run("parallel at each level", func(t *testing.T) {
		var res struct {
			Pipe struct {
				Write struct {
					Write struct {
						Id string
					}
					Read string
				}
				Read string
			}
		}
		req(t, gql, `query {
			pipe {
				write(message: "one") {
					write(message: "two") {
						id
					}
					read
				}
				read
			}
		}`, &res)

		assert.Equal(t, res.Pipe.Read, "one")
		assert.Equal(t, res.Pipe.Write.Read, "two")
	})
}
