package dagql_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/internal/points"
)

const recipeClassificationReason = "test: value is bound to its producing session"

func newRecipeClassificationServer(t *testing.T) *dagql.Server {
	t.Helper()
	srv := newExternalDagqlServerForTest(t, Query{})
	points.Install[Query](srv)
	dagql.Fields[Query]{
		dagql.Func("unsafePoint", func(context.Context, Query, struct{}) (*points.Point, error) {
			t.Fatal("recipe classification must not evaluate fields")
			return nil, nil
		}).NotReplayable(recipeClassificationReason),
		dagql.Func("pointFromObject", func(context.Context, Query, struct {
			Object dagql.AnyID
		}) (*points.Point, error) {
			t.Fatal("recipe classification must not evaluate fields")
			return nil, nil
		}).Args(dagql.Arg("object")),
		dagql.Func("pointFromLazyObject", func(context.Context, Query, struct {
			Object dagql.AnyID
		}) (*points.Point, error) {
			t.Fatal("recipe classification must not evaluate fields")
			return nil, nil
		}).Args(dagql.Arg("object").LazyRef()),
	}.Install(srv)
	return srv
}

func requireNotReplayableCall(t *testing.T, got dagql.RecipeClassification, want *call.ID) {
	t.Helper()
	require.NotNil(t, got.NotReplayable)
	require.Equal(t, want.Field(), got.NotReplayable.Field)
	require.Equal(t, want.Digest(), got.NotReplayable.Digest)
	require.Equal(t, recipeClassificationReason, got.NotReplayable.Reason)
}

func TestClassifyRecipeNotReplayable(t *testing.T) {
	srv := newRecipeClassificationServer(t)
	pointType := (&points.Point{}).Type()
	unsafe := call.New().Append(pointType, "unsafePoint")

	t.Run("direct", func(t *testing.T) {
		requireNotReplayableCall(t, srv.ClassifyRecipe(unsafe), unsafe)
	})

	t.Run("receiver", func(t *testing.T) {
		recipe := unsafe.Append(pointType, "shiftLeft")
		requireNotReplayableCall(t, srv.ClassifyRecipe(recipe), unsafe)
	})

	t.Run("ID argument", func(t *testing.T) {
		recipe := call.New().Append(pointType, "pointFromObject",
			call.WithArgs(call.NewArgument("object", call.NewLiteralID(unsafe), false)),
		)
		requireNotReplayableCall(t, srv.ClassifyRecipe(recipe), unsafe)
	})
}

func TestClassifyRecipeReplayableAndLazyRefs(t *testing.T) {
	srv := newRecipeClassificationServer(t)
	pointType := (&points.Point{}).Type()
	replayable := call.New().Append(pointType, "point").Append(pointType, "shiftLeft")
	require.Nil(t, srv.ClassifyRecipe(replayable).NotReplayable)

	unsafe := call.New().Append(pointType, "unsafePoint")
	lazyRecipe := call.New().Append(pointType, "pointFromLazyObject",
		call.WithArgs(call.NewArgument("object", call.NewLiteralID(unsafe), false)),
	)
	require.Nil(t, srv.ClassifyRecipe(lazyRecipe).NotReplayable,
		"lazy-ref arguments are not evaluated during recipe loading")
}

func TestClassifyRecipeUnknownFieldsAreBestEffort(t *testing.T) {
	srv := newRecipeClassificationServer(t)
	pointType := (&points.Point{}).Type()

	unknown := call.New().Append(pointType, "fieldNotInSchema")
	require.Nil(t, srv.ClassifyRecipe(unknown).NotReplayable)
	require.Nil(t, srv.ClassifyRecipe(nil).NotReplayable)

	// An unresolved field cannot declare an argument lazy, so its ID arguments
	// are traversed as normal, just as they are by recipe loading.
	unsafe := call.New().Append(pointType, "unsafePoint")
	unknownWithArg := call.New().Append(pointType, "fieldNotInSchema",
		call.WithArgs(call.NewArgument("object", call.NewLiteralID(unsafe), false)),
	)
	requireNotReplayableCall(t, srv.ClassifyRecipe(unknownWithArg), unsafe)
}

// This file also measures what the recipe loader actually does when it is forced
// to re-execute a recorded call, rather than what it is documented to do.
