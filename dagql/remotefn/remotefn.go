package remotefn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/big"
	"reflect"
	"runtime"
	"strings"
	"time"
)

// FnSchema inspects `fn` to see if it's either:
//
//	func(T) (R, error)                 // single-arg
//	func(context.Context, T) (R, error) // context + single-arg
//
// It extracts T (the user argument type), then returns a JSON Schema
// for T as a map[string]interface{} (instead of a JSON string).
func FnSchema(fn interface{}) (map[string]interface{}, error) {
	userType, err := extractUserArgType(fn)
	if err != nil {
		return nil, err
	}
	schemaMap := buildTypeSchema(userType, make(map[reflect.Type]bool))
	return schemaMap, nil
}

// FnCall decodes `jsonArgs` into T, then calls `fn`, returning (interface{}, error).
// fn can be:
//
//	func(T) (R, error)
//	func(context.Context, T) (R, error)
func FnCall(ctx context.Context, fn interface{}, jsonArgs []byte) (interface{}, error) {
	userType, err := extractUserArgType(fn)
	if err != nil {
		return nil, err
	}
	schemaMap := buildTypeSchema(userType, make(map[reflect.Type]bool))

	// Decode JSON into T
	argVal, err := decodeToType(jsonArgs, userType, schemaMap)
	if err != nil {
		return nil, err
	}

	// Reflect call
	fv := reflect.ValueOf(fn)
	ft := fv.Type()
	if ft.NumOut() != 2 {
		return nil, fmt.Errorf("function must return exactly (R, error)")
	}

	var in []reflect.Value
	switch ft.NumIn() {
	case 1:
		// (T)
		if !ft.In(0).AssignableTo(userType) {
			return nil, fmt.Errorf("function param mismatch: wanted %v, got %v", userType, ft.In(0))
		}
		in = []reflect.Value{argVal}

	case 2:
		// (context.Context, T)
		if ft.In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() {
			return nil, fmt.Errorf("function's first param must be context.Context")
		}
		if !ft.In(1).AssignableTo(userType) {
			return nil, fmt.Errorf("function's second param mismatch: wanted %v, got %v", userType, ft.In(1))
		}
		in = []reflect.Value{
			reflect.ValueOf(ctx),
			argVal,
		}

	default:
		return nil, fmt.Errorf("function must have 1 or 2 params")
	}

	results := fv.Call(in)
	resVal := results[0]
	errVal := results[1]
	if !errVal.IsNil() {
		return resVal.Interface(), errVal.Interface().(error)
	}
	return resVal.Interface(), nil
}

// FnDecodeArgs decodes the JSON into the function's expected argument type T
// but does not call the function. It returns (decodedValue, error).
func FnDecodeArgs(ctx context.Context, fn interface{}, jsonArgs string) (interface{}, error) {
	userType, err := extractUserArgType(fn)
	if err != nil {
		return nil, err
	}
	schemaMap := buildTypeSchema(userType, make(map[reflect.Type]bool))
	argVal, err := decodeToType([]byte(jsonArgs), userType, schemaMap)
	if err != nil {
		return nil, err
	}
	return argVal.Interface(), nil
}

// FnName returns the (best-effort) name of the function or method ("FooMethod", etc.).
func FnName(fn interface{}) (string, error) {
	if fn == nil {
		return "", errors.New("fn is nil")
	}
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func {
		return "", fmt.Errorf("not a function: %v", v.Kind())
	}

	pc := v.Pointer()
	rf := runtime.FuncForPC(pc)
	if rf == nil {
		return "", errors.New("cannot resolve runtime func")
	}
	fullName := rf.Name() // e.g. "github.com/user/repo.(*MyObj).FooMethod-fm"
	shortName := parseFuncName(fullName)
	if shortName == "" {
		return "", fmt.Errorf("unable to parse function name from %q", fullName)
	}
	return shortName, nil
}

// FnDescription returns the doc comment from above the function in its .go file.
func FnDescription(fn interface{}) (string, error) {
	if fn == nil {
		return "", errors.New("fn is nil")
	}
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func {
		return "", fmt.Errorf("not a function: %v", v.Kind())
	}
	pc := v.Pointer()
	rf := runtime.FuncForPC(pc)
	if rf == nil {
		return "", errors.New("cannot resolve runtime func")
	}
	file, line := rf.FileLine(pc)
	if file == "" {
		return "", fmt.Errorf("no file info for function %q", rf.Name())
	}

	shortName := parseFuncName(rf.Name())
	if shortName == "" {
		return "", fmt.Errorf("cannot parse function name from %q", rf.Name())
	}

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("parse file: %v", err)
	}

	// Attempt to find a matching FuncDecl
	var doc string
	ast.Inspect(node, func(n ast.Node) bool {
		fnDecl, ok := n.(*ast.FuncDecl)
		if !ok || fnDecl.Name == nil {
			return true
		}
		if fnDecl.Name.Name == shortName {
			start := fset.Position(fnDecl.Pos()).Line
			end := fset.Position(fnDecl.End()).Line
			if line >= start && line <= end {
				if fnDecl.Doc != nil {
					doc = strings.TrimSpace(fnDecl.Doc.Text())
				}
				return false
			}
		}
		return true
	})

	// If empty, try method with receiver
	if doc == "" {
		ast.Inspect(node, func(n ast.Node) bool {
			fnDecl, ok := n.(*ast.FuncDecl)
			if !ok || fnDecl.Recv == nil || fnDecl.Name == nil {
				return true
			}
			if fnDecl.Name.Name == shortName {
				start := fset.Position(fnDecl.Pos()).Line
				end := fset.Position(fnDecl.End()).Line
				if line >= start && line <= end {
					if fnDecl.Doc != nil {
						doc = strings.TrimSpace(fnDecl.Doc.Text())
					}
					return false
				}
			}
			return true
		})
	}

	return doc, nil
}

//------------------------------------------------------------------------------
// internal helper logic
//------------------------------------------------------------------------------

func extractUserArgType(fn interface{}) (reflect.Type, error) {
	if fn == nil {
		return nil, errors.New("fn is nil")
	}
	fv := reflect.ValueOf(fn)
	ft := fv.Type()
	if ft.Kind() != reflect.Func {
		return nil, fmt.Errorf("fn must be a function, got %v", ft.Kind())
	}

	switch ft.NumIn() {
	case 1:
		return ft.In(0), nil
	case 2:
		ctxType := ft.In(0)
		if ctxType != reflect.TypeOf((*context.Context)(nil)).Elem() {
			return nil, fmt.Errorf("function with 2 params must have (context.Context, T)")
		}
		return ft.In(1), nil
	default:
		return nil, fmt.Errorf("function must have 1 or 2 params, got %d", ft.NumIn())
	}
}

func buildTypeSchema(t reflect.Type, visited map[reflect.Type]bool) map[string]interface{} {
	if t == nil {
		return map[string]interface{}{"type": "null"}
	}
	// unwrap pointer(s)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
		if t == nil {
			return map[string]interface{}{"type": "null"}
		}
	}
	if visited[t] {
		return map[string]interface{}{"type": "object"}
	}
	visited[t] = true

	if t == reflect.TypeOf(time.Time{}) {
		return map[string]interface{}{
			"type":   "string",
			"format": "date-time",
		}
	}
	if t == reflect.TypeOf(big.Int{}) {
		return map[string]interface{}{
			"type":   "string",
			"format": "big-integer",
		}
	}
	if ev, ok := tryGetEnumValues(t); ok {
		return map[string]interface{}{
			"type": "string",
			"enum": ev,
		}
	}

	switch t.Kind() {
	case reflect.Bool:
		return map[string]interface{}{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]interface{}{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]interface{}{"type": "number"}
	case reflect.String:
		return map[string]interface{}{"type": "string"}
	case reflect.Slice, reflect.Array:
		return map[string]interface{}{
			"type":  "array",
			"items": buildTypeSchema(t.Elem(), visited),
		}
	case reflect.Map:
		return map[string]interface{}{
			"type":                 "object",
			"additionalProperties": buildTypeSchema(t.Elem(), visited),
		}
	case reflect.Struct:
		return buildStructSchema(t, visited)
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Complex64, reflect.Complex128, reflect.UnsafePointer:
		return map[string]interface{}{"type": "string"}
	default:
		return map[string]interface{}{"type": "string"}
	}
}

func buildStructSchema(t reflect.Type, visited map[reflect.Type]bool) map[string]interface{} {
	obj := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
	props := obj["properties"].(map[string]interface{})
	req := obj["required"].([]string)

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // unexported
		}
		fName := f.Name
		props[fName] = buildTypeSchema(f.Type, visited)
		req = append(req, fName)
	}
	obj["required"] = req
	return obj
}

func tryGetEnumValues(t reflect.Type) ([]string, bool) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
		if t == nil {
			return nil, false
		}
	}
	m, found := t.MethodByName("EnumValues")
	if !found {
		return nil, false
	}
	if m.Type.NumIn() != 1 || m.Type.NumOut() != 1 {
		return nil, false
	}
	zero := reflect.New(t).Elem()
	out := m.Func.Call([]reflect.Value{zero})
	arr := out[0].Interface()
	strSlice, ok := arr.([]string)
	return strSlice, ok
}

func decodeToType(rawJSON []byte, t reflect.Type, sch map[string]interface{}) (reflect.Value, error) {
	var intermediate interface{}
	if err := json.Unmarshal(rawJSON, &intermediate); err != nil {
		return reflect.Value{}, fmt.Errorf("invalid JSON: %w", err)
	}
	return convertValue(intermediate, t, sch)
}

func convertValue(data interface{}, t reflect.Type, sch map[string]interface{}) (reflect.Value, error) {
	// pointers
	if t.Kind() == reflect.Ptr {
		if data == nil {
			return reflect.Zero(t), nil
		}
		ev, err := convertValue(data, t.Elem(), sch)
		if err != nil {
			return reflect.Value{}, err
		}
		ptr := reflect.New(t.Elem())
		ptr.Elem().Set(ev)
		return ptr, nil
	}
	if t == reflect.TypeOf(time.Time{}) {
		s, ok := data.(string)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected string for time.Time, got %T", data)
		}
		tt, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("invalid time format: %v", err)
		}
		return reflect.ValueOf(tt), nil
	}
	if t == reflect.TypeOf(big.Int{}) {
		switch v := data.(type) {
		case string:
			var z big.Int
			if _, ok := z.SetString(v, 10); !ok {
				return reflect.Value{}, fmt.Errorf("cannot parse big.Int from %q", v)
			}
			return reflect.ValueOf(z), nil
		case float64:
			tmp := big.NewInt(int64(v))
			if float64(tmp.Int64()) != v {
				return reflect.Value{}, fmt.Errorf("lossy big.Int from float64: %v", v)
			}
			return reflect.ValueOf(*tmp), nil
		case map[string]interface{}:
			return reflect.Value{}, fmt.Errorf("default big.Int encoding not supported; pass as decimal string")
		default:
			return reflect.Value{}, fmt.Errorf("expected string/float for big.Int, got %T", data)
		}
	}

	// enum?
	if _, isEnum := sch["enum"]; isEnum && t.Kind() == reflect.String {
		strVal, ok := data.(string)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected enum string, got %T", data)
		}
		return reflect.ValueOf(strVal).Convert(t), nil
	}

	switch t.Kind() {
	case reflect.Bool:
		b, ok := data.(bool)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected bool, got %T", data)
		}
		return reflect.ValueOf(b).Convert(t), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		f, ok := data.(float64)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected int, got %T", data)
		}
		return reflect.ValueOf(int64(f)).Convert(t), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		f, ok := data.(float64)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected uint, got %T", data)
		}
		return reflect.ValueOf(uint64(f)).Convert(t), nil
	case reflect.Float32, reflect.Float64:
		f, ok := data.(float64)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected float, got %T", data)
		}
		return reflect.ValueOf(f).Convert(t), nil
	case reflect.String:
		s, ok := data.(string)
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected string, got %T", data)
		}
		return reflect.ValueOf(s).Convert(t), nil

	case reflect.Slice:
		arr, ok := data.([]interface{})
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected array, got %T", data)
		}
		sliceVal := reflect.MakeSlice(t, len(arr), len(arr))
		itemSchema, _ := sch["items"].(map[string]interface{})
		for i, elem := range arr {
			ev, err := convertValue(elem, t.Elem(), itemSchema)
			if err != nil {
				return reflect.Value{}, fmt.Errorf("slice[%d]: %v", i, err)
			}
			sliceVal.Index(i).Set(ev)
		}
		return sliceVal, nil

	case reflect.Array:
		arr, ok := data.([]interface{})
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected array, got %T", data)
		}
		if len(arr) != t.Len() {
			return reflect.Value{}, fmt.Errorf("array length mismatch: needed %d, got %d", t.Len(), len(arr))
		}
		arrayVal := reflect.New(t).Elem()
		itemSchema, _ := sch["items"].(map[string]interface{})
		for i, elem := range arr {
			ev, err := convertValue(elem, t.Elem(), itemSchema)
			if err != nil {
				return reflect.Value{}, fmt.Errorf("array[%d]: %v", i, err)
			}
			arrayVal.Index(i).Set(ev)
		}
		return arrayVal, nil

	case reflect.Map:
		obj, ok := data.(map[string]interface{})
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected object, got %T", data)
		}
		mapVal := reflect.MakeMap(t)
		childSchema, _ := sch["additionalProperties"].(map[string]interface{})
		for k, v := range obj {
			keyVal := reflect.ValueOf(k).Convert(t.Key())
			val, err := convertValue(v, t.Elem(), childSchema)
			if err != nil {
				return reflect.Value{}, fmt.Errorf("map key %q: %v", k, err)
			}
			mapVal.SetMapIndex(keyVal, val)
		}
		return mapVal, nil

	case reflect.Struct:
		obj, ok := data.(map[string]interface{})
		if !ok {
			return reflect.Value{}, fmt.Errorf("expected object, got %T", data)
		}
		res := reflect.New(t).Elem()
		props, _ := sch["properties"].(map[string]interface{})
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			fieldSchema, _ := props[f.Name].(map[string]interface{})
			rawVal, has := obj[f.Name]
			if !has {
				continue
			}
			fv, err := convertValue(rawVal, f.Type, fieldSchema)
			if err != nil {
				return reflect.Value{}, fmt.Errorf("struct field %s: %v", f.Name, err)
			}
			res.Field(i).Set(fv)
		}
		return res, nil

	default:
		// If implements json.Unmarshaler, attempt that
		if reflect.PointerTo(t).Implements(reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()) {
			rawB, err := json.Marshal(data)
			if err != nil {
				return reflect.Value{}, err
			}
			newPtr := reflect.New(t)
			if err := newPtr.Interface().(json.Unmarshaler).UnmarshalJSON(rawB); err != nil {
				return reflect.Value{}, fmt.Errorf("custom UnmarshalJSON for %v: %v", t, err)
			}
			return newPtr.Elem(), nil
		}
		// fallback
		rawB, err := json.Marshal(data)
		if err != nil {
			return reflect.Value{}, err
		}
		newPtr := reflect.New(t)
		if err := json.Unmarshal(rawB, newPtr.Interface()); err != nil {
			return reflect.Value{}, fmt.Errorf("cannot unmarshal into %v: %v", t, err)
		}
		return newPtr.Elem(), nil
	}
}

// parseFuncName extracts the short method name from a fully qualified runtime name,
// e.g. "github.com/user/repo.(*MyObj).FooMethod-fm" => "FooMethod".
func parseFuncName(fullName string) string {
	// Remove trailing "-fm"
	if strings.HasSuffix(fullName, "-fm") {
		fullName = strings.TrimSuffix(fullName, "-fm")
	}
	idx := strings.LastIndexAny(fullName, ".)")
	if idx == -1 {
		return fullName
	}
	return fullName[idx+1:]
}
