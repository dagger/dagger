package cc

import (
	"sync"

	"cuelang.org/go/cue"
)

// Polyfill for a cue runtime
// (we call it compiler to avoid confusion with dagger runtime)
// Use this instead of cue.Runtime
type Compiler struct {
	sync.RWMutex
	cue.Runtime
}

func (cc *Compiler) Cue() *cue.Runtime {
	return &(cc.Runtime)
}

// Compile an empty struct
func (cc *Compiler) EmptyStruct() (*Value, error) {
	return cc.Compile("", "")
}

func (cc *Compiler) Compile(name string, src interface{}) (*Value, error) {
	cc.Lock()
	defer cc.Unlock()

	inst, err := cc.Cue().Compile(name, src)
	if err != nil {
		// FIXME: cleaner way to unwrap cue error details?
		return nil, Err(err)
	}
	return cc.Wrap(inst.Value(), inst), nil
}

func (cc *Compiler) Wrap(v cue.Value, inst *cue.Instance) *Value {
	return wrapValue(v, inst)
}
