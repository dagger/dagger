package core

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

func (*JSONValue) Type() *ast.Type {
	return &ast.Type{
		NamedType: "JSONObject",
		NonNull:   true,
	}
}

type JSONValue struct {
	data []byte
}

// NewJSONValue constructs a JSONValue from any Go value.
func NewJSONValue(v any) (JSONValue, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return JSONValue{}, err
	}
	return JSONValue{data: b}, nil
}

// Set sets value at path (dot-separated).
// "" or "." replaces the whole value.
func (o JSONValue) Set(path string, value any) (JSONValue, error) {
	if path == "" || path == "." {
		b, err := json.Marshal(value)
		if err != nil {
			return JSONValue{}, err
		}
		return JSONValue{data: b}, nil
	}

	var root any
	if len(o.data) != 0 {
		if err := json.Unmarshal(o.data, &root); err != nil {
			return JSONValue{}, err
		}
	}
	obj, ok := root.(map[string]interface{})
	if !ok {
		obj = map[string]interface{}{}
	}

	node := obj
	keys := strings.Split(path, ".")
	for i, k := range keys {
		if i == len(keys)-1 {
			node[k] = value
			break
		}
		if m, ok := node[k].(map[string]interface{}); ok {
			node = m
		} else {
			nm := map[string]interface{}{}
			node[k] = nm
			node = nm
		}
	}

	b, err := json.Marshal(obj)
	if err != nil {
		return JSONValue{}, err
	}
	return JSONValue{data: b}, nil
}

// Unset removes the value at path. "" or "." resets to null.
func (o JSONValue) Unset(path string) (JSONValue, error) {
	if path == "" || path == "." {
		return JSONValue{data: []byte("null")}, nil
	}

	var root any
	if err := json.Unmarshal(o.data, &root); err != nil {
		return JSONValue{}, err
	}
	obj, ok := root.(map[string]interface{})
	if !ok {
		return o, nil
	}

	node := obj
	keys := strings.Split(path, ".")
	for i, k := range keys {
		if i == len(keys)-1 {
			delete(node, k)
			break
		}
		if m, ok := node[k].(map[string]interface{}); ok {
			node = m
		} else {
			return o, nil
		}
	}

	b, err := json.Marshal(obj)
	if err != nil {
		return JSONValue{}, err
	}
	return JSONValue{data: b}, nil
}

// Get returns the value at path. "" or "." returns the whole value.
func (o JSONValue) Get(path string) (any, error) {
	var root any
	if err := json.Unmarshal(o.data, &root); err != nil {
		return nil, err
	}
	if path == "" || path == "." {
		return root, nil
	}

	node := root
	for _, k := range strings.Split(path, ".") {
		m, ok := node.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("path not found")
		}
		node, ok = m[k]
		if !ok {
			return nil, fmt.Errorf("path not found")
		}
	}
	return node, nil
}

// String returns the raw JSON.
func (o JSONValue) String() string { return string(o.data) }
