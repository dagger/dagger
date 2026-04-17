package dagql_test

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

func mustID(t testing.TB, idable interface{ ID() (*call.ID, error) }) *call.ID {
	t.Helper()
	id, err := idable.ID()
	if err != nil {
		t.Fatalf("ID(): %v", err)
	}
	return id
}

func mustRecipeID(t testing.TB, ctx context.Context, idable interface {
	RecipeID(context.Context) (*call.ID, error)
}) *call.ID {
	t.Helper()
	id, err := idable.RecipeID(ctx)
	if err != nil {
		t.Fatalf("RecipeID(): %v", err)
	}
	return id
}

func testCall(id *call.ID) *dagql.ResultCall {
	if id == nil {
		return nil
	}
	return &dagql.ResultCall{
		Kind:         dagql.ResultCallKindField,
		Type:         dagql.NewResultCallType(id.Type().ToAST()),
		Field:        id.Field(),
		View:         id.View(),
		Nth:          id.Nth(),
		EffectIDs:    id.EffectIDs(),
		ExtraDigests: id.ExtraDigests(),
	}
}
