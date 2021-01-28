package dagger

import (
	"fmt"

	"cuelang.org/go/cue"
	cueformat "cuelang.org/go/cue/format"
	"github.com/moby/buildkit/client/llb"
)

// Value is a wrapper around cue.Value.
// Use instead of cue.Value and cue.Instance
type Value struct {
	val  cue.Value
	cc   *Compiler
	inst *cue.Instance
}

func (v *Value) CueInst() *cue.Instance {
	return v.inst
}

func (v *Value) Wrap(v2 cue.Value) *Value {
	return wrapValue(v2, v.inst, v.cc)
}

func wrapValue(v cue.Value, inst *cue.Instance, cc *Compiler) *Value {
	return &Value{
		val:  v,
		cc:   cc,
		inst: inst,
	}
}

// Fill is a concurrency safe wrapper around cue.Value.Fill()
// This is the only method which changes the value in-place.
func (v *Value) Fill(x interface{}) error {
	v.cc.Lock()
	defer v.cc.Unlock()

	// If calling Fill() with a Value, we want to use the underlying
	// cue.Value to fill.
	if val, ok := x.(*Value); ok {
		v.val = v.val.Fill(val.val)
	} else {
		v.val = v.val.Fill(x)
	}
	return v.Validate()
}

// LookupPath is a concurrency safe wrapper around cue.Value.LookupPath
func (v *Value) LookupPath(p cue.Path) *Value {
	v.cc.RLock()
	defer v.cc.RUnlock()

	return v.Wrap(v.val.LookupPath(p))
}

// Lookup is a helper function to lookup by path parts.
func (v *Value) Lookup(path ...string) *Value {
	return v.LookupPath(cueStringsToCuePath(path...))
}

// Get is a helper function to lookup by path string
func (v *Value) Get(target string) *Value {
	return v.LookupPath(cue.ParsePath(target))
}

// Proxy function to the underlying cue.Value
func (v *Value) Len() cue.Value {
	return v.val.Len()
}

// Proxy function to the underlying cue.Value
func (v *Value) List() (cue.Iterator, error) {
	return v.val.List()
}

// Proxy function to the underlying cue.Value
func (v *Value) Fields() (*cue.Iterator, error) {
	return v.val.Fields()
}

// Proxy function to the underlying cue.Value
func (v *Value) Struct() (*cue.Struct, error) {
	return v.val.Struct()
}

// Proxy function to the underlying cue.Value
func (v *Value) Exists() bool {
	return v.val.Exists()
}

// Proxy function to the underlying cue.Value
func (v *Value) String() (string, error) {
	return v.val.String()
}

// Proxy function to the underlying cue.Value
func (v *Value) Path() cue.Path {
	return v.val.Path()
}

// Proxy function to the underlying cue.Value
func (v *Value) Decode(x interface{}) error {
	return v.val.Decode(x)
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

// Finalize a value using the given spec. This means:
//   1. Check that the value matches the spec.
//   2. Merge the value and the spec, and return the result.
func (v *Value) Finalize(spec *Value) (*Value, error) {
	v.cc.Lock()
	unified := spec.val.Unify(v.val)
	v.cc.Unlock()
	// FIXME: temporary debug message, remove before merging.
	//      fmt.Printf("Finalize:\n  spec=%v\n  v=%v\n  unified=%v", spec.val, v.val, unified)

	// OPTION 1: unfinished fields should pass, but don't
	// if err := unified.Validate(cue.Concrete(true)); err != nil {
	// OPTION 2: missing fields should fail, but don't
	// We choose option 2 for now, because it's easier to layer a
	// fix on top (we access individual fields so have an opportunity
	//  to return an error if they are not there).
	if err := unified.Validate(cue.Final()); err != nil {
		return nil, cueErr(err)
	}
	return v.Merge(spec)
}

// FIXME: receive string path?
func (v *Value) Merge(x interface{}, path ...string) (*Value, error) {
	if xval, ok := x.(*Value); ok {
		if xval.cc != v.cc {
			return nil, fmt.Errorf("can't merge values from different compilers")
		}
		x = xval.val
	}

	v.cc.Lock()
	result := v.Wrap(v.val.Fill(x, path...))
	v.cc.Unlock()

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

// Recursive concreteness check.
func (v *Value) IsConcreteR() error {
	return v.val.Validate(cue.Concrete(true))
}

// Export concrete values to JSON. ignoring non-concrete values.
// Contrast with cue.Value.MarshalJSON which requires all values
// to be concrete.
func (v *Value) JSON() JSON {
	var out JSON
	v.val.Walk(
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
	out, _ = out.Get(cuePathToStrings(v.Path())...)
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

// Check that a value is valid. Optionally check that it matches
// all the specified spec definitions..
func (v *Value) Validate(defs ...string) error {
	if err := v.val.Validate(); err != nil {
		return err
	}
	if len(defs) == 0 {
		return nil
	}
	spec := v.cc.Spec()
	for _, def := range defs {
		if err := spec.Validate(v, def); err != nil {
			return err
		}
	}
	return nil
}

// Return cue source for this value
func (v *Value) Source() ([]byte, error) {
	return cueformat.Node(v.val.Eval().Syntax())
}

// Return cue source for this value, as a Go string
func (v *Value) SourceString() (string, error) {
	b, err := v.Source()
	return string(b), err
}

func (v *Value) IsEmptyStruct() bool {
	if st, err := v.Struct(); err == nil {
		if st.Len() == 0 {
			return true
		}
	}
	return false
}
