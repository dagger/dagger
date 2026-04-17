package dagui

import (
	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// extractIntoDAG recursively populates recipe.CallsByDigest from the call and its dependencies.
func extractIntoDAG(recipe *callpbv1.RecipeDAG, db *DB, callDigest string) {
	if callDigest == "" {
		return
	}
	if _, exists := recipe.CallsByDigest[callDigest]; exists {
		return
	}

	call := db.Call(callDigest)
	if call == nil {
		return
	}
	call = &callpbv1.Call{
		ReceiverDigest: call.ReceiverDigest,
		Type:           call.Type,
		Field:          call.Field,
		Args:           call.Args,
		Nth:            call.Nth,
		Module:         call.Module,
		Digest:         callDigest,
		View:           call.View,
	}
	recipe.CallsByDigest[callDigest] = call

	if call.ReceiverDigest != "" {
		extractIntoDAG(recipe, db, call.ReceiverDigest)
	}
	for _, arg := range call.Args {
		if arg.Value != nil {
			extractLitIntoDAG(recipe, db, arg.Value)
		}
	}
	if call.Module != nil && call.Module.CallDigest != "" {
		extractIntoDAG(recipe, db, call.Module.CallDigest)
	}
}

// extractLitIntoDAG recursively extracts calls from literals.
func extractLitIntoDAG(recipe *callpbv1.RecipeDAG, db *DB, lit *callpbv1.Literal) {
	switch v := lit.Value.(type) {
	case *callpbv1.Literal_CallDigest:
		extractIntoDAG(recipe, db, v.CallDigest)
	case *callpbv1.Literal_List:
		if v.List != nil {
			for _, val := range v.List.Values {
				extractLitIntoDAG(recipe, db, val)
			}
		}
	case *callpbv1.Literal_Object:
		if v.Object != nil {
			for _, val := range v.Object.Values {
				if val.Value != nil {
					extractLitIntoDAG(recipe, db, val.Value)
				}
			}
		}
	default:
		// Other literal types do not reference calls, so ignore.
	}
}
