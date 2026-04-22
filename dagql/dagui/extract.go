package dagui

import (
	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// extractIntoDAG recursively populates dag.CallsByDigest from the call and its dependencies.
func extractIntoDAG(dag *callpbv1.DAG, db *DB, callDigest string) {
	if callDigest == "" {
		return
	}
	if _, exists := dag.CallsByDigest[callDigest]; exists {
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
	dag.CallsByDigest[callDigest] = call

	if call.ReceiverDigest != "" {
		extractIntoDAG(dag, db, call.ReceiverDigest)
	}
	for _, arg := range call.Args {
		if arg.Value != nil {
			extractLitIntoDAG(dag, db, arg.Value)
		}
	}
	if call.Module != nil && call.Module.CallDigest != "" {
		extractIntoDAG(dag, db, call.Module.CallDigest)
	}
}

// extractLitIntoDAG recursively extracts calls from literals.
func extractLitIntoDAG(dag *callpbv1.DAG, db *DB, lit *callpbv1.Literal) {
	switch v := lit.Value.(type) {
	case *callpbv1.Literal_CallDigest:
		extractIntoDAG(dag, db, v.CallDigest)
	case *callpbv1.Literal_List:
		if v.List != nil {
			for _, val := range v.List.Values {
				extractLitIntoDAG(dag, db, val)
			}
		}
	case *callpbv1.Literal_Object:
		if v.Object != nil {
			for _, val := range v.Object.Values {
				if val.Value != nil {
					extractLitIntoDAG(dag, db, val.Value)
				}
			}
		}
	default:
		// Other literal types do not reference calls, so ignore.
	}
}
