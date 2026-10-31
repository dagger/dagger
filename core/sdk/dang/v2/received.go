package dangv2

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/zeebo/xxh3"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// receivedObject is a JSON object a function call received, in its parent or
// an argument. Equal objects within one received value are one
// receivedObject, so a call rebuilds each of them once: an object nested in
// every item of a collection, such as the module each item belongs to, is
// sent once per item but rebuilt once.
//
// A decoded value is shared by every call that receives the same JSON, and
// must not be modified.
type receivedObject struct {
	fields map[string]any
}

// decodeReceived decodes a function call's parent or argument JSON. Every
// call on one collection item receives the whole collection, so the decoded
// value is kept, keyed by the JSON's content, while a session that received it
// is open. It is plain data, so sessions that receive the same JSON share it.
func decodeReceived(ctx context.Context, data []byte) (any, error) {
	cache, cacheErr := dagql.EngineCache(ctx)
	metadata, metadataErr := engine.ClientMetadataFromContext(ctx)
	if cacheErr != nil || metadataErr != nil {
		return decodeReceivedJSON(data)
	}
	sum := xxh3.Hash128(data).Bytes()
	key := fmt.Sprintf("native-dang-received:%d:%x", len(data), sum)
	res, err := cache.GetOrInitArbitrary(ctx, metadata.SessionID, key, func(context.Context) (any, error) {
		val, err := decodeReceivedJSON(data)
		if err != nil {
			return nil, err
		}
		return receivedValue{val}, nil
	})
	if err != nil {
		return nil, err
	}
	val, ok := res.Value().(receivedValue)
	if !ok {
		return nil, fmt.Errorf("invalid decoded value %T", res.Value())
	}
	return val.v, nil
}

// receivedValue boxes a decoded value, which may be nil, for the cache.
type receivedValue struct{ v any }

func decodeReceivedJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var val any
	if err := dec.Decode(&val); err != nil {
		return nil, err
	}
	in := receivedInterner{objects: map[xxh3.Uint128][]*receivedObject{}}
	val, _ = in.intern(val)
	return val, nil
}

// receivedInterner replaces each JSON object of a decoded value with a
// receivedObject, the same one for equal objects. It works bottom up, so the
// objects inside two equal objects are already the same, and comparing the two
// needs only one level.
type receivedInterner struct {
	objects map[xxh3.Uint128][]*receivedObject
	buf     []byte
}

// intern returns val with its objects interned, and a hash of its content.
func (in *receivedInterner) intern(val any) (any, xxh3.Uint128) {
	switch v := val.(type) {
	case map[string]any:
		keys := slices.Sorted(maps.Keys(v))
		hashes := make([]xxh3.Uint128, len(keys))
		for i, k := range keys {
			v[k], hashes[i] = in.intern(v[k])
		}
		in.buf = append(in.buf[:0], 'o')
		for i, k := range keys {
			in.buf = appendHashedString(in.buf, k)
			in.buf = appendHash(in.buf, hashes[i])
		}
		sum := xxh3.Hash128(in.buf)
		for _, obj := range in.objects[sum] {
			if receivedFieldsEqual(obj.fields, v) {
				return obj, sum
			}
		}
		obj := &receivedObject{fields: v}
		in.objects[sum] = append(in.objects[sum], obj)
		return obj, sum
	case []any:
		hashes := make([]xxh3.Uint128, len(v))
		for i := range v {
			v[i], hashes[i] = in.intern(v[i])
		}
		in.buf = append(in.buf[:0], 'l')
		for _, h := range hashes {
			in.buf = appendHash(in.buf, h)
		}
		return v, xxh3.Hash128(in.buf)
	case string:
		return v, xxh3.Hash128(appendHashedString(append(in.buf[:0], 's'), v))
	case json.Number:
		return v, xxh3.Hash128(appendHashedString(append(in.buf[:0], 'n'), string(v)))
	case bool:
		if v {
			return v, xxh3.Hash128([]byte{'t'})
		}
		return v, xxh3.Hash128([]byte{'f'})
	default: // nil
		return v, xxh3.Hash128([]byte{'z'})
	}
}

func appendHash(buf []byte, h xxh3.Uint128) []byte {
	buf = binary.LittleEndian.AppendUint64(buf, h.Hi)
	return binary.LittleEndian.AppendUint64(buf, h.Lo)
}

func appendHashedString(buf []byte, s string) []byte {
	buf = binary.LittleEndian.AppendUint64(buf, uint64(len(s)))
	return append(buf, s...)
}

// receivedFieldsEqual compares two interned objects' fields. Objects inside
// them are interned already, so they are equal only if they are the same.
func receivedFieldsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || !receivedValuesEqual(av, bv) {
			return false
		}
	}
	return true
}

func receivedValuesEqual(a, b any) bool {
	switch a := a.(type) {
	case []any:
		b, ok := b.([]any)
		return ok && slices.EqualFunc(a, b, receivedValuesEqual)
	case *receivedObject:
		b, ok := b.(*receivedObject)
		return ok && a == b
	default: // string, json.Number, bool or nil
		return a == b
	}
}

// decodeReceivedParent decodes a function call's parent, a JSON object, into
// its fields.
func decodeReceivedParent(ctx context.Context, data []byte) (map[string]any, error) {
	val, err := decodeReceived(ctx, data)
	if err != nil {
		return nil, err
	}
	switch v := val.(type) {
	case *receivedObject:
		return v.fields, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("expected an object, got %T", val)
	}
}
