package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/KromDaniel/jonson"
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
		return nil, fmt.Errorf("parse root json: %w", err)
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
		return nil, fmt.Errorf("unset: parse root json: %w", err)
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
		return nil, fmt.Errorf("parse root json: %w", err)
	}
	value, err = jonson.Parse(valueJSON)
	if err != nil {
		return nil, fmt.Errorf("SetJSON: parse value json: |%s|: %w", valueJSON, err)
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

func (s JSON) String() string {
	if s == nil {
		return "{}"
	}
	return string(s)
}

func (s JSON) PrettyString() string {
	raw := s.String()
	b := &bytes.Buffer{}
	// If indenting fails, return raw string
	if err := json.Indent(b, []byte(raw), "", "  "); err != nil {
		return raw
	}
	return b.String()
}
