package compiler

import (
	"errors"
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
	empty, err := c.Compile("", `
	{
		...
		_
	}
	`)
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
