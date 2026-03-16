package dagql_test

import (
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

func mustRecipeID(t testing.TB, idable interface{ RecipeID() (*call.ID, error) }) *call.ID {
	t.Helper()
	id, err := idable.RecipeID()
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
