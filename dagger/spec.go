package dagger

import (
	cueerrors "cuelang.org/go/cue/errors"
	"github.com/pkg/errors"

	"dagger.cloud/go/dagger/cc"
)

var (
	// Global shared dagger spec, generated from spec.cue
	spec = NewSpec()
)

// Cue spec validator
type Spec struct {
	root *cc.Value
}

func NewSpec() *Spec {
	v, err := cc.Compile("spec.cue", DaggerSpec)
	if err != nil {
		panic(err)
	}
	if _, err := v.Struct(); err != nil {
		panic(err)
	}
	return &Spec{
		root: v,
	}
}

// eg. Validate(op, "#Op")
func (s Spec) Validate(v *cc.Value, defpath string) error {
	// Lookup def by name, eg. "#Script" or "#Copy"
	// See dagger/spec.cue
	def := s.root.Get(defpath)
	if err := def.Fill(v); err != nil {
		return errors.New(cueerrors.Details(err, nil))
	}

	return nil
}

func (s Spec) Match(v *cc.Value, defpath string) bool {
	return s.Validate(v, defpath) == nil
}

func (s Spec) Get(target string) *cc.Value {
	return s.root.Get(target)
}
