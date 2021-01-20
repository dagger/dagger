package dagger

import (
	cueerrors "cuelang.org/go/cue/errors"
	"github.com/pkg/errors"
)

// Cue spec validator
type Spec struct {
	root *Value
}

// eg. Validate(op, "#Op")
func (s Spec) Validate(v *Value, defpath string) error {
	// Lookup def by name, eg. "#Script" or "#Copy"
	// See dagger/spec.cue
	def := s.root.Get(defpath)
	if err := def.Fill(v.Value); err != nil {
		return errors.New(cueerrors.Details(err, nil))
	}

	return nil
}

func (s Spec) Match(v *Value, defpath string) bool {
	return s.Validate(v, defpath) == nil
}

func (s Spec) Get(target string) *Value {
	return s.root.Get(target)
}
