package compiler

import (
	"errors"
	"fmt"
	"sync"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
	cuejson "cuelang.org/go/encoding/json"
	cueyaml "cuelang.org/go/encoding/yaml"
)

var (
	// DefaultCompiler is the default Compiler and is used by Compile
	DefaultCompiler = &Compiler{}
)

func Compile(name string, src interface{}) (*Value, error) {
	return DefaultCompiler.Compile(name, src)
}

func NewValue() *Value {
	return DefaultCompiler.NewValue()
}

// FIXME can be refactored away now?
func Wrap(v cue.Value, inst *cue.Instance) *Value {
	return DefaultCompiler.Wrap(v, inst)
}

func InstanceMerge(src ...interface{}) (*Value, error) {
	return DefaultCompiler.InstanceMerge(src...)
}

func Cue() *cue.Runtime {
	return DefaultCompiler.Cue()
}

func Err(err error) error {
	return DefaultCompiler.Err(err)
}

func DecodeJSON(path string, data []byte) (*Value, error) {
	return DefaultCompiler.DecodeJSON(path, data)
}

func DecodeYAML(path string, data []byte) (*Value, error) {
	return DefaultCompiler.DecodeYAML(path, data)
}

// Polyfill for a cue runtime
// (we call it compiler to avoid confusion with dagger runtime)
// Use this instead of cue.Runtime
type Compiler struct {
	l sync.RWMutex
	cue.Runtime
}

func (c *Compiler) lock() {
	c.l.Lock()
}

func (c *Compiler) unlock() {
	c.l.Unlock()
}

func (c *Compiler) rlock() {
	c.l.RLock()
}

func (c *Compiler) runlock() {
	c.l.RUnlock()
}

func (c *Compiler) Cue() *cue.Runtime {
	return &(c.Runtime)
}

// Compile an empty value
func (c *Compiler) NewValue() *Value {
	empty, err := c.Compile("", "_")
	if err != nil {
		panic(err)
	}
	return empty
}

func (c *Compiler) Compile(name string, src interface{}) (*Value, error) {
	c.lock()
	defer c.unlock()

	inst, err := c.Cue().Compile(name, src)
	if err != nil {
		// FIXME: cleaner way to unwrap cue error details?
		return nil, Err(err)
	}
	return c.Wrap(inst.Value(), inst), nil
}

// InstanceMerge merges multiple values and mirrors the value in the cue.Instance.
// FIXME: AVOID THIS AT ALL COST
// Special case: we must return an instance with the same
// contents as v, for the purposes of cueflow.
func (c *Compiler) InstanceMerge(src ...interface{}) (*Value, error) {
	var (
		v    = c.NewValue()
		inst = v.CueInst()
		err  error
	)

	c.lock()
	defer c.unlock()

	for _, s := range src {
		// If calling Fill() with a Value, we want to use the underlying
		// cue.Value to fill.
		if val, ok := s.(*Value); ok {
			inst, err = inst.Fill(val.val)
			if err != nil {
				return nil, fmt.Errorf("merge failed: %w", err)
			}
		} else {
			inst, err = inst.Fill(s)
			if err != nil {
				return nil, fmt.Errorf("merge failed: %w", err)
			}
		}
	}

	v = c.Wrap(inst.Value(), inst)
	return v, nil
}

func (c *Compiler) DecodeJSON(path string, data []byte) (*Value, error) {
	inst, err := cuejson.Decode(c.Cue(), path, data)
	if err != nil {
		return nil, Err(err)
	}
	return c.Wrap(inst.Value(), inst), nil
}

func (c *Compiler) DecodeYAML(path string, data []byte) (*Value, error) {
	inst, err := cueyaml.Decode(c.Cue(), path, data)
	if err != nil {
		return nil, Err(err)
	}
	return c.Wrap(inst.Value(), inst), nil
}

func (c *Compiler) Wrap(v cue.Value, inst *cue.Instance) *Value {
	return wrapValue(v, inst, c)
}

func (c *Compiler) Err(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(cueerrors.Details(err, &cueerrors.Config{}))
}
