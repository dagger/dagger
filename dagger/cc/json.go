package cc

import (
	"fmt"

	"cuelang.org/go/cue"
	cuejson "cuelang.org/go/encoding/json"
	"github.com/KromDaniel/jonson"
	"github.com/pkg/errors"
)

type JSON []byte

func (s JSON) Get(path ...string) ([]byte, error) {
	if s == nil {
		s = []byte("{}")
	}
	var (
		root *jonson.JSON
	)
	root, err := jonson.Parse(s)
	if err != nil {
		return nil, errors.Wrap(err, "parse root json")
	}
	pointer := root
	for _, key := range path {
		// FIXME: we can traverse maps but not arrays (need to handle int keys)
		pointer = pointer.At(key)
	}
	// FIXME: use indent function from stdlib
	return pointer.ToJSON()
}

func (s JSON) Unset(path ...string) (JSON, error) {
	if s == nil {
		s = []byte("{}")
	}
	var (
		root *jonson.JSON
	)
	root, err := jonson.Parse(s)
	if err != nil {
		return nil, errors.Wrap(err, "unset: parse root json")
	}
	var (
		pointer = root
		pathDir []string
	)
	if len(path) > 0 {
		pathDir = path[:len(path)-1]
	}
	for _, key := range pathDir {
		pointer = pointer.At(key)
	}
	if len(path) == 0 {
		pointer.Set(nil)
	} else {
		key := path[len(path)-1]
		pointer.DeleteMapKey(key)
	}
	return root.ToJSON()
}

func (s JSON) Set(valueJSON []byte, path ...string) (JSON, error) {
	if s == nil {
		s = []byte("{}")
	}
	var (
		root  *jonson.JSON
		value *jonson.JSON
	)
	root, err := jonson.Parse(s)
	if err != nil {
		return nil, errors.Wrap(err, "parse root json")
	}
	value, err = jonson.Parse(valueJSON)
	if err != nil {
		return nil, errors.Wrapf(err, "SetJSON: parse value json: |%s|", valueJSON)
	}
	var (
		pointer = root
		pathDir []string
	)
	if len(path) > 0 {
		pathDir = path[:len(path)-1]
	}
	for _, key := range pathDir {
		if !pointer.ObjectKeyExists(key) {
			pointer.MapSet(key, jonson.NewEmptyJSONMap())
		}
		pointer = pointer.At(key)
	}
	if len(path) == 0 {
		pointer.Set(value)
	} else {
		key := path[len(path)-1]
		pointer.MapSet(key, value)
	}
	return root.ToJSON()
}

func (s JSON) Merge(layers ...JSON) (JSON, error) {
	r := new(cue.Runtime)
	var resultInst *cue.Instance
	for i, l := range append([]JSON{s}, layers...) {
		if l == nil {
			continue
		}
		filename := fmt.Sprintf("%d", i)
		inst, err := cuejson.Decode(r, filename, []byte(l))
		if err != nil {
			return nil, err
		}
		if resultInst == nil {
			resultInst = inst
		} else {
			resultInst, err = resultInst.Fill(inst.Value())
			if err != nil {
				return nil, err
			}
			if resultInst.Err != nil {
				return nil, resultInst.Err
			}
		}
	}
	b, err := resultInst.Value().MarshalJSON()
	if err != nil {
		return nil, err
	}
	return JSON(b), nil
}

func (s JSON) String() string {
	if s == nil {
		return "{}"
	}
	return string(s)
}
