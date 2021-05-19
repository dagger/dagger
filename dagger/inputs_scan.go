package dagger

import (
	"fmt"

	"cuelang.org/go/cue"
	"dagger.io/go/dagger/compiler"
)

// func isReference(val cue.Value) bool {
// 	_, ref := val.ReferencePath()
// 	isRef := len(ref.Selectors()) > 0

// 	if isRef {
// 		return true
// 	}

// 	_, vals := val.Expr()
// 	for _, v := range vals {
// 		// walk recursively
// 		if v.Path().String() == val.Path().String() {
// 			// avoid loop by checking the same value
// 			continue
// 		}
// 		return isReference(v)
// 	}

// 	return isRef
// }

// walk recursively to find references
// func isReference(val cue.Value) bool {
// 	_, vals := val.Expr()
// 	for _, v := range vals {
// 		// walk recursively
// 		if v.Path().String() == val.Path().String() {
// 			// avoid loop by checking the same value
// 			continue
// 		}
// 		return isReference(v)
// 	}

// 	_, ref := val.ReferencePath()
// 	return len(ref.Selectors()) > 0
// }

func isReference(val cue.Value) bool {
	checkRef := func(vv cue.Value) bool {
		_, ref := vv.ReferencePath()
		return len(ref.Selectors()) > 0
	}

	_, vals := val.Expr()
	for _, v := range vals {
		if checkRef(v) {
			return true
		}
	}

	return checkRef(val)
}

func ScanInputs(value *compiler.Value) ([]*compiler.Value, error) {
	vals := []*compiler.Value{}

	value.Walk(
		func(val *compiler.Value) bool {
			// if isReference(val.Cue()) {
			// 	fmt.Println("### isReference ->", val.Cue().Path())
			// 	return false
			// }

			if val.HasAttr("input") && !isReference(val.Cue()) {
				fmt.Printf("#### FOUND: %s\n", val.Path())
				vals = append(vals, val)
			}
			return true
		}, nil,
	)

	return vals, nil
}
