package cuetils

import (
	"cuelang.org/go/cue"
)

func InferInputs(value cue.Value) ([]cue.Value, error) {
	var (
		vals []cue.Value
		err error
	)

	// walk function
	before := func(v cue.Value) (bool, error) {
		// explicit phase
		// look for #up
		label, _ := v.Label()
		if label == "#up" {
			return false, nil
		}

		// look for @dagger(input/computed)
		attrs := v.Attributes(cue.ValueAttr)
		for _, attr := range attrs {
			name :=  attr.Name()
			// match `@dagger(...)`
			if name == "dagger" {
				// loop over args (CSV content in attribute)
				for i := 0; i < attr.NumArgs(); i++ {
					key, _ := attr.Arg(i)
					// we found an explicit computed value
					if key == "computed" {
						return false, nil
					}
					// we found an explicit input
					if key == "input" {
						vals = append(vals, v)
						return false, nil
					}
				}
			}
		}

		// inferrence phase
		switch v.IncompleteKind() {

			case cue.StructKind:
				return true, nil

			case cue.ListKind:
				if !v.IsConcrete() {
					vals = append(vals, v)
					return false, nil
				}
				return true, nil

			default:

				// a leaf with default?
				_, has := v.Default()
				if has {
					vals = append(vals, v)
					// recurse here?
					return false, nil
				}

				// is this leaf not concrete?
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

