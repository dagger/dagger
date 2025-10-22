package dagql

import (
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestVisitID(t *testing.T) {
	pointT := &ast.Type{
		NamedType: "Point",
		NonNull:   true,
	}

	id := call.New().
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
		Append(pointT, "shiftLeft").
		Append(pointT, "add", call.WithArgs(
			call.NewArgument("other", call.NewLiteralID(call.New().
				Append(pointT, "point", call.WithArgs(
					call.NewArgument("x", call.NewLiteralInt(1), false),
					call.NewArgument("y", call.NewLiteralInt(2), false),
				))), false),
		)).
		Append(ast.NamedType("Int", nil), "length")

	t.Run("no-op", func(t *testing.T) {
		rewrittenID, err := VisitID(id, func(id *call.ID) (*call.ID, error) {
			return nil, nil
		})
		require.NoError(t, err)
		require.Equal(t, id, rewrittenID)
	})

	t.Run("change field name", func(t *testing.T) {
		rewrittenID, err := VisitID(id, func(id *call.ID) (*call.ID, error) {
			if id.Receiver() == nil && id.Field() == "point" {
				newID := call.New().
					Append(pointT, "pointRenamed", call.WithArgs(
						call.NewArgument("x", id.Arg("x").Value(), false),
						call.NewArgument("y", id.Arg("y").Value(), false),
					))
				return newID, nil
			}
			return nil, nil
		})
		require.NoError(t, err)

		expectedID := call.New().
			Append(pointT, "pointRenamed", call.WithArgs(
				call.NewArgument("x", call.NewLiteralInt(6), false),
				call.NewArgument("y", call.NewLiteralInt(7), false),
			)).
			Append(pointT, "shiftLeft").
			Append(pointT, "add", call.WithArgs(
				call.NewArgument("other", call.NewLiteralID(call.New().
					Append(pointT, "pointRenamed", call.WithArgs(
						call.NewArgument("x", call.NewLiteralInt(1), false),
						call.NewArgument("y", call.NewLiteralInt(2), false),
					))), false),
			)).
			Append(ast.NamedType("Int", nil), "length")

		require.Equal(t, expectedID.Display(), rewrittenID.Display())

		require.NotEqual(t, id.Digest(), rewrittenID.Digest())
		require.Equal(t, expectedID.Digest(), rewrittenID.Digest())
	})

	t.Run("change arg value", func(t *testing.T) {
		rewrittenID, err := VisitID(id, func(id *call.ID) (*call.ID, error) {
			if id.Receiver() == nil && id.Field() == "point" {
				xArg := id.Arg("x")
				yArg := id.Arg("y")
				if xArg != nil && yArg != nil {
					xLit, ok := xArg.Value().(*call.LiteralInt)
					if !ok {
						return nil, nil
					}
					yLit, ok := yArg.Value().(*call.LiteralInt)
					if !ok {
						return nil, nil
					}
					newX := call.NewLiteralInt(xLit.Value() + 100)
					newY := call.NewLiteralInt(yLit.Value() + 200)

					newID := id.
						WithArgument(call.NewArgument("x", newX, false)).
						WithArgument(call.NewArgument("y", newY, false))
					return newID, nil
				}
			}
			return nil, nil
		})
		require.NoError(t, err)

		expectedID := call.New().
			Append(pointT, "point", call.WithArgs(
				call.NewArgument("x", call.NewLiteralInt(106), false),
				call.NewArgument("y", call.NewLiteralInt(207), false),
			)).
			Append(pointT, "shiftLeft").
			Append(pointT, "add", call.WithArgs(
				call.NewArgument("other", call.NewLiteralID(call.New().
					Append(pointT, "point", call.WithArgs(
						call.NewArgument("x", call.NewLiteralInt(101), false),
						call.NewArgument("y", call.NewLiteralInt(202), false),
					))), false),
			)).
			Append(ast.NamedType("Int", nil), "length")

		require.Equal(t, expectedID.Display(), rewrittenID.Display())

		require.NotEqual(t, id.Digest(), rewrittenID.Digest())
		require.Equal(t, expectedID.Digest(), rewrittenID.Digest())
	})
}
