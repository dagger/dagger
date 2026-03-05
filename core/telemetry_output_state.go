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
)

const (
	maxOutputStateDepth  = 8
	maxOutputStateTraces = 1024
)

type outputStatePayloadV1 struct {
	Type   string                        `json:"type"`
	Fields map[string]outputStateFieldV1 `json:"fields"`
}

type outputStateFieldV1 struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value any    `json:"value"`
}

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
	state, err := buildOutputStatePayloadV1(obj)
	if err != nil {
		return "", err
	}

	payload, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("marshal output state payload: %w", err)
	}
	return base64.StdEncoding.EncodeToString(payload), nil
}

func buildOutputStatePayloadV1(obj dagql.AnyResult) (*outputStatePayloadV1, error) {
	if obj == nil {
		return nil, fmt.Errorf("nil object result")
	}

	typed := obj.Unwrap()
	if typed == nil {
		return nil, fmt.Errorf("nil object payload")
	}

	return buildOutputStatePayloadFromTyped(typed, obj.Type())
}

func buildOutputStatePayloadFromTyped(typed dagql.Typed, resultType *ast.Type) (*outputStatePayloadV1, error) {
	if typed == nil {
		return nil, fmt.Errorf("nil object payload")
	}

	v := reflect.ValueOf(typed)
	fields, err := collectOutputStateFields(v)
	if err != nil {
		return nil, err
	}

	return &outputStatePayloadV1{
		Type:   gqlTypeName(resultType),
		Fields: fields,
	}, nil
}

func collectOutputStateFields(v reflect.Value) (map[string]outputStateFieldV1, error) {
	v = derefValue(v)
	if !v.IsValid() {
		return map[string]outputStateFieldV1{}, nil
	}
	if v.Kind() != reflect.Struct {
		value, err := toOutputStateValue(v, 0, map[visitKey]struct{}{})
		if err != nil {
			return nil, err
		}
		return map[string]outputStateFieldV1{
			"value": {
				Name:  "value",
				Type:  v.Type().String(),
				Value: value,
			},
		}, nil
	}

	fields := map[string]outputStateFieldV1{}
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		structField := typ.Field(i)
		name, ok := outputStateFieldName(structField)
		if !ok {
			continue
		}

		fieldValue := v.Field(i)
		value, err := toOutputStateValue(fieldValue, 0, map[visitKey]struct{}{})
		if err != nil {
			value = map[string]any{
				"error": err.Error(),
			}
		}

		fields[name] = outputStateFieldV1{
			Name:  name,
			Type:  outputStateTypeName(structField, fieldValue),
			Value: value,
		}
	}

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

func toOutputStateValue(v reflect.Value, depth int, seen map[visitKey]struct{}) (any, error) {
	if depth > maxOutputStateDepth {
		return "<max-depth>", nil
	}

	for v.IsValid() && v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil, nil
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return nil, nil
	}

	if v.CanInterface() {
		if idDigest, ok := digestFromIDable(v.Interface()); ok {
			return idDigest, nil
		}
		if id, ok := v.Interface().(*call.ID); ok {
			if id == nil {
				return nil, nil
			}
			return id.Digest().String(), nil
		}
	}

	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return nil, nil
		}

		key := visitKey{typ: v.Type(), ptr: v.Pointer()}
		if _, ok := seen[key]; ok {
			return "<cycle>", nil
		}
		seen[key] = struct{}{}
		defer delete(seen, key)

		return toOutputStateValue(v.Elem(), depth+1, seen)

	case reflect.Bool:
		return v.Bool(), nil
	case reflect.String:
		return v.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u := v.Uint()
		if u <= math.MaxInt64 {
			return int64(u), nil
		}
		return strconv.FormatUint(u, 10), nil
	case reflect.Float32, reflect.Float64:
		return v.Float(), nil

	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			// Avoid leaking raw bytes while preserving JSON encodability.
			return base64.StdEncoding.EncodeToString(v.Bytes()), nil
		}
		fallthrough
	case reflect.Array:
		out := make([]any, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			val, err := toOutputStateValue(v.Index(i), depth+1, seen)
			if err != nil {
				return nil, err
			}
			out = append(out, val)
		}
		return out, nil

	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			if v.CanInterface() {
				return fmt.Sprintf("%v", v.Interface()), nil
			}
			return "<map>", nil
		}

		out := map[string]any{}
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool {
			return keys[i].String() < keys[j].String()
		})
		for _, key := range keys {
			val, err := toOutputStateValue(v.MapIndex(key), depth+1, seen)
			if err != nil {
				return nil, err
			}
			out[key.String()] = val
		}
		return out, nil

	case reflect.Struct:
		if v.CanInterface() {
			if ts, ok := v.Interface().(time.Time); ok {
				return ts.UTC().Format(time.RFC3339Nano), nil
			}
		}

		out := map[string]any{}
		typ := v.Type()
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name, ok := outputStateFieldName(field)
			if !ok {
				continue
			}
			val, err := toOutputStateValue(v.Field(i), depth+1, seen)
			if err != nil {
				return nil, err
			}
			out[name] = val
		}
		return out, nil

	default:
		if v.CanInterface() {
			if marshaler, ok := v.Interface().(json.Marshaler); ok {
				b, err := marshaler.MarshalJSON()
				if err == nil {
					var decoded any
					if err := json.Unmarshal(b, &decoded); err == nil {
						return decoded, nil
					}
				}
			}
			return fmt.Sprintf("%v", v.Interface()), nil
		}
		return nil, nil
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
