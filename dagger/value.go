package dagger

import (
	"fmt"
	"os"

	"cuelang.org/go/cue"
	cueformat "cuelang.org/go/cue/format"
	"github.com/moby/buildkit/client/llb"
)

// Polyfill for cue.Value.
// Use instead of cue.Value and cue.Instance
type Value struct {
	// FIXME: don't embed, cleaner API
	cue.Value
	cc   *Compiler
	inst *cue.Instance
}

func (v *Value) Lock() {
	if v.cc == nil {
		return
	}
	v.cc.Lock()
}

func (v *Value) Unlock() {
	if v.cc == nil {
		return
	}
	v.cc.Unlock()
}

func (v *Value) Lookup(path ...string) *Value {
	v.Lock()
	defer v.Unlock()

	return v.Wrap(v.Unwrap().LookupPath(cueStringsToCuePath(path...)))
}

func (v *Value) LookupPath(p cue.Path) *Value {
	v.Lock()
	defer v.Unlock()
	return v.Wrap(v.Unwrap().LookupPath(p))
}

// FIXME: deprecated by Get()
func (v *Value) LookupTarget(target string) *Value {
	return v.LookupPath(cue.ParsePath(target))
}

func (v *Value) Get(target string) *Value {
	return v.LookupPath(cue.ParsePath(target))
}

// Component returns the component value if v is a valid dagger component or an error otherwise.
// If no '#dagger' annotation is present, os.ErrNotExist
// is returned.
func (v *Value) Component() (*Component, error) {
	c := &Component{
		v: v,
	}
	if !c.Exists() {
		return c, os.ErrNotExist
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

func (v *Value) Script() (*Script, error) {
	s := &Script{
		v: v,
	}
	if err := s.Validate(); err != nil {
		return s, err
	}
	return s, nil
}

func (v *Value) Executable() (Executable, error) {
	if script, err := v.Script(); err == nil {
		return script, nil
	}
	if component, err := v.Component(); err == nil {
		return component, nil
	}
	if op, err := v.Op(); err == nil {
		return op, nil
	}
	return nil, fmt.Errorf("value is not executable")
}

// ScriptOrComponent returns one of:
//  (1) the component value if v is a valid component (type *Component)
//  (2) the script value if v is a valid script (type *Script)
//  (3) an error otherwise
func (v *Value) ScriptOrComponent() (interface{}, error) {
	s, err := v.Script()
	if err == nil {
		return s, nil
	}
	c, err := v.Component()
	if err == nil {
		return c, nil
	}
	return nil, fmt.Errorf("not a script or component")
}

func (v *Value) Op() (*Op, error) {
	// Merge #Op definition from spec to get default values
	spec, err := v.Compiler().Spec()
	if err != nil {
		return nil, err
	}
	v, err = spec.Get("#Op").Merge(v)
	if err != nil {
		return nil, err
	}
	op := &Op{
		v: v,
	}
	return op, nil
}

func (v *Value) Mount(dest string) (*Mount, error) {
	mnt := &Mount{
		v:    v,
		dest: dest,
	}
	return mnt, mnt.Validate()
}

// Interpret this value as a spec
func (v *Value) Spec() (*Spec, error) {
	// Spec must be a struct
	if _, err := v.Struct(); err != nil {
		return nil, err
	}
	return &Spec{
		root: v,
	}, nil
}

// FIXME: receive string path?
func (v *Value) Merge(x interface{}, path ...string) (*Value, error) {
	if xval, ok := x.(*Value); ok {
		if xval.Compiler() != v.Compiler() {
			return nil, fmt.Errorf("can't merge values from different compilers")
		}
		x = xval.Unwrap()
	}
	result := v.Wrap(v.Unwrap().Fill(x, path...))
	return result, result.Validate()
}

func (v *Value) MergePath(x interface{}, p cue.Path) (*Value, error) {
	// FIXME: array indexes and defs are not supported,
	//  they will be silently converted to regular fields.
	//  eg.  `foo.#bar[0]` will become `foo["#bar"]["0"]`
	return v.Merge(x, cuePathToStrings(p)...)
}

func (v *Value) MergeTarget(x interface{}, target string) (*Value, error) {
	return v.MergePath(x, cue.ParsePath(target))
}

func (v *Value) RangeList(fn func(int, *Value) error) error {
	it, err := v.List()
	if err != nil {
		return err
	}
	i := 0
	for it.Next() {
		if err := fn(i, v.Wrap(it.Value())); err != nil {
			return err
		}
		i++
	}
	return nil
}

func (v *Value) RangeStruct(fn func(string, *Value) error) error {
	it, err := v.Fields()
	if err != nil {
		return err
	}
	for it.Next() {
		if err := fn(it.Label(), v.Wrap(it.Value())); err != nil {
			return err
		}
	}
	return nil
}

// Recursive concreteness check.
// Return false if v is not concrete, or contains any
// non-concrete fields or items.
func (v *Value) IsConcreteR() bool {
	// FIXME: use Value.Walk
	if it, err := v.Fields(); err == nil {
		for it.Next() {
			w := v.Wrap(it.Value())
			if !w.IsConcreteR() {
				return false
			}
		}
		return true
	}
	if it, err := v.List(); err == nil {
		for it.Next() {
			w := v.Wrap(it.Value())
			if !w.IsConcreteR() {
				return false
			}
		}
		return true
	}
	dv, _ := v.Default()
	return v.IsConcrete() || dv.IsConcrete()
}

// Export concrete values to JSON. ignoring non-concrete values.
// Contrast with cue.Value.MarshalJSON which requires all values
// to be concrete.
func (v *Value) JSON() JSON {
	v.Lock()
	defer v.Unlock()
	var out JSON
	v.Walk(
		func(v cue.Value) bool {
			b, err := v.MarshalJSON()
			if err == nil {
				newOut, err := out.Set(b, cuePathToStrings(v.Path())...)
				if err == nil {
					out = newOut
				}
				return false
			}
			return true
		},
		nil,
	)
	return out
}

func (v *Value) SaveJSON(fs FS, filename string) FS {
	return fs.Change(func(st llb.State) llb.State {
		return st.File(
			llb.Mkfile(filename, 0600, v.JSON()),
		)
	})
}

func (v *Value) Save(fs FS, filename string) (FS, error) {
	src, err := v.Source()
	if err != nil {
		return fs, err
	}
	return fs.Change(func(st llb.State) llb.State {
		return st.File(
			llb.Mkfile(filename, 0600, src),
		)
	}), nil
}

func (v *Value) Validate(defs ...string) error {
	if err := v.Unwrap().Validate(); err != nil {
		return err
	}
	if len(defs) == 0 {
		return nil
	}
	spec, err := v.Compiler().Spec()
	if err != nil {
		return err
	}
	for _, def := range defs {
		if err := spec.Validate(v, def); err != nil {
			return err
		}
	}
	return nil
}

// Value implements Fillable.
// This is the only method which changes the value in-place.
// FIXME this co-exists awkwardly with the rest of Value.
func (v *Value) Fill(x interface{}) error {
	v.Value = v.Value.Fill(x)
	return v.Validate()
}

func (v *Value) Source() ([]byte, error) {
	v.Lock()
	defer v.Unlock()
	return cueformat.Node(v.Eval().Syntax())
}

func (v *Value) IsEmptyStruct() bool {
	if st, err := v.Struct(); err == nil {
		if st.Len() == 0 {
			return true
		}
	}
	return false
}

func (v *Value) CueInst() *cue.Instance {
	return v.inst
}

func (v *Value) Compiler() *Compiler {
	//	if v.cc == nil {
	//		return &Compiler{}
	//	}
	return v.cc
}

func (v *Value) Debugf(msg string, args ...interface{}) {
	prefix := v.Path().String()
	args = append([]interface{}{prefix}, args...)
	debugf("%s: "+msg, args...)
}

func (v *Value) Wrap(v2 cue.Value) *Value {
	return wrapValue(v2, v.inst, v.cc)
}

func (v *Value) Unwrap() cue.Value {
	return v.Value
}

func wrapValue(v cue.Value, inst *cue.Instance, cc *Compiler) *Value {
	return &Value{
		Value: v,
		cc:    cc,
		inst:  inst,
	}
}
