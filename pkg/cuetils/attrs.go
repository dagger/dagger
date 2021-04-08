package cuetils

import (
	"cuelang.org/go/cue"
)

// FindsAttributes returns CUE values with @daggger(key) as long as any key is found.
func FindAttributes(value cue.Value, keys []string) ([]cue.Value, error) {

	var (
		vals []cue.Value
		err error
	)

	// walk function
	before := func(v cue.Value) (bool, error) {
		attrs := v.Attributes(cue.ValueAttr)
		for _, attr := range attrs {
			name :=  attr.Name()
			// match `@dagger(...)`
			if name == "dagger" {
				// loop over args (CSV content in attribute)
				for i := 0; i < attr.NumArgs(); i++ {
					kA, _ := attr.Arg(i)
					for _, key := range keys {
						if kA == key {
							vals = append(vals, v)
							// we found a match, stop processing by returning from before
							return true, nil
						}
					}
				}
			}
		}
		return true, nil
	}

	// walk
	err = walkValue(value, before, nil)
	if err != nil {
		return nil, err
	}

	return vals, nil
}

func InferInputs(value cue.Value) ([]cue.Value, error) {
	var (
		vals []cue.Value
		err error
	)

	// walk function
	before := func(v cue.Value) (bool, error) {
		switch v.IncompleteKind() {
			case cue.StructKind, cue.ListKind:
				return true, nil

			default:

				// is this leaf not concrete?
				if v.Validate(cue.Concrete(true)) != nil {

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

