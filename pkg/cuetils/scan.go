package cuetils

import (
	"cuelang.org/go/cue"
)

// ScanForInputs walks a Value looking for potential inputs
// - non-concrete values or values with defaults
// - exclude @dagger(computed) and #up
// - exclude values which have references
func ScanForInputs(value cue.Value) ([]cue.Value, error) {
	var (
		vals []cue.Value
		err  error
	)

	// walk before function, bool return true if the walk should recurse again
	before := func(v cue.Value) (bool, error) {
		// inference phase
		switch v.IncompleteKind() {
		case cue.StructKind:
			return true, nil

		case cue.ListKind:
			if !v.IsConcrete() && isInput(v) {
				vals = append(vals, v)
				return false, nil
			}
			return true, nil

		default:

			if !isInput(v) {
				// Not an input
				return false, nil
			}

			// a leaf with default?
			_, has := v.Default()
			if has {
				vals = append(vals, v)
				// recurse here?
				return false, nil
			}

			// is this leaf not concrete? (should cause an error)
			if v.Validate(cue.Concrete(true), cue.Optional(true)) != nil {
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

func isInput(v cue.Value) bool {
	attrs := v.Attributes(cue.ValueAttr)

	for _, attr := range attrs {
		name := attr.Name()
		// match `@dagger(...)`
		if name == "dagger" {
			// loop over args (CSV content in attribute)
			for i := 0; i < attr.NumArgs(); i++ {
				key, _ := attr.Arg(i)

				// we found an explicit input value
				if key == "input" {
					return true
				}
			}
		}
	}

	return false
}

// walkValue is a custome walk function so that we recurse into more types than CUE's buildin walk
// specificially, we need to customize the options to val.Fields when val is a struct
func walkValue(val cue.Value, before func(cue.Value) (bool, error), after func(cue.Value) error) error {
	if before != nil {
		recurse, err := before(val)
		if err != nil {
			return err
		}

		// should we recurse into fields
		if recurse {
			switch val.IncompleteKind() {
			case cue.StructKind:
				// provide custom args to ensure we walk nested defs
				// and that optionals are included
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
