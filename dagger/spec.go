package dagger

import (
	"fmt"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
)

// Cue spec validator
type Spec struct {
	root *Value
}

// eg. Validate(op, "#Op")
func (s Spec) Validate(v *Value, defpath string) (err error) {
	// Expand cue errors to get full details
	// FIXME: there is probably a cleaner way to do this.
	defer func() {
		if err != nil {
			//debugf("ERROR while validating %v against %v err=%q", v, defpath, err)
			err = fmt.Errorf("%s", cueerrors.Details(err, nil))
		}
	}()

	// Lookup def by name, eg. "#Script" or "#Copy"
	// See dagger/spec.cue
	def := s.root.Get(defpath)
	if err := def.Validate(); err != nil {
		return err
	}
	merged := def.Unwrap().Fill(v)
	if err := merged.Err(); err != nil {
		return err
	}
	if err := merged.Validate(cue.Final()); err != nil {
		return err
	}
	return nil
}

func (s Spec) Match(v *Value, defpath string) bool {
	return s.Validate(v, defpath) == nil
}

func (s Spec) Get(target string) *Value {
	return s.root.Get(target)
}
