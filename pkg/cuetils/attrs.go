package cuetils

import (
	"fmt"
	"cuelang.org/go/cue"
)

func InferInputs(value cue.Value) ([]cue.Value, error) {
	var (
		vals []cue.Value
		err error
	)

	// walk function
	before := func(v cue.Value) (bool, error) {
		switch v.IncompleteKind() {

			case cue.StructKind:
				return true, nil

			case cue.ListKind:
				l, _ := v.Label()

				if !v.IsConcrete() {
					vals = append(vals, v)
					return false, nil
				}
				fmt.Println("List:", l, v, v.IsConcrete())
				return true, nil

			default:

				// is this leaf not concrete?
				if v.Validate(cue.Concrete(true)) != nil {
					fmt.Println("Non-concrete:")
					L, _ := v.Label()
					fmt.Println(L, v)

					// look for @dagger(computed)
					attrs := v.Attributes(cue.ValueAttr)
					for _, attr := range attrs {
						if attr.Name() == "dagger" {
							for i := 0; i < attr.NumArgs(); i++ {
								kA, _ := attr.Arg(i)
								// found, so omit, and don't continue the walk
								if kA == "computed" {
									return false, nil
								}
							}
						}
					}

					i,p := v.Reference()
					fmt.Println("Ref:", i,p)

					// look for reference to some other field
					o,l := v.Expr()
					fmt.Println("Expr:", o,l)
					for _, ex := range l {
						ei, ep := ex.Reference()
						fmt.Println("-", ei, ep)
					}

					// If we make it here, then @dagger(computed) is NOT on this value, so add it
					vals = append(vals, v)

				}

				return false, nil
		}
	}

	// walk
	err = walkValue(value, before, nil)
	if err != nil {
		return nil, err
	}

	return vals, nil
}

func walkValue(val cue.Value, before func (cue.Value) (bool, error), after func(cue.Value) error) error {

	if before != nil {

		recurse, err := before(val)
		if err != nil {
			return err
		}

		// should we recurse into fields
		if recurse {

			switch val.IncompleteKind() {
				case cue.StructKind:
					iter, err := val.Fields(
						cue.Definitions(true),
						cue.Optional(true),
					)
					if err != nil {
						return err
					}
					for iter.Next() {
						err := walkValue(iter.Value(), before, after)
						if err != nil {
							return err
						}
					}

				case cue.ListKind:
					iter, err := val.List()
					if err != nil {
						return err
					}
					for iter.Next() {
						err := walkValue(iter.Value(), before, after)
						if err != nil {
							return err
						}
					}

			}

		}
	}

	if after != nil {
		err := after(val)
		if err != nil {
			return err
		}
	}

	return nil
}

