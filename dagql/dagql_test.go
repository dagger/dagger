package dagql_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/99designs/gqlgen/client"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
	"gotest.tools/v3/golden"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/internal/pipes"
	"github.com/dagger/dagger/dagql/internal/points"
	"github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
)

var logs = new(bytes.Buffer)

func init() {
	var logsW io.Writer = logs
	if os.Getenv("DEBUG") != "" {
		logsW = io.MultiWriter(logsW, os.Stderr)
	}
	// keep test output clean
	slog.SetDefault(slog.New(slog.NewTextHandler(logsW, nil)))
}

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

func reqFail(t *testing.T, gql *client.Client, query string, substring string) {
	t.Helper()
	err := gql.Post(query, &struct{}{})
	assert.ErrorContains(t, err, substring)
}

func newCache(t *testing.T) *dagql.SessionCache {
	baseCache, err := dagql.NewCache(t.Context(), "")
	assert.NilError(t, err)
	return dagql.NewSessionCache(baseCache)
}

func TestBasic(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))

	points.Install[Query](srv)

	gql := client.New(dagql.NewDefaultHandler(srv))

	var res struct {
		Point struct {
			X         int
			Y         int
			ShiftLeft struct {
				ID        string
				Ecks      int
				Why       int
				Neighbors []struct {
					ID string
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

	pointT := (&points.Point{}).Type()
	expectedID := call.New().
		Append(pointT, "point", call.WithArgs(
			call.NewArgument(
				"x",
				call.NewLiteralInt(6),
				false,
			),
			call.NewArgument(
				"y",
				call.NewLiteralInt(7),
				false,
			),
		)).
		Append(pointT, "shiftLeft")
	expectedEnc, err := dagql.NewID[*points.Point](expectedID).Encode()
	assert.NilError(t, err)
	assert.Equal(t, 6, res.Point.X)
	assert.Equal(t, 7, res.Point.Y)
	assert.Equal(t, 5, res.Point.ShiftLeft.Ecks)
	assert.Equal(t, 7, res.Point.ShiftLeft.Why)

	assert.Equal(t, expectedEnc, res.Point.ShiftLeft.ID)

	assert.Assert(t, cmp.Len(res.Point.ShiftLeft.Neighbors, 4))
	assert.Equal(t, 4, res.Point.ShiftLeft.Neighbors[0].X)
	assert.Equal(t, 7, res.Point.ShiftLeft.Neighbors[0].Y)
	assert.Equal(t, 6, res.Point.ShiftLeft.Neighbors[1].X)
	assert.Equal(t, 7, res.Point.ShiftLeft.Neighbors[1].Y)
	assert.Equal(t, 5, res.Point.ShiftLeft.Neighbors[2].X)
	assert.Equal(t, 6, res.Point.ShiftLeft.Neighbors[2].Y)
	assert.Equal(t, 5, res.Point.ShiftLeft.Neighbors[3].X)
	assert.Equal(t, 8, res.Point.ShiftLeft.Neighbors[3].Y)
}

func TestSelectArray(t *testing.T) {
	ctx := context.Background()
	srv := dagql.NewServer(Query{}, newCache(t))
	points.Install[Query](srv)

	dagql.Fields[Query]{
		dagql.Func("listOfRandomObjects", func(ctx context.Context, self Query, args struct {
		}) ([]*points.Point, error) {
			rando := rand.IntN(math.MaxInt)
			return []*points.Point{
				{X: rando, Y: rando},
				{X: rando, Y: rando},
			}, nil
		}),
	}.Install(srv)

	dagql.Fields[*points.Point]{
		dagql.Func("instanceNeighbors", func(ctx context.Context, self *points.Point, _ struct{}) (dagql.ResultArray[*points.Point], error) {
			var pt0 dagql.Result[*points.Point]
			err := srv.Select(ctx, srv.Root(), &pt0,
				dagql.Selector{
					Field: "point",
					Args: []dagql.NamedInput{
						{
							Name:  "x",
							Value: dagql.NewInt(self.X - 1),
						},
						{
							Name:  "y",
							Value: dagql.NewInt(self.Y),
						},
					},
				},
			)
			if err != nil {
				return nil, err
			}

			var pt1 dagql.Result[*points.Point]
			err = srv.Select(ctx, srv.Root(), &pt1,
				dagql.Selector{
					Field: "point",
					Args: []dagql.NamedInput{
						{
							Name:  "x",
							Value: dagql.NewInt(self.X + 1),
						},
						{
							Name:  "y",
							Value: dagql.NewInt(self.Y),
						},
					},
				},
			)
			if err != nil {
				return nil, err
			}

			var pt2 dagql.Result[*points.Point]
			err = srv.Select(ctx, srv.Root(), &pt2,
				dagql.Selector{
					Field: "point",
					Args: []dagql.NamedInput{
						{
							Name:  "x",
							Value: dagql.NewInt(self.X),
						},
						{
							Name:  "y",
							Value: dagql.NewInt(self.Y - 1),
						},
					},
				},
			)
			if err != nil {
				return nil, err
			}

			var pt3 dagql.Result[*points.Point]
			err = srv.Select(ctx, srv.Root(), &pt3,
				dagql.Selector{
					Field: "point",
					Args: []dagql.NamedInput{
						{
							Name:  "x",
							Value: dagql.NewInt(self.X),
						},
						{
							Name:  "y",
							Value: dagql.NewInt(self.Y + 1),
						},
					},
				},
			)
			if err != nil {
				return nil, err
			}

			return []dagql.Result[*points.Point]{pt0, pt1, pt2, pt3}, nil
		}),
	}.Install(srv)

	pointSel := dagql.Selector{
		Field: "point",
		Args: []dagql.NamedInput{
			{
				Name:  "x",
				Value: dagql.NewInt(6),
			},
			{
				Name:  "y",
				Value: dagql.NewInt(7),
			},
		},
	}

	t.Run("select all as array", func(t *testing.T) {
		var points dagql.Array[*points.Point]
		assert.NilError(t, srv.Select(ctx, srv.Root(), &points,
			pointSel,
			dagql.Selector{
				Field: "neighbors",
			},
		))
		assert.Equal(t, points[0].X, 5)
		assert.Equal(t, points[0].Y, 7)
	})

	t.Run("select all as instance array", func(t *testing.T) {
		var points dagql.ResultArray[*points.Point]
		assert.NilError(t, srv.Select(ctx, srv.Root(), &points,
			pointSel,
			dagql.Selector{
				Field: "neighbors",
			},
		))

		assert.Equal(t, points[0].Self().X, 5)
		assert.Equal(t, points[0].Self().Y, 7)
		id0 := points[0].ID()
		assert.Equal(t, id0.Type().NamedType(), "Point")
		assert.Equal(t, id0.Type().ToAST().Elem, (*ast.Type)(nil))
		assert.Equal(t, int(id0.Nth()), 1)

		assert.Equal(t, points[1].Self().X, 7)
		assert.Equal(t, points[1].Self().Y, 7)
		id1 := points[1].ID()
		assert.Equal(t, id1.Type().NamedType(), "Point")
		assert.Equal(t, id1.Type().ToAST().Elem, (*ast.Type)(nil))
		assert.Equal(t, int(id1.Nth()), 2)

		// receiver id is the array itself and should be the same for each element in this case
		assert.Equal(t, id0.Receiver().Display(), id1.Receiver().Display())
	})

	t.Run("select all individual instances", func(t *testing.T) {
		var points dagql.ResultArray[*points.Point]
		assert.NilError(t, srv.Select(ctx, srv.Root(), &points,
			pointSel,
			dagql.Selector{
				Field: "instanceNeighbors",
			},
		))

		assert.Equal(t, points[0].Self().X, 5)
		assert.Equal(t, points[0].Self().Y, 7)
		id0 := points[0].ID()
		assert.Equal(t, id0.Type().NamedType(), "Point")
		assert.Equal(t, id0.Type().ToAST().Elem, (*ast.Type)(nil))
		assert.Equal(t, int(id0.Nth()), 0)

		assert.Equal(t, points[1].Self().X, 7)
		assert.Equal(t, points[1].Self().Y, 7)
		id1 := points[1].ID()
		assert.Equal(t, id1.Type().NamedType(), "Point")
		assert.Equal(t, id1.Type().ToAST().Elem, (*ast.Type)(nil))
		assert.Equal(t, int(id1.Nth()), 0)

		// ids are not the same because they are returned as their own individual instances
		assert.Check(t, id0.Display() != id1.Display())
	})

	t.Run("select all array as Typed", func(t *testing.T) {
		var dest dagql.Typed
		assert.NilError(t, srv.Select(ctx, srv.Root(), &dest,
			pointSel,
			dagql.Selector{
				Field: "neighbors",
			},
		))
		res, ok := dest.(dagql.Result[dagql.Typed])
		assert.Assert(t, ok, fmt.Sprintf("expected dagql.Array[*points.Point] but got %T", dest))
		pointsArr, ok := res.Self().(dagql.Array[*points.Point])
		assert.Assert(t, ok, fmt.Sprintf("expected dagql.Array[*points.Point] but got %T", res.Self()))
		assert.Equal(t, len(pointsArr), 4)
	})

	t.Run("select all children", func(t *testing.T) {
		var points dagql.ResultArray[*points.Point]
		assert.ErrorContains(t, srv.Select(ctx, srv.Root(), &points,
			pointSel,
			dagql.Selector{
				Field: "neighbors",
			},
			dagql.Selector{
				Field: "x",
			},
		), "cannot sub-select enum")
	})

	t.Run("select nth", func(t *testing.T) {
		var point dagql.Result[*points.Point]
		assert.NilError(t, srv.Select(ctx, srv.Root(), &point,
			pointSel,
			dagql.Selector{
				Field: "neighbors",
				Nth:   1,
			},
		))
		assert.Equal(t, point.Self().X, 5)
		assert.Equal(t, point.Self().Y, 7)
	})

	t.Run("select nth caching", func(t *testing.T) {
		var point1 dagql.Result[*points.Point]
		assert.NilError(t, srv.Select(ctx, srv.Root(), &point1,
			dagql.Selector{
				Field: "listOfRandomObjects",
				Nth:   1,
			},
		))

		var point2 dagql.Result[*points.Point]
		assert.NilError(t, srv.Select(ctx, srv.Root(), &point2,
			dagql.Selector{
				Field: "listOfRandomObjects",
				Nth:   2,
			},
		))

		assert.Equal(t, point1.Self().X, point2.Self().X)
		assert.Equal(t, point1.Self().Y, point2.Self().Y)
	})
}

func TestNullableResults(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))

	points.Install[Query](srv)

	dagql.Fields[Query]{
		dagql.Func("nullableInt", func(ctx context.Context, self Query, args struct {
			Value dagql.Optional[dagql.Int]
		}) (dagql.Optional[dagql.Int], error) {
			return args.Value, nil
		}),
		dagql.Func("nullablePoint", func(ctx context.Context, self Query, args struct {
			Point dagql.Optional[dagql.ID[*points.Point]]
		}) (dagql.Nullable[*points.Point], error) {
			return dagql.MapOpt(args.Point, func(id dagql.ID[*points.Point]) (*points.Point, error) {
				point, err := id.Load(ctx, srv)
				return point.Self(), err
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
			Array dagql.Optional[dagql.ArrayInput[dagql.ID[*points.Point]]]
		}) (dagql.Nullable[dagql.Array[*points.Point]], error) {
			return dagql.MapOpt(args.Array, func(id dagql.ArrayInput[dagql.ID[*points.Point]]) (dagql.Array[*points.Point], error) {
				return dagql.MapArrayInput(id, func(id dagql.ID[*points.Point]) (*points.Point, error) {
					point, err := id.Load(ctx, srv)
					return point.Self(), err
				})
			})
		}),
		dagql.Func("arrayOfNullableInts", func(ctx context.Context, self Query, args struct {
			Array dagql.ArrayInput[dagql.Optional[dagql.Int]]
		}) (dagql.Array[dagql.Optional[dagql.Int]], error) {
			return args.Array.ToArray(), nil
		}),
		dagql.Func("arrayOfNullablePoints", func(ctx context.Context, self Query, args struct {
			Array dagql.ArrayInput[dagql.Optional[dagql.ID[*points.Point]]]
		}) (dagql.Array[dagql.Nullable[*points.Point]], error) {
			return dagql.MapArrayInput(args.Array, func(id dagql.Optional[dagql.ID[*points.Point]]) (dagql.Nullable[*points.Point], error) {
				return dagql.MapOpt(id, func(id dagql.ID[*points.Point]) (*points.Point, error) {
					point, err := id.Load(ctx, srv)
					if err != nil {
						return nil, err
					}
					return point.Self(), err
				})
			})
		}),
	}.Install(srv)

	gql := client.New(dagql.NewDefaultHandler(srv))

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
				ID string
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
			present: nullablePoint(point: "`+getPoint.Point.ID+`") {
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
					ID string
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
			id := neighbor.ID
			ids = append(ids, &id)
			ids = append(ids, nil)
		}
		payload, err := json.Marshal(ids)
		assert.NilError(t, err)
		var res struct {
			ArrayOfNullablePoints []*struct {
				ID string
				X  int
				Y  int
			}
		}
		req(t, gql, `query {
			arrayOfNullablePoints(array: `+string(payload)+`) {
				id
				x
				y
			}
		}`, &res)
		assert.Assert(t, cmp.Len(res.ArrayOfNullablePoints, 8))
		for i, point := range res.ArrayOfNullablePoints {
			switch i {
			case 1, 3, 5, 7:
				assert.Assert(t, point == nil)
			case 0:
				assert.Equal(t, point.X, 5)
				assert.Equal(t, point.Y, 7)
			case 2:
				assert.Equal(t, point.X, 7)
				assert.Equal(t, point.Y, 7)
			case 4:
				assert.Equal(t, point.X, 6)
				assert.Equal(t, point.Y, 6)
			case 6:
				assert.Equal(t, point.X, 6)
				assert.Equal(t, point.Y, 8)
			}
		}

		t.Run("from ID", func(t *testing.T) {
			for i, point := range res.ArrayOfNullablePoints {
				if i%2 != 0 {
					assert.Assert(t, point == nil)
					continue
				}
				var res struct {
					Loaded points.Point
				}
				req(t, gql, `query {
					loaded: loadPointFromID(id: "`+point.ID+`") {
						x
						y
					}
				}`, &res)
				assert.Equal(t, point.X, res.Loaded.X)
				assert.Equal(t, point.Y, res.Loaded.Y)
			}
		})
	})
}

func TestListResults(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	points.Install[Query](srv)

	dagql.Fields[Query]{
		dagql.Func("listOfInts", func(ctx context.Context, self Query, args struct {
		}) ([]int, error) {
			return []int{1, 2, 3}, nil
		}),
		dagql.Func("emptyListOfInts", func(ctx context.Context, self Query, args struct {
		}) ([]int, error) {
			return []int{}, nil
		}),
		dagql.Func("emptyNilListOfInts", func(ctx context.Context, self Query, args struct {
		}) ([]int, error) {
			return nil, nil
		}),
		dagql.Func("nullableListOfInts", func(ctx context.Context, self Query, args struct {
		}) (dagql.Nullable[dagql.Array[dagql.Int]], error) {
			return dagql.Null[dagql.Array[dagql.Int]](), nil
		}),
		dagql.Func("listOfObjects", func(ctx context.Context, self Query, args struct {
		}) ([]*points.Point, error) {
			return []*points.Point{
				{X: 1, Y: 2},
				{X: 3, Y: 4},
			}, nil
		}),
		dagql.Func("listOfRandomObjects", func(ctx context.Context, self Query, args struct {
		}) ([]*points.Point, error) {
			rando := rand.IntN(math.MaxInt)
			return []*points.Point{
				{X: rando, Y: rando},
				{X: rando, Y: rando},
			}, nil
		}),
		dagql.Func("emptyListOfObjects", func(ctx context.Context, self Query, args struct {
		}) ([]*points.Point, error) {
			return []*points.Point{}, nil
		}),
		dagql.Func("emptyNilListOfObjects", func(ctx context.Context, self Query, args struct {
		}) ([]*points.Point, error) {
			return nil, nil
		}),
		dagql.Func("nullableListOfObjects", func(ctx context.Context, self Query, args struct {
		}) (dagql.Nullable[dagql.Array[*points.Point]], error) {
			return dagql.Null[dagql.Array[*points.Point]](), nil
		}),
	}.Install(srv)

	gql := client.New(dagql.NewDefaultHandler(srv))
	{
		var res struct {
			ListOfInts            []int
			EmptyListOfInts       []int
			EmptyNilListOfInts    []int
			NullableListOfInts    []int
			ListOfObjects         []points.Point
			EmptyListOfObjects    []points.Point
			EmptyNilListOfObjects []points.Point
			NullableListOfObjects []points.Point
		}

		req(t, gql, `query {
			listOfInts
			emptyListOfInts
			emptyNilListOfInts
			nullableListOfInts
			listOfObjects {
				x
				y
			}
			emptyListOfObjects {
				x
				y
			}
			emptyNilListOfObjects {
				x
				y
			}
			nullableListOfObjects {
				x
				y
			}
		}`, &res)
		assert.DeepEqual(t, []int{1, 2, 3}, res.ListOfInts)
		assert.DeepEqual(t, []int{}, res.EmptyListOfInts)
		assert.DeepEqual(t, []int{}, res.EmptyNilListOfInts)
		assert.Check(t, res.NullableListOfInts == nil)
		assert.DeepEqual(t, []points.Point{{X: 1, Y: 2}, {X: 3, Y: 4}}, res.ListOfObjects)
		assert.DeepEqual(t, []points.Point{}, res.EmptyListOfObjects)
		assert.DeepEqual(t, []points.Point{}, res.EmptyNilListOfObjects)
		assert.Check(t, res.NullableListOfObjects == nil)
	}

	{
		var res struct {
			ListOfRandomObjects []struct {
				ID string
			}
		}
		req(t, gql, `query {
			listOfRandomObjects {
				id
			}
		}`, &res)
		assert.Assert(t, cmp.Len(res.ListOfRandomObjects, 2))

		var res2 struct {
			LoadPointFromID struct {
				X int
				Y int
			}
		}
		req(t, gql, `query {
			loadPointFromID(id: "`+res.ListOfRandomObjects[0].ID+`") {
				x
				y
			}
		}`, &res2)
		assert.Equal(t, res2.LoadPointFromID.X, res2.LoadPointFromID.Y)

		var res3 struct {
			LoadPointFromID struct {
				X int
				Y int
			}
		}
		req(t, gql, `query {
			loadPointFromID(id: "`+res.ListOfRandomObjects[1].ID+`") {
				x
				y
			}
		}`, &res3)
		assert.Equal(t, res3.LoadPointFromID.X, res3.LoadPointFromID.Y)

		assert.Equal(t, res2.LoadPointFromID.X, res3.LoadPointFromID.X)
	}
}

func ptr[T any](v T) *T {
	return &v
}

func TestLoadingFromID(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))

	points.Install[Query](srv)

	gql := client.New(dagql.NewDefaultHandler(srv))

	var res struct {
		Point struct {
			X         int
			Y         int
			ShiftLeft struct {
				ID        string
				Ecks      int
				Why       int
				Neighbors []struct {
					ID        string
					X         int
					Y         int
					Neighbors []struct {
						ID string
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
				ID string
				X  int
				Y  int
			}
		}
		req(t, gql, `query {
			loadPointFromID(id: "`+neighbor.ID+`") {
				id
				x
				y
			}
		}`, &res)

		assert.Equal(t, neighbor.ID, res.LoadPointFromID.ID)
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
					ID string
					X  int
					Y  int
				}
			}
			req(t, gql, `query {
				loadPointFromID(id: "`+neighbor.ID+`") {
					id
					x
					y
				}
			}`, &res)

			assert.Equal(t, neighbor.ID, res.LoadPointFromID.ID)
			assert.Equal(t, neighbor.X, res.LoadPointFromID.X)
			assert.Equal(t, neighbor.Y, res.LoadPointFromID.Y)
		}
	}
}

func TestIDsReflectQuery(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	points.Install[Query](srv)

	gql := client.New(dagql.NewDefaultHandler(srv))

	var res struct {
		Point struct {
			ShiftLeft struct {
				ID        string
				Neighbors []struct {
					ID string
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

	pointT := (&points.Point{}).Type()
	expectedID := call.New().
		Append(pointT, "point", call.WithArgs(
			call.NewArgument(
				"x",
				call.NewLiteralInt(6),
				false,
			),
			call.NewArgument(
				"y",
				call.NewLiteralInt(7),
				false,
			),
		)).
		Append(pointT, "shiftLeft")
	expectedEnc, err := dagql.NewID[*points.Point](expectedID).Encode()
	assert.NilError(t, err)
	eqIDs(t, res.Point.ShiftLeft.ID, expectedEnc)

	assert.Assert(t, cmp.Len(res.Point.ShiftLeft.Neighbors, 4))
	for i, neighbor := range res.Point.ShiftLeft.Neighbors {
		var res struct {
			LoadPointFromID struct {
				ID string
				X  int
				Y  int
			}
		}
		req(t, gql, `query {
			loadPointFromID(id: "`+neighbor.ID+`") {
				id
				x
				y
			}
		}`, &res)

		eqIDs(t, res.LoadPointFromID.ID, neighbor.ID)

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

func TestIDsDoNotContainSensitiveValues(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	points.Install[Query](srv)

	gql := client.New(dagql.NewDefaultHandler(srv))

	dagql.Fields[*points.Point]{
		dagql.Func("loginTag", func(ctx context.Context, self *points.Point, _ struct {
			Password string `sensitive:"true"`
		}) (*points.Point, error) {
			return self, nil
		}),
		dagql.Func("loginTagFalse", func(ctx context.Context, self *points.Point, _ struct {
			Password string `sensitive:"false"`
		}) (*points.Point, error) {
			return self, nil
		}),
		dagql.Func("loginChain", func(ctx context.Context, self *points.Point, _ struct {
			Password string
		}) (*points.Point, error) {
			return self, nil
		}).Args(
			dagql.Arg("password").Sensitive(),
		),
	}.Install(srv)

	var res struct {
		Point struct {
			LoginTag, LoginTagFalse, LoginChain struct {
				ID string
			}
		}
	}
	req(t, gql, `query {
		point(x: 6, y: 7) {
			loginTag(password: "hunter2") {
				id
			}
			loginTagFalse(password: "hunter2") {
				id
			}
			loginChain(password: "hunter2") {
				id
			}
		}
	}`, &res)

	pointT := (&points.Point{}).Type()
	expectedID := call.New().
		Append(pointT, "point", call.WithArgs(
			call.NewArgument(
				"x",
				call.NewLiteralInt(6),
				false,
			),
			call.NewArgument(
				"y",
				call.NewLiteralInt(7),
				false,
			),
		)).
		Append(pointT, "loginTag")

	expectedEnc, err := dagql.NewID[*points.Point](expectedID).Encode()
	assert.NilError(t, err)
	eqIDs(t, res.Point.LoginTag.ID, expectedEnc)

	expectedID = call.New().
		Append(pointT, "point", call.WithArgs(
			call.NewArgument(
				"x",
				call.NewLiteralInt(6),
				false,
			),
			call.NewArgument(
				"y",
				call.NewLiteralInt(7),
				false,
			),
		)).
		Append(pointT, "loginChain")

	expectedEnc, err = dagql.NewID[*points.Point](expectedID).Encode()
	assert.NilError(t, err)
	eqIDs(t, res.Point.LoginChain.ID, expectedEnc)

	expectedID = call.New().
		Append(pointT, "point", call.WithArgs(
			call.NewArgument(
				"x",
				call.NewLiteralInt(6),
				false,
			),
			call.NewArgument(
				"y",
				call.NewLiteralInt(7),
				false,
			),
		)).
		Append(pointT, "loginTagFalse", call.WithArgs(
			call.NewArgument(
				"password",
				call.NewLiteralString("hunter2"),
				false,
			),
		))
	expectedEnc, err = dagql.NewID[*points.Point](expectedID).Encode()
	assert.NilError(t, err)
	eqIDs(t, res.Point.LoginTagFalse.ID, expectedEnc)
}

func TestEmptyID(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	points.Install[Query](srv)

	gql := client.New(dagql.NewDefaultHandler(srv))

	var res struct {
		LoadPointFromID struct {
			X int
			Y int
		}
	}
	err := gql.Post(`query {
		loadPointFromID(id: "") {
			id
			x
			y
		}
	}`, &res)
	assert.ErrorContains(t, err, "cannot decode empty string as ID")
}

func TestPureIDsDoNotReEvaluate(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	points.Install[Query](srv)

	gql := client.New(dagql.NewDefaultHandler(srv))

	called := 0
	dagql.Fields[*points.Point]{
		dagql.Func("snitch", func(ctx context.Context, self *points.Point, _ struct{}) (*points.Point, error) {
			called++
			return self, nil
		}),
	}.Install(srv)

	var res struct {
		Point struct {
			Snitch struct {
				ID string
			}
		}
	}
	req(t, gql, `query {
		point(x: 6, y: 7) {
			snitch {
				id
			}
		}
	}`, &res)

	assert.Equal(t, called, 1)

	var loaded struct {
		LoadPointFromID struct {
			ID string
			X  int
			Y  int
		}
	}
	req(t, gql, `query {
		loadPointFromID(id: "`+res.Point.Snitch.ID+`") {
			id
			x
			y
		}
	}`, &loaded)

	assert.Equal(t, loaded.LoadPointFromID.ID, res.Point.Snitch.ID)
	assert.Equal(t, loaded.LoadPointFromID.X, 6)
	assert.Equal(t, loaded.LoadPointFromID.Y, 7)

	assert.Equal(t, called, 1)
}

func TestImpureIDsReEvaluate(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	points.Install[Query](srv)

	gql := client.New(dagql.NewDefaultHandler(srv))

	called := 0
	dagql.Fields[*points.Point]{
		dagql.Func("snitch", func(ctx context.Context, self *points.Point, _ struct{}) (*points.Point, error) {
			called++
			return self, nil
		}).DoNotCache("Increments internal state on each call."),
	}.Install(srv)

	var res struct {
		Point struct {
			Snitch struct {
				ID string
			}
		}
	}
	req(t, gql, `query {
		point(x: 6, y: 7) {
			snitch {
				id
			}
		}
	}`, &res)

	assert.Equal(t, called, 1)

	var loaded struct {
		LoadPointFromID struct {
			ID string
			X  int
			Y  int
		}
	}
	req(t, gql, `query {
		loadPointFromID(id: "`+res.Point.Snitch.ID+`") {
			id
			x
			y
		}
	}`, &loaded)
	assert.Equal(t, loaded.LoadPointFromID.ID, res.Point.Snitch.ID)
	assert.Equal(t, loaded.LoadPointFromID.X, 6)
	assert.Equal(t, loaded.LoadPointFromID.Y, 7)

	assert.Equal(t, called, 2)
}

func TestPassingObjectsAround(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	points.Install[Query](srv)

	gql := client.New(dagql.NewDefaultHandler(srv))

	var res struct {
		Point struct {
			ID string
		}
	}
	req(t, gql, `query {
		point(x: 6, y: 7) {
			id
		}
	}`, &res)

	id67 := res.Point.ID

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
	srv := dagql.NewServer(Query{}, newCache(t))
	points.Install[Query](srv)

	gql := client.New(dagql.NewDefaultHandler(srv))

	t.Run("outputs", func(t *testing.T) {
		var res struct {
			Point struct {
				ID string
			}
		}
		req(t, gql, `query {
			point(x: 6, y: 7) {
				id
			}
		}`, &res)

		id67 := res.Point.ID

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
		dagql.Fields[*points.Point]{
			dagql.Func("badShift", func(ctx context.Context, self *points.Point, args struct {
				Direction points.Direction `default:"BOGUS"`
				Amount    dagql.Int        `default:"1"`
			}) (*points.Point, error) {
				return nil, fmt.Errorf("should not be called")
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

type DefaultsInput struct {
	Boolean     dagql.Boolean `default:"true"`
	Int         dagql.Int     `default:"42"`
	String      dagql.String  `default:"hello, world!"`
	EmptyString dagql.String  `default:""`
	Float       dagql.Float   `default:"3.14"`
	Optional    dagql.Optional[dagql.String]

	EmbeddedWrapped
}

type EmbeddedWrapped struct {
	Slice     dagql.ArrayInput[dagql.Int]                   `field:"true" default:"[1, 2, 3]"`
	DeepSlice dagql.ArrayInput[dagql.ArrayInput[dagql.Int]] `field:"true" default:"[[1, 2], [3]]"`
}

func (DefaultsInput) TypeName() string {
	return "DefaultsInput"
}

type BuiltinsInput struct {
	Boolean     bool    `default:"true"`
	Int         int     `default:"42"`
	String      string  `default:"hello, world!"`
	EmptyString string  `default:""`
	Float       float64 `default:"3.14"`
	Optional    *string
	EmbeddedBuiltins
	InvalidButIgnored any `name:"-"`
}

func (BuiltinsInput) TypeName() string {
	return "BuiltinsInput"
}

func TestInputObjects(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	gql := client.New(dagql.NewDefaultHandler(srv))

	dagql.MustInputSpec(DefaultsInput{}).Install(srv)

	InstallDefaults(srv)
	InstallBuiltins(srv)

	dagql.Fields[Query]{
		dagql.Func("myInput", func(ctx context.Context, self Query, args struct {
			Input dagql.InputObject[DefaultsInput]
		}) (Defaults, error) {
			return Defaults(args.Input.Value), nil
		}),
		dagql.Func("myBuiltinsInput", func(ctx context.Context, self Query, args struct {
			Input dagql.InputObject[BuiltinsInput]
		}) (Builtins, error) {
			return Builtins(args.Input.Value), nil
		}),
	}.Install(srv)

	type values struct {
		Boolean     bool
		Int         int
		String      string
		EmptyString string
		Float       float64
		Slice       []int
		DeepSlice   [][]int
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
				emptyString
				float
				slice
				deepSlice
			}
			notDefaults: myInput(input: {boolean: false, int: 21, string: "goodbye, world!", emptyString: "not empty", float: 6.28, slice: [4, 5], deepSlice: [[4], [5]]}) {
				boolean
				int
				string
				emptyString
				float
				slice
				deepSlice
			}
		}`, &res)

		assert.DeepEqual(t, values{true, 42, "hello, world!", "", 3.14, []int{1, 2, 3}, [][]int{{1, 2}, {3}}}, res.Defaults)
		assert.DeepEqual(t, values{false, 21, "goodbye, world!", "not empty", 6.28, []int{4, 5}, [][]int{{4}, {5}}}, res.NotDefaults)
	})

	t.Run("inputs with embedded structs in IDs", func(t *testing.T) {
		var idRes struct {
			MyInput struct {
				ID string
			}
			DifferentEmbedded struct {
				ID string
			}
		}
		req(t, gql, `query {
			myInput(input: {boolean: false, int: 21, string: "goodbye, world!", emptyString: "not empty", float: 6.28, slice: [4, 5], deepSlice: [[4], [5]]}) {
				id
			}
			differentEmbedded: myInput(input: {boolean: false, int: 21, string: "goodbye, world!", emptyString: "not empty", float: 6.28, slice: [4, 5], deepSlice: [[6], [7]]}) {
				id
			}
		}`, &idRes)

		var id1, id2 call.ID
		err := id1.Decode(idRes.MyInput.ID)
		assert.NilError(t, err)
		err = id2.Decode(idRes.DifferentEmbedded.ID)
		assert.NilError(t, err)

		t.Logf("id1: %s", id1.Display())
		t.Logf("id2: %s", id2.Display())
		assert.Assert(t, id1.Display() != id2.Display())

		var res struct {
			LoadDefaultsFromID values
		}
		req(t, gql, `query {
			loadDefaultsFromID(id: "`+idRes.MyInput.ID+`") {
				boolean
				int
				string
				emptyString
				float
				slice
				deepSlice
			}
		}`, &res)

		assert.DeepEqual(t, values{false, 21, "goodbye, world!", "not empty", 6.28, []int{4, 5}, [][]int{{4}, {5}}}, res.LoadDefaultsFromID)
	})

	t.Run("inputs with builtins and defaults", func(t *testing.T) {
		var res struct {
			NotDefaults values
			Defaults    values
		}
		req(t, gql, `query {
			defaults: myBuiltinsInput(input: {}) {
				boolean
				int
				string
				emptyString
				float
				slice
				deepSlice
			}
			notDefaults: myBuiltinsInput(input: {boolean: false, int: 21, string: "goodbye, world!", emptyString: "not empty", float: 6.28, slice: [4, 5], deepSlice: [[4], [5]]}) {
				boolean
				int
				string
				emptyString
				float
				slice
				deepSlice
			}
		}`, &res)

		assert.DeepEqual(t, values{true, 42, "hello, world!", "", 3.14, []int{1, 2, 3}, [][]int{{1, 2}, {3}}}, res.Defaults)
		assert.DeepEqual(t, values{false, 21, "goodbye, world!", "not empty", 6.28, []int{4, 5}, [][]int{{4}, {5}}}, res.NotDefaults)
	})

	t.Run("nullable inputs", func(t *testing.T) {
		dagql.Fields[Query]{
			dagql.Func("myOptionalInput", func(ctx context.Context, self Query, args struct {
				Input dagql.Optional[dagql.InputObject[DefaultsInput]]
			}) (dagql.Nullable[dagql.Boolean], error) {
				return dagql.MapOpt(args.Input, func(input dagql.InputObject[DefaultsInput]) (dagql.Boolean, error) {
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
				Input dagql.ArrayInput[dagql.InputObject[DefaultsInput]]
			}) (dagql.Array[dagql.Boolean], error) {
				return dagql.MapArrayInput(args.Input, func(input dagql.InputObject[DefaultsInput]) (dagql.Boolean, error) {
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
	Boolean     dagql.Boolean                `field:"true" default:"true"`
	Int         dagql.Int                    `field:"true" default:"42"`
	String      dagql.String                 `field:"true" default:"hello, world!"`
	EmptyString dagql.String                 `field:"true" default:""`
	Float       dagql.Float                  `field:"true" default:"3.14"`
	Optional    dagql.Optional[dagql.String] `field:"true"`

	EmbeddedWrapped
}

func (Defaults) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Defaults",
		NonNull:   true,
	}
}

func InstallDefaults(srv *dagql.Server) {
	dagql.Fields[Defaults]{}.Install(srv)
}

func TestDefaults(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	gql := client.New(dagql.NewDefaultHandler(srv))

	InstallDefaults(srv)

	t.Run("builtin scalar types", func(t *testing.T) {
		dagql.Fields[Query]{
			dagql.Func("defaults", func(ctx context.Context, self Query, args Defaults) (Defaults, error) {
				return args, nil // cute
			}),
		}.Install(srv)

		var res struct {
			Defaults struct {
				Boolean     bool
				Int         int
				String      string
				EmptyString string
				Float       float64
			}
		}
		req(t, gql, `query {
			defaults {
				boolean
				int
				string
				emptyString
				float
			}
		}`, &res)

		assert.Equal(t, true, res.Defaults.Boolean)
		assert.Equal(t, 42, res.Defaults.Int)
		assert.Equal(t, "hello, world!", res.Defaults.String)
		assert.Equal(t, "", res.Defaults.EmptyString)
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
	srv := dagql.NewServer(Query{}, newCache(t))
	gql := client.New(dagql.NewDefaultHandler(srv))

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
						ID string
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

func TestArrayParallelism(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))

	points.Install[Query](srv)

	// Barrier to detect parallel execution.
	// The test creates 4 neighbors - if they're resolved in parallel,
	// all 4 goroutines will reach the barrier and unblock together.
	// If they're resolved serially, the first goroutine will block forever
	// waiting for the others to arrive.
	const numNeighbors = 4
	var arrived atomic.Int32
	allArrived := make(chan struct{})
	var closeOnce sync.Once

	// Add a field that blocks until all array elements have started resolving.
	// This proves parallel execution without timing-based flakiness.
	dagql.Fields[*points.Point]{
		dagql.Func("barrierWait", func(ctx context.Context, self *points.Point, _ struct{}) (*points.Point, error) {
			// Signal that this goroutine has arrived at the barrier
			count := arrived.Add(1)
			if count == numNeighbors {
				// Last one to arrive - unblock everyone
				closeOnce.Do(func() { close(allArrived) })
			}

			// Wait for all goroutines to arrive (or timeout)
			select {
			case <-allArrived:
				return self, nil
			case <-time.After(5 * time.Second):
				return nil, fmt.Errorf("timeout: only %d of %d goroutines arrived at barrier (serial execution detected)", arrived.Load(), numNeighbors)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}),
	}.Install(srv)

	gql := client.New(dagql.NewDefaultHandler(srv))

	var res struct {
		Point struct {
			X         int
			Y         int
			Neighbors []struct {
				BarrierWait struct {
					X int
					Y int
				}
			}
		}
	}

	// Query that fetches neighbors and calls barrierWait on each.
	// If array elements are resolved in parallel, all 4 will reach the barrier
	// and unblock. If serial, the first one will timeout waiting for the others.
	req(t, gql, `query {
		point(x: 6, y: 7) {
			x
			y
			neighbors {
				barrierWait {
					x
					y
				}
			}
		}
	}`, &res)

	// Verify the query completed successfully (proves parallel execution)
	assert.Equal(t, 6, res.Point.X)
	assert.Equal(t, 7, res.Point.Y)
	assert.Assert(t, cmp.Len(res.Point.Neighbors, numNeighbors))

	// Verify all neighbors resolved correctly
	assert.Equal(t, 5, res.Point.Neighbors[0].BarrierWait.X)
	assert.Equal(t, 7, res.Point.Neighbors[0].BarrierWait.Y)
	assert.Equal(t, 7, res.Point.Neighbors[1].BarrierWait.X)
	assert.Equal(t, 7, res.Point.Neighbors[1].BarrierWait.Y)
	assert.Equal(t, 6, res.Point.Neighbors[2].BarrierWait.X)
	assert.Equal(t, 6, res.Point.Neighbors[2].BarrierWait.Y)
	assert.Equal(t, 6, res.Point.Neighbors[3].BarrierWait.X)
	assert.Equal(t, 8, res.Point.Neighbors[3].BarrierWait.Y)

	// Verify all goroutines actually arrived (sanity check)
	assert.Equal(t, int32(numNeighbors), arrived.Load())
}

type Builtins struct {
	Boolean     bool    `field:"true" default:"true"`
	Int         int     `field:"true" default:"42"`
	String      string  `field:"true" default:"hello, world!"`
	EmptyString string  `field:"true" default:""`
	Float       float64 `field:"true" default:"3.14"`
	Optional    *string `field:"true"`
	EmbeddedBuiltins
	InvalidButIgnored any `name:"-"`
}

type EmbeddedBuiltins struct {
	Slice     []int   `field:"true" default:"[1, 2, 3]"`
	DeepSlice [][]int `field:"true" default:"[[1, 2], [3]]"` // chicago style
}

func (Builtins) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Builtins",
		NonNull:   true,
	}
}

func InstallBuiltins(srv *dagql.Server) {
	dagql.Fields[Builtins]{}.Install(srv)
}

func TestBuiltins(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	gql := client.New(dagql.NewDefaultHandler(srv))

	InstallBuiltins(srv)

	t.Run("builtin scalar types", func(t *testing.T) {
		dagql.Fields[Query]{
			dagql.Func("builtins", func(ctx context.Context, self Query, args Builtins) (Builtins, error) {
				return args, nil // cute
			}),
		}.Install(srv)

		var res struct {
			Builtins struct {
				Boolean   bool
				Int       int
				String    string
				Float     float64
				Slice     []int
				DeepSlice [][]int
				Optional  *string
			}
		}
		req(t, gql, `query {
			builtins(boolean: false, int: 21, string: "goodbye, world!", float: 6.28, slice: [4, 5], deepSlice: [[4], [5]], optional: "present") {
				boolean
				int
				string
				float
				slice
				deepSlice
				optional
			}
		}`, &res)

		assert.Check(t, cmp.Equal(false, res.Builtins.Boolean))
		assert.Check(t, cmp.Equal(21, res.Builtins.Int))
		assert.Check(t, cmp.Equal("goodbye, world!", res.Builtins.String))
		assert.Check(t, cmp.Equal(6.28, res.Builtins.Float))
		assert.Check(t, cmp.DeepEqual([]int{4, 5}, res.Builtins.Slice))
		assert.Check(t, cmp.DeepEqual([][]int{{4}, {5}}, res.Builtins.DeepSlice))
		assert.Check(t, cmp.DeepEqual(ptr("present"), res.Builtins.Optional))
	})

	t.Run("with defaults", func(t *testing.T) {
		dagql.Fields[Query]{
			dagql.Func("builtins", func(ctx context.Context, self Query, args Builtins) (Builtins, error) {
				return args, nil // cute
			}),
		}.Install(srv)

		var res struct {
			Builtins struct {
				Boolean   bool
				Int       int
				String    string
				Float     float64
				Slice     []int
				DeepSlice [][]int
				Optional  *string
			}
		}
		req(t, gql, `query {
			builtins {
				boolean
				int
				string
				float
				slice
				deepSlice
				optional
			}
		}`, &res)

		assert.Check(t, cmp.Equal(true, res.Builtins.Boolean))
		assert.Check(t, cmp.Equal(42, res.Builtins.Int))
		assert.Check(t, cmp.Equal("hello, world!", res.Builtins.String))
		assert.Check(t, cmp.Equal(3.14, res.Builtins.Float))
		assert.Check(t, cmp.DeepEqual([]int{1, 2, 3}, res.Builtins.Slice))
		assert.Check(t, cmp.DeepEqual([][]int{{1, 2}, {3}}, res.Builtins.DeepSlice))
		assert.Check(t, res.Builtins.Optional == nil)
	})

	t.Run("invalid defaults for builtins", func(t *testing.T) {
		dagql.Fields[Query]{
			dagql.Func("badBool", func(ctx context.Context, self Query, args struct {
				Boolean bool `default:"yessir"`
			}) (Builtins, error) {
				panic("should not be called")
			}),
			dagql.Func("badInt", func(ctx context.Context, self Query, args struct {
				Int int `default:"forty-two"`
			}) (Builtins, error) {
				panic("should not be called")
			}),
			dagql.Func("badFloat", func(ctx context.Context, self Query, args struct {
				Float float64 `default:"float on"`
			}) (Builtins, error) {
				panic("should not be called")
			}),
			dagql.Func("badSlice", func(ctx context.Context, self Query, args struct {
				Slice []int `default:"pizza"`
			}) (Builtins, error) {
				panic("should not be called")
			}),
		}.Install(srv)

		var res struct {
			Builtins struct {
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
			badSlice {
				slice
			}
		}`, &res)
		t.Logf("error (expected): %s", err)
		assert.ErrorContains(t, err, "yessir")
		assert.ErrorContains(t, err, "forty-two")
		assert.ErrorContains(t, err, "float on")
		assert.ErrorContains(t, err, "pizza")
	})
}

type IntrospectTest struct {
	Field           int `field:"true" doc:"I'm a field!"`
	NotField        int
	DeprecatedField int `field:"true" doc:"Don't use me." deprecated:"use something else"`
}

func (IntrospectTest) Type() *ast.Type {
	return &ast.Type{
		NamedType: "IntrospectTest",
		NonNull:   true,
	}
}

func TestIntrospection(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	introspection.Install[Query](srv)

	// just a quick way to get more coverage
	points.Install[Query](srv)

	dagql.Fields[IntrospectTest]{}.Install(srv)

	dagql.Fields[Query]{
		dagql.Func("fieldDoc", func(ctx context.Context, self Query, args struct{}) (bool, error) {
			return true, nil
		}).Doc(`a really cool function`),

		dagql.Func("argDoc", func(ctx context.Context, self Query, args struct {
			DocumentedArg string `doc:"a really cool argument"`
		}) (string, error) {
			return args.DocumentedArg, nil
		}),

		dagql.Func("argDocChain", func(ctx context.Context, self Query, args struct {
			DocumentedArg string
		}) (string, error) {
			return args.DocumentedArg, nil
		}).Args(
			dagql.Arg("documentedArg").Doc("a really cool argument"),
		),

		dagql.Func("deprecatedField", func(ctx context.Context, self Query, args struct {
			Foo string
		}) (string, error) {
			return args.Foo, nil
		}).Deprecated("use something else", "another para"),

		dagql.Func("deprecatedArg", func(ctx context.Context, self Query, args struct {
			DeprecatedArg string `deprecated:"use something else"`
		}) (string, error) {
			return args.DeprecatedArg, nil
		}),

		dagql.Func("deprecatedArgChain", func(ctx context.Context, self Query, args struct {
			DeprecatedArg string
		}) (string, error) {
			return args.DeprecatedArg, nil
		}).Args(
			dagql.Arg("deprecatedArg").Doc("because I said so").Deprecated(),
		),

		dagql.Func("impureField", func(ctx context.Context, self Query, args struct{}) (string, error) {
			return time.Now().String(), nil
		}).DoNotCache("Because I said so."),
	}.Install(srv)

	gql := client.New(dagql.NewDefaultHandler(srv))

	var res introspection.Response
	req(t, gql, introspection.Query, &res)

	buf := new(bytes.Buffer)
	enc := json.NewEncoder(buf)
	enc.SetIndent("", "  ")
	assert.NilError(t, enc.Encode(res))

	golden.Assert(t, buf.String(), "introspection.json")
}

func TestIDFormat(t *testing.T) {
	ctx := context.Background()
	srv := dagql.NewServer(Query{}, newCache(t))
	points.Install[Query](srv)

	var pointAInst dagql.ObjectResult[*points.Point]
	assert.NilError(t, srv.Select(ctx, srv.Root(), &pointAInst,
		dagql.Selector{
			Field: "point",
			Args: []dagql.NamedInput{
				{Name: "x", Value: dagql.Int(2)},
				{Name: "y", Value: dagql.Int(2)},
			},
		},
	))
	pointADgst := pointAInst.ID().Digest()

	var pointBInst dagql.ObjectResult[*points.Point]
	assert.NilError(t, srv.Select(ctx, srv.Root(), &pointBInst,
		dagql.Selector{
			Field: "point",
			Args: []dagql.NamedInput{
				{Name: "x", Value: dagql.Int(1)},
				{Name: "y", Value: dagql.Int(1)},
			},
		},
	))
	pointBDgst := pointBInst.ID().Digest()

	var lineAInst dagql.ObjectResult[*points.Line]
	assert.NilError(t, srv.Select(ctx, pointBInst, &lineAInst,
		dagql.Selector{
			Field: "line",
			Args: []dagql.NamedInput{
				{Name: "to", Value: dagql.NewID[*points.Point](pointAInst.ID())},
			},
		},
	))
	lineADgst := lineAInst.ID().Digest()

	var pointBFromInst dagql.ObjectResult[*points.Point]
	assert.NilError(t, srv.Select(ctx, lineAInst, &pointBFromInst,
		dagql.Selector{Field: "from"},
	))
	pointBFromDgst := pointBFromInst.ID().Digest()

	var lineBInst dagql.ObjectResult[*points.Line]
	assert.NilError(t, srv.Select(ctx, pointAInst, &lineBInst,
		dagql.Selector{
			Field: "line",
			Args: []dagql.NamedInput{
				{Name: "to", Value: dagql.NewID[*points.Point](pointBFromInst.ID())},
			},
		},
	))
	lineBDgst := lineBInst.ID().Digest()

	var pointAFromInst dagql.ObjectResult[*points.Point]
	assert.NilError(t, srv.Select(ctx, lineBInst, &pointAFromInst,
		dagql.Selector{Field: "from"},
	))
	pointAFromDgst := pointAFromInst.ID().Digest()

	pbDag, err := pointAFromInst.ID().ToProto()
	assert.NilError(t, err)

	assert.Equal(t, len(pbDag.CallsByDigest), 6)

	assert.Equal(t, pbDag.RootDigest, pointAFromDgst.String())
	pointAFromIDFields, ok := pbDag.CallsByDigest[pbDag.RootDigest]
	assert.Check(t, ok)
	assert.Equal(t, pointAFromIDFields.Field, "from")
	assert.Equal(t, len(pointAFromIDFields.Args), 0)

	assert.Equal(t, pointAFromIDFields.ReceiverDigest, lineBDgst.String())
	lineBIDFields, ok := pbDag.CallsByDigest[pointAFromIDFields.ReceiverDigest]
	assert.Check(t, ok)
	assert.Equal(t, lineBIDFields.Field, "line")
	assert.Equal(t, len(lineBIDFields.Args), 1)

	assert.Equal(t, lineBIDFields.ReceiverDigest, pointADgst.String())
	pointAIDFields, ok := pbDag.CallsByDigest[lineBIDFields.ReceiverDigest]
	assert.Check(t, ok)
	assert.Equal(t, pointAIDFields.Field, "point")
	assert.Equal(t, len(pointAIDFields.Args), 2)
	assert.Equal(t, pointAIDFields.ReceiverDigest, "")

	lineBArg := lineBIDFields.Args[0]
	assert.Equal(t, lineBArg.Name, "to")
	assert.Equal(t, lineBArg.Value.GetCallDigest(), pointBFromDgst.String())
	pointBFromIDFields, ok := pbDag.CallsByDigest[lineBArg.Value.GetCallDigest()]
	assert.Check(t, ok)
	assert.Equal(t, pointBFromIDFields.Field, "from")
	assert.Equal(t, len(pointBFromIDFields.Args), 0)

	assert.Equal(t, pointBFromIDFields.ReceiverDigest, lineADgst.String())
	lineAIDFields, ok := pbDag.CallsByDigest[pointBFromIDFields.ReceiverDigest]
	assert.Check(t, ok)
	assert.Equal(t, lineAIDFields.Field, "line")
	assert.Equal(t, len(lineAIDFields.Args), 1)

	assert.Equal(t, lineAIDFields.ReceiverDigest, pointBDgst.String())
	pointBIDFields, ok := pbDag.CallsByDigest[lineAIDFields.ReceiverDigest]
	assert.Check(t, ok)
	assert.Equal(t, pointBIDFields.Field, "point")
	assert.Equal(t, len(pointBIDFields.Args), 2)

	lineAArg := lineAIDFields.Args[0]
	assert.Equal(t, lineAArg.Name, "to")
	assert.Equal(t, lineAArg.Value.GetCallDigest(), pointADgst.String())
}

func TestIDAdditionalDigestsMergeOnSameRecipeCallInDAG(t *testing.T) {
	base := call.New().Append(dagql.Int(0).Type(), "same")
	idA := base.With(call.WithExtraDigest(call.ExtraDigest{Digest: digest.FromString("additional-a")}))
	idB := base.With(call.WithExtraDigest(call.ExtraDigest{Digest: digest.FromString("additional-b")}))

	assert.Check(t, idA.Digest() == idB.Digest())

	root := call.New().Append(
		dagql.Int(0).Type(),
		"combine",
		call.WithArgs(
			call.NewArgument("a", call.NewLiteralID(idA), false),
			call.NewArgument("b", call.NewLiteralID(idB), false),
		),
	)

	pbDag, err := root.ToProto()
	assert.NilError(t, err)

	callPB, ok := pbDag.CallsByDigest[idA.Digest().String()]
	assert.Assert(t, ok)
	assert.Equal(t, len(callPB.ExtraDigests), 2)
	assert.Equal(t, callPB.ExtraDigests[0].Digest, digest.FromString("additional-a").String())
	assert.Equal(t, callPB.ExtraDigests[1].Digest, digest.FromString("additional-b").String())
	assert.Equal(t, callPB.ExtraDigests[0].Label, "")
	assert.Equal(t, callPB.ExtraDigests[1].Label, "")

	var decoded call.ID
	assert.NilError(t, decoded.FromProto(pbDag))
	decodedDAG, err := decoded.ToProto()
	assert.NilError(t, err)
	_, ok = decodedDAG.CallsByDigest[idA.Digest().String()]
	assert.Assert(t, ok)
	assert.Equal(t, len(decodedDAG.CallsByDigest), len(pbDag.CallsByDigest))
}

func TestIDWithContentDigestAddsKnownDigest(t *testing.T) {
	base := call.New().Append(dagql.Int(0).Type(), "same-content")
	first := digest.FromString("content-digest-a")
	second := digest.FromString("content-digest-b")

	withContent := base.
		With(call.WithContentDigest(first)).
		With(call.WithContentDigest(second))

	assert.Equal(t, withContent.ContentDigest().String(), second.String())
	assert.DeepEqual(t, withContent.ExtraDigests(), []call.ExtraDigest{
		{Digest: first, Label: "content"},
		{Digest: second, Label: "content"},
	})
}

func eqIDs(t *testing.T, actual, expected string) {
	debugID(t, "actual  : %s", actual)
	debugID(t, "expected: %s", expected)
	assert.Equal(t, actual, expected)
}

func debugID(t *testing.T, msgf string, idStr string, args ...any) {
	var id call.ID
	err := id.Decode(idStr)
	assert.NilError(t, err)
	t.Logf(msgf, append([]any{id.Display()}, args...)...)
}

func InstallViewer(srv *dagql.Server) {
	getView := func(_ context.Context, _ Query, _ struct{}) (string, error) {
		return string(srv.View), nil
	}
	getViewArg := func(_ context.Context, _ Query, args struct {
		Arg string
	}) (string, error) {
		return string(srv.View) + args.Arg, nil
	}

	dagql.Fields[Query]{
		dagql.Func("global", getView).
			View(dagql.GlobalView).
			Doc("available on all views"),
		dagql.Func("all", getView).
			View(dagql.AllView{}).
			Doc("available on all views"),

		dagql.Func("args", getViewArg).
			View(dagql.AllView{}).
			Doc("available on all views").
			Args(
				dagql.Arg("arg").View(dagql.ExactView("firstView")).Doc("available on first view"),
				dagql.Arg("arg").View(dagql.ExactView("secondView")).Doc("available on second view"),
			),

		dagql.Func("shared", getView).
			View(dagql.ExactView("firstView")).
			Doc("available on first+second views"),
		dagql.Func("firstExclusive", getView).
			View(dagql.ExactView("firstView")).
			Doc("available on first view"),

		dagql.Func("shared", getView).
			View(dagql.ExactView("secondView")).
			Extend(),
		dagql.Func("secondExclusive", getView).
			View(dagql.ExactView("secondView")).
			Doc("available on second view"),
		dagql.Func("all", getView).
			View(dagql.ExactView("secondView")).
			Doc("available on second view"),
	}.Install(srv)
}

func TestViews(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	gql := client.New(dagql.NewDefaultHandler(srv))

	InstallViewer(srv)

	t.Run("in default view", func(t *testing.T) {
		srv.View = ""

		var res struct {
			All  string
			Args string
		}
		req(t, gql, `query {
			all
			args
		}`, &res)
		assert.Equal(t, "", res.All)

		reqFail(t, gql, `query {
			shared
		}`, "Cannot query field")

		reqFail(t, gql, `query {
			args(arg: "foo")
		}`, `Unknown argument \"arg\"`)
	})

	t.Run("in unknown view", func(t *testing.T) {
		srv.View = "unknownView"

		var res struct {
			All  string
			Args string
		}
		req(t, gql, `query {
			all
			args
		}`, &res)
		assert.Equal(t, "unknownView", res.All)

		reqFail(t, gql, `query {
			shared
		}`, "Cannot query field")

		reqFail(t, gql, `query {
			args(arg: "foo")
		}`, `Unknown argument \"arg\"`)
	})

	t.Run("in first view", func(t *testing.T) {
		srv.View = "firstView"

		var res struct {
			All            string
			Shared         string
			Args           string
			FirstExclusive string
		}
		req(t, gql, `query {
			all
			shared
			args(arg: "foo")
			firstExclusive
		}`, &res)
		assert.Equal(t, "firstView", res.All)
		assert.Equal(t, "firstView", res.Shared)
		assert.Equal(t, "firstViewfoo", res.Args)
		assert.Equal(t, "firstView", res.FirstExclusive)

		reqFail(t, gql, `query {
			secondExclusive
		}`, "Cannot query field")
	})

	t.Run("in second view", func(t *testing.T) {
		srv.View = "secondView"

		var res struct {
			All             string
			Shared          string
			Args            string
			SecondExclusive string
		}
		req(t, gql, `query {
			all
			shared
			args(arg: "foo")
			secondExclusive
		}`, &res)
		assert.Equal(t, "secondView", res.All)
		assert.Equal(t, "secondView", res.Shared)
		assert.Equal(t, "secondViewfoo", res.Args)
		assert.Equal(t, "secondView", res.SecondExclusive)

		reqFail(t, gql, `query {
			firstExclusive
		}`, "Cannot query field")
	})
}

func TestViewsCaching(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	gql := client.New(dagql.NewDefaultHandler(srv))

	InstallViewer(srv)

	var res struct {
		All    string
		Global string
	}

	srv.View = "firstView"
	req(t, gql, `query {
		all
		global
	}`, &res)
	assert.Equal(t, "firstView", res.All)
	assert.Equal(t, "firstView", res.Global)

	srv.View = "secondView"
	req(t, gql, `query {
		all
		global
	}`, &res)
	assert.Equal(t, "secondView", res.All)
	assert.Equal(t, "firstView", res.Global) // this is cached from the first query!
}

func TestViewsIntrospection(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	introspection.Install[Query](srv)
	gql := client.New(dagql.NewDefaultHandler(srv))

	InstallViewer(srv)

	t.Run("in default view", func(t *testing.T) {
		srv.View = ""

		var res introspection.Response
		req(t, gql, introspection.Query, &res)
		fields := make(map[string]string)
		for _, field := range res.Schema.Types.Get("Query").Fields {
			fields[field.Name] = field.Description
		}

		require.Contains(t, fields, "all")
		require.Equal(t, "available on all views", fields["all"])
		require.Contains(t, fields, "global")
		require.Equal(t, "available on all views", fields["global"])
		require.NotContains(t, fields, "shared")
	})

	t.Run("in unknown view", func(t *testing.T) {
		srv.View = "unknownView"

		var res introspection.Response
		req(t, gql, introspection.Query, &res)
		fields := make(map[string]string)
		for _, field := range res.Schema.Types.Get("Query").Fields {
			fields[field.Name] = field.Description
		}

		require.Contains(t, fields, "all")
		require.Equal(t, "available on all views", fields["all"])
		require.Contains(t, fields, "global")
		require.Equal(t, "available on all views", fields["global"])
		require.NotContains(t, fields, "shared")
	})

	t.Run("in first view", func(t *testing.T) {
		srv.View = "firstView"

		var res introspection.Response
		req(t, gql, introspection.Query, &res)
		fields := make(map[string]string)
		for _, field := range res.Schema.Types.Get("Query").Fields {
			fields[field.Name] = field.Description
		}

		require.Contains(t, fields, "all")
		require.Equal(t, "available on all views", fields["all"])
		require.Contains(t, fields, "global")
		require.Equal(t, "available on all views", fields["global"])
		require.Contains(t, fields, "shared")
		require.Equal(t, "available on first+second views", fields["shared"])
		require.Contains(t, fields, "firstExclusive")
		require.Equal(t, "available on first view", fields["firstExclusive"])
		require.NotContains(t, fields, "secondExclusive")
	})

	t.Run("in second view", func(t *testing.T) {
		srv.View = "secondView"

		var res introspection.Response
		req(t, gql, introspection.Query, &res)
		fields := make(map[string]string)
		for _, field := range res.Schema.Types.Get("Query").Fields {
			fields[field.Name] = field.Description
		}

		require.Contains(t, fields, "all")
		require.Equal(t, "available on second view", fields["all"])
		require.Contains(t, fields, "global")
		require.Equal(t, "available on all views", fields["global"])
		require.Contains(t, fields, "shared")
		require.Equal(t, "available on first+second views", fields["shared"])
		require.NotContains(t, fields, "firstExclusive")
		require.Contains(t, fields, "secondExclusive")
		require.Equal(t, "available on second view", fields["secondExclusive"])
	})
}

type CoolInt struct {
	Val int `field:"true"`
}

func (*CoolInt) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CoolInt",
		NonNull:   true,
	}
}

func (*CoolInt) TypeDescription() string {
	return "idk"
}

func TestNodeFuncResultZeroValueTypeInference(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))

	dagql.Fields[*CoolInt]{}.Install(srv)
	dagql.Fields[Query]{
		dagql.NodeFunc("typedResult", func(ctx context.Context, _ dagql.ObjectResult[Query], args struct {
			Val int
		}) (dagql.Result[*CoolInt], error) {
			return dagql.NewResultForCurrentID(ctx, &CoolInt{Val: args.Val})
		}),
	}.Install(srv)

	gql := client.New(dagql.NewDefaultHandler(srv))

	var res struct {
		TypedResult struct {
			Val int
		}
	}
	req(t, gql, `query {
		typedResult(val: 123) {
			val
		}
	}`, &res)
	assert.Equal(t, 123, res.TypedResult.Val)
}

func TestNullResultCachePathDoesNotPanic(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	dagql.Fields[Query]{
		dagql.Func("alwaysNull", func(context.Context, Query, struct{}) (dagql.Nullable[dagql.String], error) {
			return dagql.Null[dagql.String](), nil
		}),
	}.Install(srv)

	gql := client.New(dagql.NewDefaultHandler(srv))

	for range 2 {
		var res struct {
			AlwaysNull *string
		}
		req(t, gql, `query {
			alwaysNull
		}`, &res)
		assert.Assert(t, res.AlwaysNull == nil)
	}
}

func TestCacheConfigReturnedIDRewritesExecutionArgs(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))

	calls := []int{}
	dagql.Fields[Query]{
		dagql.NodeFuncWithDynamicInputs(
			"rewrittenArg",
			func(_ context.Context, _ dagql.ObjectResult[Query], args struct{ Val int }) (dagql.Int, error) {
				calls = append(calls, args.Val)
				return dagql.Int(args.Val), nil
			},
			func(_ context.Context, _ dagql.ObjectResult[Query], _ struct{ Val int }, req dagql.DynamicInputRequest) (*dagql.DynamicInputResponse, error) {
				resp := &dagql.DynamicInputResponse{CacheKey: req.CacheKey}
				resp.CacheKey.ID = resp.CacheKey.ID.WithArgument(call.NewArgument(
					"val",
					dagql.Int(7).ToLiteral(),
					false,
				))
				return resp, nil
			},
		),
	}.Install(srv)

	gql := client.New(dagql.NewDefaultHandler(srv))
	var res struct {
		RewrittenArg int
	}
	req(t, gql, `query { rewrittenArg(val: 1) }`, &res)
	assert.Equal(t, 7, res.RewrittenArg)
	assert.DeepEqual(t, calls, []int{7})
}

func TestImplicitInputCachePerClient(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))

	var calls atomic.Int64
	dagql.Fields[Query]{
		dagql.NodeFunc("perClientCounter", func(ctx context.Context, _ dagql.ObjectResult[Query], _ struct{}) (int, error) {
			return int(calls.Add(1)), nil
		}).WithInput(dagql.CachePerClient),
	}.Install(srv)

	callForClient := func(clientID string) int {
		ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
			ClientID: clientID,
		})
		var res int
		err := srv.Select(ctx, srv.Root(), &res, dagql.Selector{
			Field: "perClientCounter",
		})
		require.NoError(t, err)
		return res
	}

	assert.Equal(t, callForClient("client-a"), 1)
	assert.Equal(t, callForClient("client-a"), 1)
	assert.Equal(t, callForClient("client-b"), 2)
	assert.Equal(t, callForClient("client-b"), 2)
}

func TestImplicitInputCachePerSession(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))

	var calls atomic.Int64
	dagql.Fields[Query]{
		dagql.NodeFunc("perSessionCounter", func(ctx context.Context, _ dagql.ObjectResult[Query], _ struct{}) (int, error) {
			return int(calls.Add(1)), nil
		}).WithInput(dagql.CachePerSession),
	}.Install(srv)

	callForSession := func(sessionID string) int {
		ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
			ClientID:  "same-client",
			SessionID: sessionID,
		})
		var res int
		err := srv.Select(ctx, srv.Root(), &res, dagql.Selector{
			Field: "perSessionCounter",
		})
		require.NoError(t, err)
		return res
	}

	assert.Equal(t, callForSession("session-a"), 1)
	assert.Equal(t, callForSession("session-a"), 1)
	assert.Equal(t, callForSession("session-b"), 2)
	assert.Equal(t, callForSession("session-b"), 2)
}

func TestImplicitInputCachePerCall(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))

	var calls atomic.Int64
	dagql.Fields[Query]{
		dagql.NodeFunc("perCallCounter", func(ctx context.Context, _ dagql.ObjectResult[Query], _ struct{}) (int, error) {
			return int(calls.Add(1)), nil
		}).WithInput(dagql.CachePerCall),
	}.Install(srv)

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "client-a",
	})

	var first int
	require.NoError(t, srv.Select(ctx, srv.Root(), &first, dagql.Selector{Field: "perCallCounter"}))
	assert.Equal(t, first, 1)

	var second int
	require.NoError(t, srv.Select(ctx, srv.Root(), &second, dagql.Selector{Field: "perCallCounter"}))
	assert.Equal(t, second, 2)

	var third int
	require.NoError(t, srv.Select(ctx, srv.Root(), &third, dagql.Selector{Field: "perCallCounter"}))
	assert.Equal(t, third, 3)
}

func TestImplicitInputCachePerSchema(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))

	var calls atomic.Int64
	dagql.Fields[Query]{
		dagql.NodeFunc("perSchemaCounter", func(ctx context.Context, _ dagql.ObjectResult[Query], _ struct{}) (int, error) {
			return int(calls.Add(1)), nil
		}).WithInput(dagql.CachePerSchema(srv)),
	}.Install(srv)

	call := func() int {
		var res int
		err := srv.Select(context.Background(), srv.Root(), &res, dagql.Selector{
			Field: "perSchemaCounter",
		})
		require.NoError(t, err)
		return res
	}

	assert.Equal(t, call(), 1)
	assert.Equal(t, call(), 1)

	// Change schema; the implicit schema input should invalidate cache identity.
	dagql.Fields[Query]{
		dagql.NodeFunc("schemaBump", func(context.Context, dagql.ObjectResult[Query], struct{}) (int, error) {
			return 0, nil
		}),
	}.Install(srv)

	assert.Equal(t, call(), 2)
	assert.Equal(t, call(), 2)
}

func TestImplicitInputCachePerClientSchema(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))

	var calls atomic.Int64
	dagql.Fields[Query]{
		dagql.NodeFunc("perClientSchemaCounter", func(ctx context.Context, _ dagql.ObjectResult[Query], _ struct{}) (int, error) {
			return int(calls.Add(1)), nil
		}).WithInput(dagql.CachePerClient, dagql.CachePerSchema(srv)),
	}.Install(srv)

	callForClient := func(clientID string) int {
		ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
			ClientID: clientID,
		})
		var res int
		err := srv.Select(ctx, srv.Root(), &res, dagql.Selector{
			Field: "perClientSchemaCounter",
		})
		require.NoError(t, err)
		return res
	}

	assert.Equal(t, callForClient("client-a"), 1)
	assert.Equal(t, callForClient("client-a"), 1)
	assert.Equal(t, callForClient("client-b"), 2)
	assert.Equal(t, callForClient("client-b"), 2)

	// Change schema; same client should now see a new cache identity.
	dagql.Fields[Query]{
		dagql.NodeFunc("schemaBump", func(context.Context, dagql.ObjectResult[Query], struct{}) (int, error) {
			return 0, nil
		}),
	}.Install(srv)

	assert.Equal(t, callForClient("client-a"), 3)
	assert.Equal(t, callForClient("client-a"), 3)
}

func TestImplicitInputCacheAsRequested(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))

	var calls atomic.Int64
	dagql.Fields[Query]{
		dagql.NodeFunc("asRequestedCounter", func(ctx context.Context, _ dagql.ObjectResult[Query], args struct {
			NoCache bool `default:"false"`
		}) (int, error) {
			return int(calls.Add(1)), nil
		}).WithInput(dagql.CacheAsRequested("noCache")),
	}.Install(srv)

	call := func(clientID string, noCache bool) int {
		ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
			ClientID: clientID,
		})
		var res int
		err := srv.Select(ctx, srv.Root(), &res, dagql.Selector{
			Field: "asRequestedCounter",
			Args: []dagql.NamedInput{
				{Name: "noCache", Value: dagql.NewBoolean(noCache)},
			},
		})
		require.NoError(t, err)
		return res
	}
	callDefault := func(clientID string) int {
		ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
			ClientID: clientID,
		})
		var res int
		err := srv.Select(ctx, srv.Root(), &res, dagql.Selector{
			Field: "asRequestedCounter",
		})
		require.NoError(t, err)
		return res
	}

	// default noCache=false should also resolve as per-client cache
	assert.Equal(t, callDefault("client-default"), 1)
	assert.Equal(t, callDefault("client-default"), 1)

	// noCache=false => per-client cache reuse
	assert.Equal(t, call("client-a", false), 2)
	assert.Equal(t, call("client-a", false), 2)
	assert.Equal(t, call("client-b", false), 3)
	assert.Equal(t, call("client-b", false), 3)

	// noCache=true => per-call execution
	assert.Equal(t, call("client-a", true), 4)
	assert.Equal(t, call("client-a", true), 5)
}

func TestImplicitInputRecomputedAfterCacheConfigIDRewrite(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))

	var calls atomic.Int64
	var observed []bool
	var observedL sync.Mutex
	observedInput := dagql.ImplicitInput{
		Name: "observedNoCache",
		Resolver: func(_ context.Context, args map[string]dagql.Input) (dagql.Input, error) {
			raw, ok := args["noCache"]
			if !ok {
				return nil, fmt.Errorf("missing noCache arg")
			}
			var noCache bool
			switch val := raw.(type) {
			case dagql.Boolean:
				noCache = val.Bool()
			case dagql.Optional[dagql.Boolean]:
				if val.Valid {
					noCache = val.Value.Bool()
				}
			case dagql.DynamicOptional:
				if val.Valid {
					booleanVal, ok := val.Value.(dagql.Boolean)
					if !ok {
						return nil, fmt.Errorf("expected noCache bool in dynamic optional, got %T", val.Value)
					}
					noCache = booleanVal.Bool()
				}
			default:
				return nil, fmt.Errorf("expected noCache bool, got %T", raw)
			}
			observedL.Lock()
			observed = append(observed, noCache)
			observedL.Unlock()
			return dagql.NewString(fmt.Sprintf("%t", noCache)), nil
		},
	}
	dagql.Fields[Query]{
		dagql.NodeFuncWithDynamicInputs("updatedArgCounter", func(ctx context.Context, _ dagql.ObjectResult[Query], args struct {
			NoCache bool `default:"false"`
		}) (int, error) {
			return int(calls.Add(1)), nil
		}, func(ctx context.Context, _ dagql.ObjectResult[Query], _ struct {
			NoCache bool `default:"false"`
		}, req dagql.DynamicInputRequest) (*dagql.DynamicInputResponse, error) {
			resp := &dagql.DynamicInputResponse{
				CacheKey: req.CacheKey,
			}
			resp.CacheKey.ID = resp.CacheKey.ID.WithArgument(call.NewArgument(
				"noCache",
				dagql.NewBoolean(true).ToLiteral(),
				false,
			))
			return resp, nil
		}).WithInput(observedInput),
	}.Install(srv)

	ctx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID: "client-a",
	})

	var result int
	require.NoError(t, srv.Select(ctx, srv.Root(), &result, dagql.Selector{Field: "updatedArgCounter"}))
	assert.Equal(t, result, 1)

	observedL.Lock()
	defer observedL.Unlock()
	assert.DeepEqual(t, observed, []bool{false, true})
}

func TestServerSelect(t *testing.T) {
	// Create a new server with a simple object hierarchy for testing
	srv := dagql.NewServer(Query{}, newCache(t))

	// Install test types
	InstallTestTypes(srv)

	ctx := context.Background()

	t.Run("basic selection", func(t *testing.T) {
		// Create a test object and wrap it as a dagql.Object
		testObj, err := dagql.NewResultForID(&TestObject{Value: 42, Text: "hello"},
			call.New().Append((TestObject{}).Type(), "fake"))
		require.NoError(t, err)

		// Get the installed class from the server
		testObjClass, ok := srv.ObjectType("TestObject")
		require.True(t, ok, "TestObject class not found")

		// Create an instance
		objResult, err := testObjClass.New(testObj)
		require.NoError(t, err)

		// Test selecting a simple field
		var result int
		err = srv.Select(ctx, objResult, &result, dagql.Selector{Field: "value"})
		require.NoError(t, err)
		assert.Equal(t, 42, result)

		// Test selecting a string field
		var textResult string
		err = srv.Select(ctx, objResult, &textResult, dagql.Selector{Field: "text"})
		require.NoError(t, err)
		assert.Equal(t, "hello", textResult)
	})

	t.Run("chained selection", func(t *testing.T) {
		// Create nested objects
		innerObj := &TestObject{Value: 100, Text: "nested value"}
		nestedObj, err := dagql.NewResultForID(&NestedObject{
			Name:  "nested",
			Inner: innerObj,
		}, call.New().Append((TestObject{}).Type(), "fake"))
		require.NoError(t, err)

		// Get the installed class from the server
		nestedObjClass, ok := srv.ObjectType("NestedObject")
		require.True(t, ok, "NestedObject class not found")

		// Create an instance
		objResult, err := nestedObjClass.New(nestedObj)
		require.NoError(t, err)

		// Test selecting through a chain of objects
		var result int
		err = srv.Select(ctx, objResult, &result,
			dagql.Selector{Field: "inner"},
			dagql.Selector{Field: "value"})
		require.NoError(t, err)
		assert.Equal(t, 100, result)
	})

	t.Run("null result", func(t *testing.T) {
		// Create an object with a null field
		testObj, err := dagql.NewResultForID(&TestObject{Value: 42, Text: "hello", NullableField: nil},
			call.New().Append((TestObject{}).Type(), "fake"),
		)
		require.NoError(t, err)

		// Get the installed class from the server
		testObjClass, ok := srv.ObjectType("TestObject")
		require.True(t, ok, "TestObject class not found")

		// Create an instance
		objResult, err := testObjClass.New(testObj)
		require.NoError(t, err)

		// Test selecting a null field
		var result *string
		err = srv.Select(ctx, objResult, &result, dagql.Selector{Field: "nullableField"})
		require.NoError(t, err)
		assert.Assert(t, result == nil)
	})

	t.Run("array selection", func(t *testing.T) {
		// Create an array of integers
		intArray := dagql.NewIntArray(1, 2, 3)

		// Add a field to Query that returns this array
		dagql.Fields[Query]{
			dagql.Func("testArray", func(ctx context.Context, self Query, args struct{}) (dagql.Array[dagql.Int], error) {
				return intArray, nil
			}),
		}.Install(srv)

		// Get the root object
		root := srv.Root()

		// For arrays, we need to use a different approach
		// First, get the array result
		var arrayResult dagql.AnyResult
		arrayResult, err := root.Select(ctx, srv, dagql.Selector{Field: "testArray"})
		require.NoError(t, err)

		// Verify it's enumerable
		enum, ok := arrayResult.Unwrap().(dagql.Enumerable)
		require.True(t, ok, "Expected array to be enumerable")
		assert.Equal(t, 3, enum.Len())

		// Check each item
		for i := 1; i <= enum.Len(); i++ {
			item, err := enum.Nth(i)
			require.NoError(t, err)

			// Convert to int
			intVal, ok := item.(dagql.Int)
			require.True(t, ok, "Expected item to be a dagql.Int")
			assert.Equal(t, i, int(intVal))
		}
	})

	t.Run("array selection into []int", func(t *testing.T) {
		// Create an array of integers
		intArray := dagql.NewIntArray(1, 2, 3)

		// Add a field to Query that returns this array
		dagql.Fields[Query]{
			dagql.Func("testArray", func(ctx context.Context, self Query, args struct{}) (dagql.Array[dagql.Int], error) {
				return intArray, nil
			}),
		}.Install(srv)

		// Get the root object
		root := srv.Root()

		// For arrays, we need to use a different approach
		// First, get the array result
		var result []int
		err := srv.Select(ctx, root, &result, dagql.Selector{Field: "testArray"})
		require.NoError(t, err)
		require.Equal(t, []int{1, 2, 3}, result)
	})

	t.Run("array selection into []string", func(t *testing.T) {
		// Create an array of integers
		strArray := dagql.NewStringArray("one", "two", "three")

		// Add a field to Query that returns this array
		dagql.Fields[Query]{
			dagql.Func("testArray", func(ctx context.Context, self Query, args struct{}) (dagql.Array[dagql.String], error) {
				return strArray, nil
			}),
		}.Install(srv)

		// Get the root object
		root := srv.Root()

		// For arrays, we need to use a different approach
		// First, get the array result
		var result []string
		err := srv.Select(ctx, root, &result, dagql.Selector{Field: "testArray"})
		require.NoError(t, err)
		require.Equal(t, []string{"one", "two", "three"}, result)
	})

	t.Run("error cases", func(t *testing.T) {
		// Create a test object
		testObj, err := dagql.NewResultForID(&TestObject{Value: 42, Text: "hello"},
			call.New().Append((TestObject{}).Type(), "fake"),
		)
		require.NoError(t, err)

		// Get the installed class from the server
		testObjClass, ok := srv.ObjectType("TestObject")
		require.True(t, ok, "TestObject class not found")

		// Create an instance
		objResult, err := testObjClass.New(testObj)
		require.NoError(t, err)

		// Test selecting a non-existent field
		var result int
		err = srv.Select(ctx, objResult, &result, dagql.Selector{Field: "nonExistentField"})
		require.Error(t, err)

		// Test invalid selector chain (trying to select from a scalar)
		err = srv.Select(ctx, objResult, &result,
			dagql.Selector{Field: "value"},
			dagql.Selector{Field: "something"})
		require.Error(t, err)
	})

	t.Run("null result handling", func(t *testing.T) {
		// Add a field to Query that returns null
		dagql.Fields[Query]{
			dagql.Func("nullResult", func(ctx context.Context, self Query, args struct{}) (dagql.Nullable[dagql.String], error) {
				return dagql.Null[dagql.String](), nil
			}),
		}.Install(srv)

		// Get the root object
		root := srv.Root()

		// Test selecting a null result
		var result *string
		err := srv.Select(ctx, root, &result, dagql.Selector{Field: "nullResult"})
		require.NoError(t, err)
		assert.Assert(t, result == nil, "Expected result to be nil")

		// Test selecting from a null result (should not error)
		var nestedResult string
		err = srv.Select(ctx, root, &nestedResult,
			dagql.Selector{Field: "nullResult"},
			dagql.Selector{Field: "nonExistentField"})
		require.NoError(t, err)
		assert.Equal(t, "", nestedResult, "Expected empty result for selection from null")
	})
}

// Helper types for testing

type TestObject struct {
	Value         int     `field:"true"`
	Text          string  `field:"true"`
	NullableField *string `field:"true"`
}

func (TestObject) Type() *ast.Type {
	return &ast.Type{
		NamedType: "TestObject",
		NonNull:   true,
	}
}

type NestedObject struct {
	Name  string      `field:"true"`
	Inner *TestObject `field:"true"`
}

func (NestedObject) Type() *ast.Type {
	return &ast.Type{
		NamedType: "NestedObject",
		NonNull:   true,
	}
}

// InstallTestTypes installs the test types on the server
func InstallTestTypes(srv *dagql.Server) {
	// Install TestObject
	testObjClass := dagql.NewClass(srv, dagql.ClassOpts[*TestObject]{
		Typed: &TestObject{},
	})

	testObjClass.Install(
		dagql.Field[*TestObject]{
			Spec: &dagql.FieldSpec{
				Name: "value",
				Type: dagql.Int(0),
			},
			Func: func(ctx context.Context, self dagql.ObjectResult[*TestObject], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
				return dagql.NewResultForCurrentID(ctx, dagql.Int(self.Self().Value))
			},
		},
		dagql.Field[*TestObject]{
			Spec: &dagql.FieldSpec{
				Name: "text",
				Type: dagql.String(""),
			},
			Func: func(ctx context.Context, self dagql.ObjectResult[*TestObject], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
				return dagql.NewResultForCurrentID(ctx, dagql.String(self.Self().Text))
			},
		},
		dagql.Field[*TestObject]{
			Spec: &dagql.FieldSpec{
				Name: "nullableField",
				Type: dagql.Null[dagql.String](),
			},
			Func: func(ctx context.Context, self dagql.ObjectResult[*TestObject], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
				if self.Self().NullableField == nil {
					return dagql.NewResultForCurrentID(ctx, dagql.Null[dagql.String]())
				}
				return dagql.NewResultForCurrentID(ctx, dagql.String(*self.Self().NullableField))
			},
		},
	)
	srv.InstallObject(testObjClass)

	// Install NestedObject
	nestedObjClass := dagql.NewClass(srv, dagql.ClassOpts[*NestedObject]{
		Typed: &NestedObject{},
	})

	nestedObjClass.Install(
		dagql.Field[*NestedObject]{
			Spec: &dagql.FieldSpec{
				Name: "name",
				Type: dagql.String(""),
			},
			Func: func(ctx context.Context, self dagql.ObjectResult[*NestedObject], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
				return dagql.NewResultForCurrentID(ctx, dagql.String(self.Self().Name))
			},
		},
		dagql.Field[*NestedObject]{
			Spec: &dagql.FieldSpec{
				Name: "inner",
				Type: &TestObject{},
			},
			Func: func(ctx context.Context, self dagql.ObjectResult[*NestedObject], args map[string]dagql.Input, view call.View) (dagql.AnyResult, error) {
				return dagql.NewResultForCurrentID(ctx, self.Self().Inner)
			},
		},
	)
	srv.InstallObject(nestedObjClass)
}

type testInstallHook struct {
	Server *dagql.Server
}

type renamedType struct {
	dagql.ObjectType
	Name string
}

func (tp renamedType) TypeName() string {
	return tp.Name
}

func (hook *testInstallHook) InstallObject(class dagql.ObjectType) {
	if strings.HasSuffix(class.TypeName(), "Other") {
		return
	}

	// test extending a field
	class.Extend(
		dagql.FieldSpec{
			Name: "hello",
			Type: dagql.String(""),
		},
		func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
			return dagql.NewResultForCurrentID(ctx, dagql.String("hello world!"))
		},
	)

	// test adding a new type
	classOther := renamedType{class, class.TypeName() + "Other"}
	hook.Server.InstallObject(classOther)
	hook.Server.Root().ObjectType().Extend(
		dagql.FieldSpec{
			Name: "other" + class.TypeName(),
			Type: classOther.Typed(),
		},
		func(ctx context.Context, self dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
			return dagql.NewResultForCurrentID(ctx, &points.Point{X: 100, Y: 200})
		},
	)
}

func TestInstallHooks(t *testing.T) {
	srv := dagql.NewServer(Query{}, newCache(t))
	srv.AddInstallHook(&testInstallHook{srv})
	points.Install[Query](srv)

	gql := client.New(dagql.NewDefaultHandler(srv))
	var res struct {
		Point struct {
			X, Y  int
			Hello string
		}
		OtherPoint struct {
			X, Y  int
			Hello string
		}
	}
	req(t, gql, `query {
		point(x: 6, y: 7) {
			x
			y
			hello
		}
		otherPoint {
			x
			y
			hello
		}
	}`, &res)

	require.Equal(t, 6, res.Point.X)
	require.Equal(t, 7, res.Point.Y)
	require.Equal(t, "hello world!", res.Point.Hello)

	require.Equal(t, 100, res.OtherPoint.X)
	require.Equal(t, 200, res.OtherPoint.Y)
	require.Equal(t, "hello world!", res.OtherPoint.Hello)
}
