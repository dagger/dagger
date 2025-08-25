package dagui

import "github.com/dagger/dagger/dagql/call/callpbv1"

// extractIntoDAG recursively populates dag.CallsByDigest from the call and its dependencies.
func extractIntoDAG(dag *callpbv1.DAG, db *DB, call *callpbv1.Call) {
	if _, exists := dag.CallsByDigest[call.Digest]; exists {
		return
	}
	dag.CallsByDigest[call.Digest] = call
	if call.ReceiverDigest != "" {
		if recv := db.Call(call.ReceiverDigest); recv != nil {
			extractIntoDAG(dag, db, recv)
		}
	}
	for _, arg := range call.Args {
		if arg.Value != nil {
			extractLitIntoDAG(dag, db, arg.Value)
		}
	}
	if call.Module != nil && call.Module.CallDigest != "" {
		if mod := db.Call(call.Module.CallDigest); mod != nil {
			extractIntoDAG(dag, db, mod)
		}
	}
}

// extractLitIntoDAG recursively extracts calls from literals.
func extractLitIntoDAG(dag *callpbv1.DAG, db *DB, lit *callpbv1.Literal) {
	switch v := lit.Value.(type) {
	case *callpbv1.Literal_CallDigest:
		if v.CallDigest != "" {
			if call := db.Call(v.CallDigest); call != nil {
				extractIntoDAG(dag, db, call)
			}
		}
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
