package cuetils

import (
	"fmt"

	"cuelang.org/go/cue"
)

// FindsAttributes returns CUE values with @daggger(key) as long as any key is found.
func FindAttributes(value cue.Value, keys []string) ([]cue.Value, error) {

	var err error

	// Validate the value
	err = value.Validate()
	if err != nil {
		fmt.Println("Error during validate:", err)
		return nil, err
	}

	var vals []cue.Value

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

func walkValue(val cue.Value, before func (cue.Value) (bool, error), after func(cue.Value) error) error {

	if before != nil {

		recurse, err := before(val)
		if err != nil {
			return err
		}

		// should we recurse into fields
		if recurse {

			// is val a struct?
			if _, err := val.Struct(); err == nil {

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

