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
			err = fmt.Errorf("%s", cueerrors.Details(err, nil))
		}
	}()

	def := s.root.LookupTarget(defpath)
	if err := def.Err(); err != nil {
		return err
	}
	if err := def.Unwrap().Fill(v).Validate(cue.Final()); err != nil {
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
