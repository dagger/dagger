package compiler

import (
	"errors"
	"sync"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cueerrors "cuelang.org/go/cue/errors"
	cuejson "cuelang.org/go/encoding/json"
	cueyaml "cuelang.org/go/encoding/yaml"
)

var (
	// DefaultCompiler is the default Compiler and is used by Compile
	DefaultCompiler = New()
)

func Compile(name string, src string) (*Value, error) {
	return DefaultCompiler.Compile(name, src)
}

func NewValue() *Value {
	return DefaultCompiler.NewValue()
}

// FIXME can be refactored away now?
func Wrap(v cue.Value) *Value {
	return DefaultCompiler.Wrap(v)
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
	*cue.Context
}

func New() *Compiler {
	return &Compiler{
		Context: cuecontext.New(),
	}
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

// Compile an empty value
func (c *Compiler) NewValue() *Value {
	empty, err := c.Compile("", "_")
	if err != nil {
		panic(err)
	}
	return empty
}

func (c *Compiler) Compile(name string, src string) (*Value, error) {
	c.lock()
	defer c.unlock()

	v := c.Context.CompileString(src, cue.Filename(name))
	if v.Err() != nil {
		// FIXME: cleaner way to unwrap cue error details?
		return nil, Err(v.Err())
	}
	return c.Wrap(v), nil
}

func (c *Compiler) DecodeJSON(path string, data []byte) (*Value, error) {
	expr, err := cuejson.Extract(path, data)
	if err != nil {
		return nil, Err(err)
	}
	v := c.Context.BuildExpr(expr, cue.Filename(path))
	if err := v.Err(); err != nil {
		return nil, Err(err)
	}
	return c.Wrap(v), nil
}

func (c *Compiler) DecodeYAML(path string, data []byte) (*Value, error) {
	f, err := cueyaml.Extract(path, data)
	if err != nil {
		return nil, Err(err)
	}
	v := c.Context.BuildFile(f, cue.Filename(path))
	if err := v.Err(); err != nil {
		return nil, Err(err)
	}
	return c.Wrap(v), nil
}

func (c *Compiler) Wrap(v cue.Value) *Value {
	return &Value{
		val: v,
		cc:  c,
	}
}

func (c *Compiler) Err(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(cueerrors.Details(err, &cueerrors.Config{}))
}
