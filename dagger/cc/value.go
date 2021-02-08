package cc

import (
	"cuelang.org/go/cue"
	cueformat "cuelang.org/go/cue/format"
)

// Value is a wrapper around cue.Value.
// Use instead of cue.Value and cue.Instance
type Value struct {
	val  cue.Value
	inst *cue.Instance
}

func (v *Value) CueInst() *cue.Instance {
	return v.inst
}

func (v *Value) Wrap(v2 cue.Value) *Value {
	return wrapValue(v2, v.inst)
}

func wrapValue(v cue.Value, inst *cue.Instance) *Value {
	return &Value{
		val:  v,
		inst: inst,
	}
}

// Fill the value in-place, unlike Merge which returns a copy.
func (v *Value) Fill(x interface{}) error {
	cc.Lock()
	defer cc.Unlock()

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
	cc.RLock()
	defer cc.RUnlock()

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

func (v *Value) SourceUnsafe() string {
	s, _ := v.SourceString()
	return s
}

// Proxy function to the underlying cue.Value
func (v *Value) Path() cue.Path {
	return v.val.Path()
}

// Proxy function to the underlying cue.Value
func (v *Value) Decode(x interface{}) error {
	return v.val.Decode(x)
}

func (v *Value) List() ([]*Value, error) {
	l := []*Value{}
	it, err := v.val.List()
	if err != nil {
		return nil, err
	}
	for it.Next() {
		l = append(l, v.Wrap(it.Value()))
	}
	return l, nil
}

// FIXME: deprecate to simplify
func (v *Value) RangeList(fn func(int, *Value) error) error {
	it, err := v.val.List()
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

// FIXME: receive string path?
func (v *Value) Merge(x interface{}, path ...string) (*Value, error) {
	if xval, ok := x.(*Value); ok {
		x = xval.val
	}

	cc.Lock()
	result := v.Wrap(v.val.Fill(x, path...))
	cc.Unlock()

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

func (v *Value) Walk(before func(*Value) bool, after func(*Value)) {
	// FIXME: lock?
	var (
		llBefore func(cue.Value) bool
		llAfter  func(cue.Value)
	)
	if before != nil {
		llBefore = func(child cue.Value) bool {
			return before(v.Wrap(child))
		}
	}
	if after != nil {
		llAfter = func(child cue.Value) {
			after(v.Wrap(child))
		}
	}
	v.val.Walk(llBefore, llAfter)
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

// Check that a value is valid. Optionally check that it matches
// all the specified spec definitions..
func (v *Value) Validate() error {
	return v.val.Validate()
}

// Return cue source for this value
func (v *Value) Source() ([]byte, error) {
	cc.RLock()
	defer cc.RUnlock()

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

func (v *Value) Cue() cue.Value {
	return v.val
}
