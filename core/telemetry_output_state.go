package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"google.golang.org/protobuf/proto"
)

const (
	maxOutputStateDepth  = 8
	maxOutputStateTraces = 1024
)

var outputStateEmitter = newOutputStateEmitter(maxOutputStateTraces)

type outputStateEmitterCache struct {
	mu        sync.Mutex
	maxTraces int
	traces    map[string]*traceOutputState
}

type traceOutputState struct {
	lastSeen time.Time
	seen     map[string]struct{}
}

func newOutputStateEmitter(maxTraces int) *outputStateEmitterCache {
	if maxTraces <= 0 {
		maxTraces = maxOutputStateTraces
	}
	return &outputStateEmitterCache{
		maxTraces: maxTraces,
		traces:    map[string]*traceOutputState{},
	}
}

func (c *outputStateEmitterCache) TryReserve(traceID, outputDigest string) bool {
	if traceID == "" || outputDigest == "" {
		return true
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry := c.traces[traceID]
	if entry == nil {
		entry = &traceOutputState{
			seen: map[string]struct{}{},
		}
		c.traces[traceID] = entry
	}
	entry.lastSeen = time.Now()

	if _, ok := entry.seen[outputDigest]; ok {
		return false
	}
	entry.seen[outputDigest] = struct{}{}
	c.evictOldestLocked()
	return true
}

func (c *outputStateEmitterCache) Release(traceID, outputDigest string) {
	if traceID == "" || outputDigest == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry := c.traces[traceID]
	if entry == nil {
		return
	}
	delete(entry.seen, outputDigest)
	entry.lastSeen = time.Now()
	if len(entry.seen) == 0 {
		delete(c.traces, traceID)
	}
}

func (c *outputStateEmitterCache) evictOldestLocked() {
	if len(c.traces) <= c.maxTraces {
		return
	}

	var oldestTraceID string
	var oldest time.Time
	for traceID, entry := range c.traces {
		if oldestTraceID == "" || entry.lastSeen.Before(oldest) {
			oldestTraceID = traceID
			oldest = entry.lastSeen
		}
	}
	if oldestTraceID != "" {
		delete(c.traces, oldestTraceID)
	}
}

func encodeOutputStatePayload(obj dagql.AnyResult) (string, error) {
	state, err := buildOutputStatePayload(obj)
	if err != nil {
		return "", err
	}

	payload, err := proto.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("marshal output state payload: %w", err)
	}
	return base64.StdEncoding.EncodeToString(payload), nil
}

func buildOutputStatePayload(obj dagql.AnyResult) (*callpbv1.OutputState, error) {
	if obj == nil {
		return nil, fmt.Errorf("nil object result")
	}

	typed := obj.Unwrap()
	if typed == nil {
		return nil, fmt.Errorf("nil object payload")
	}

	return buildOutputStatePayloadFromTyped(typed, obj.Type())
}

func buildOutputStatePayloadFromTyped(typed dagql.Typed, resultType *ast.Type) (*callpbv1.OutputState, error) {
	if typed == nil {
		return nil, fmt.Errorf("nil object payload")
	}

	v := reflect.ValueOf(typed)
	fields, err := collectOutputStateFields(v)
	if err != nil {
		return nil, err
	}

	return &callpbv1.OutputState{
		Type:   gqlTypeName(resultType),
		Fields: fields,
	}, nil
}

func collectOutputStateFields(v reflect.Value) ([]*callpbv1.OutputStateField, error) {
	v = derefValue(v)
	if !v.IsValid() {
		return nil, nil
	}
	if v.Kind() != reflect.Struct {
		value, refs, err := toOutputStateLiteral(v, 0, map[visitKey]struct{}{})
		if err != nil {
			return nil, err
		}
		return []*callpbv1.OutputStateField{
			{
				Name:  "value",
				Type:  v.Type().String(),
				Value: value,
				Refs:  refs,
			},
		}, nil
	}

	fields := make([]*callpbv1.OutputStateField, 0, v.Type().NumField())
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		structField := typ.Field(i)
		name, ok := outputStateFieldName(structField)
		if !ok {
			continue
		}

		fieldValue := v.Field(i)
		value, refs, err := toOutputStateLiteral(fieldValue, 0, map[visitKey]struct{}{})
		if err != nil {
			value = literalObject(map[string]any{"error": err.Error()})
			refs = nil
		}

		fields = append(fields, &callpbv1.OutputStateField{
			Name:  name,
			Type:  outputStateTypeName(structField, fieldValue),
			Value: value,
			Refs:  refs,
		})
	}
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].GetName() < fields[j].GetName()
	})

	return fields, nil
}

func outputStateFieldName(field reflect.StructField) (string, bool) {
	if field.PkgPath != "" {
		return "", false
	}

	jsonTag := field.Tag.Get("json")
	if jsonTag == "-" {
		return "", false
	}
	if jsonTag != "" {
		name := strings.Split(jsonTag, ",")[0]
		if name == "-" {
			return "", false
		}
		if name != "" {
			return name, true
		}
	}
	return field.Name, true
}

func outputStateTypeName(field reflect.StructField, value reflect.Value) string {
	if typeName, ok := safeTypedTypeString(value); ok {
		return typeName
	}
	return field.Type.String()
}

type visitKey struct {
	typ reflect.Type
	ptr uintptr
}

func toOutputStateLiteral(v reflect.Value, depth int, seen map[visitKey]struct{}) (*callpbv1.Literal, []string, error) {
	if depth > maxOutputStateDepth {
		return literalString("<max-depth>"), nil, nil
	}

	for v.IsValid() && v.Kind() == reflect.Interface {
		if v.IsNil() {
			return literalNull(), nil, nil
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return literalNull(), nil, nil
	}

	if v.CanInterface() {
		if idDigest, ok := digestFromIDable(v.Interface()); ok {
			return literalCallDigest(idDigest), []string{idDigest}, nil
		}
		if id, ok := v.Interface().(*call.ID); ok {
			if id == nil {
				return literalNull(), nil, nil
			}
			digest := id.Digest().String()
			return literalCallDigest(digest), []string{digest}, nil
		}
	}

	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return literalNull(), nil, nil
		}

		key := visitKey{typ: v.Type(), ptr: v.Pointer()}
		if _, ok := seen[key]; ok {
			return literalString("<cycle>"), nil, nil
		}
		seen[key] = struct{}{}
		defer delete(seen, key)

		return toOutputStateLiteral(v.Elem(), depth+1, seen)

	case reflect.Bool:
		return literalBool(v.Bool()), nil, nil
	case reflect.String:
		return literalString(v.String()), nil, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return literalInt(v.Int()), nil, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u := v.Uint()
		if u <= math.MaxInt64 {
			return literalInt(int64(u)), nil, nil
		}
		return literalString(strconv.FormatUint(u, 10)), nil, nil
	case reflect.Float32, reflect.Float64:
		return literalFloat(v.Float()), nil, nil

	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			// Avoid leaking raw bytes while preserving JSON encodability.
			return literalString(base64.StdEncoding.EncodeToString(v.Bytes())), nil, nil
		}
		fallthrough
	case reflect.Array:
		out := make([]*callpbv1.Literal, 0, v.Len())
		refs := make([]string, 0)
		for i := 0; i < v.Len(); i++ {
			val, valRefs, err := toOutputStateLiteral(v.Index(i), depth+1, seen)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, val)
			refs = append(refs, valRefs...)
		}
		return literalList(out...), dedupeSortedStrings(refs), nil

	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			if v.CanInterface() {
				return literalString(fmt.Sprintf("%v", v.Interface())), nil, nil
			}
			return literalString("<map>"), nil, nil
		}

		out := make([]*callpbv1.Argument, 0, v.Len())
		refs := make([]string, 0)
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool {
			return keys[i].String() < keys[j].String()
		})
		for _, key := range keys {
			val, valRefs, err := toOutputStateLiteral(v.MapIndex(key), depth+1, seen)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, &callpbv1.Argument{Name: key.String(), Value: val})
			refs = append(refs, valRefs...)
		}
		return literalObjectArgs(out...), dedupeSortedStrings(refs), nil

	case reflect.Struct:
		if v.CanInterface() {
			if ts, ok := v.Interface().(time.Time); ok {
				return literalString(ts.UTC().Format(time.RFC3339Nano)), nil, nil
			}
		}

		out := make([]*callpbv1.Argument, 0, v.NumField())
		refs := make([]string, 0)
		typ := v.Type()
		names := make([]string, 0, typ.NumField())
		fieldsByName := map[string]reflect.Value{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name, ok := outputStateFieldName(field)
			if !ok {
				continue
			}
			names = append(names, name)
			fieldsByName[name] = v.Field(i)
		}
		sort.Strings(names)
		for _, name := range names {
			val, valRefs, err := toOutputStateLiteral(fieldsByName[name], depth+1, seen)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, &callpbv1.Argument{Name: name, Value: val})
			refs = append(refs, valRefs...)
		}
		return literalObjectArgs(out...), dedupeSortedStrings(refs), nil

	default:
		if v.CanInterface() {
			if marshaler, ok := v.Interface().(json.Marshaler); ok {
				b, err := marshaler.MarshalJSON()
				if err == nil {
					var decoded any
					if err := json.Unmarshal(b, &decoded); err == nil {
						return literalObject(decoded), nil, nil
					}
				}
			}
			return literalString(fmt.Sprintf("%v", v.Interface())), nil, nil
		}
		return literalNull(), nil, nil
	}
}

func dedupeSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func literalNull() *callpbv1.Literal {
	return &callpbv1.Literal{Value: &callpbv1.Literal_Null{Null: true}}
}

func literalBool(v bool) *callpbv1.Literal {
	return &callpbv1.Literal{Value: &callpbv1.Literal_Bool{Bool: v}}
}

func literalString(v string) *callpbv1.Literal {
	return &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: v}}
}

func literalInt(v int64) *callpbv1.Literal {
	return &callpbv1.Literal{Value: &callpbv1.Literal_Int{Int: v}}
}

func literalFloat(v float64) *callpbv1.Literal {
	return &callpbv1.Literal{Value: &callpbv1.Literal_Float{Float: v}}
}

func literalCallDigest(v string) *callpbv1.Literal {
	return &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: v}}
}

func literalList(values ...*callpbv1.Literal) *callpbv1.Literal {
	return &callpbv1.Literal{Value: &callpbv1.Literal_List{List: &callpbv1.List{Values: values}}}
}

func literalObjectArgs(values ...*callpbv1.Argument) *callpbv1.Literal {
	return &callpbv1.Literal{Value: &callpbv1.Literal_Object{Object: &callpbv1.Object{Values: values}}}
}

func literalObject(v any) *callpbv1.Literal {
	switch x := v.(type) {
	case nil:
		return literalNull()
	case bool:
		return literalBool(x)
	case string:
		return literalString(x)
	case int:
		return literalInt(int64(x))
	case int8:
		return literalInt(int64(x))
	case int16:
		return literalInt(int64(x))
	case int32:
		return literalInt(int64(x))
	case int64:
		return literalInt(x)
	case uint:
		if x <= math.MaxInt64 {
			return literalInt(int64(x))
		}
		return literalString(strconv.FormatUint(uint64(x), 10))
	case uint8:
		return literalInt(int64(x))
	case uint16:
		return literalInt(int64(x))
	case uint32:
		return literalInt(int64(x))
	case uint64:
		if x <= math.MaxInt64 {
			return literalInt(int64(x))
		}
		return literalString(strconv.FormatUint(x, 10))
	case float32:
		return literalFloat(float64(x))
	case float64:
		return literalFloat(x)
	case []any:
		items := make([]*callpbv1.Literal, 0, len(x))
		for _, item := range x {
			items = append(items, literalObject(item))
		}
		return literalList(items...)
	case map[string]any:
		keys := make([]string, 0, len(x))
		for key := range x {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		args := make([]*callpbv1.Argument, 0, len(keys))
		for _, key := range keys {
			args = append(args, &callpbv1.Argument{
				Name:  key,
				Value: literalObject(x[key]),
			})
		}
		return literalObjectArgs(args...)
	default:
		return literalString(fmt.Sprintf("%v", x))
	}
}

func digestFromIDable(v any) (_ string, ok bool) {
	if v == nil {
		return "", false
	}

	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return "", false
	}
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if rv.IsNil() {
			return "", false
		}
	}

	idable, ok := v.(dagql.IDable)
	if !ok {
		return "", false
	}

	// Telemetry serialization must never crash query execution. Some IDable
	// values can be typed-nil interfaces or otherwise panic when ID() is called.
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()

	id := idable.ID()
	if id == nil {
		return "", false
	}
	return id.Digest().String(), true
}

func safeTypedTypeString(v reflect.Value) (_ string, ok bool) {
	if !v.IsValid() || !v.CanInterface() {
		return "", false
	}

	val := v.Interface()
	if val == nil {
		return "", false
	}

	rv := reflect.ValueOf(val)
	if !rv.IsValid() {
		return "", false
	}
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if rv.IsNil() {
			return "", false
		}
	}

	typed, ok := val.(dagql.Typed)
	if !ok {
		return "", false
	}

	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	typ := typed.Type()
	if typ == nil {
		return "", false
	}
	return typ.String(), true
}

func gqlTypeName(typ *ast.Type) string {
	if typ == nil {
		return ""
	}
	if typ.NamedType != "" {
		return typ.NamedType
	}
	if typ.Elem != nil {
		return gqlTypeName(typ.Elem)
	}
	return typ.String()
}

func derefValue(v reflect.Value) reflect.Value {
	for v.IsValid() && v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}
