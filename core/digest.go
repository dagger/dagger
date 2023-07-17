package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"

	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

func stableDigest(value any) (digest.Digest, error) {
	buf := new(bytes.Buffer)

	if err := digestInto(value, buf); err != nil {
		return "", err
	}

	return digest.FromReader(buf)
}

func digestInto(value any, dest io.Writer) (err error) {
	defer func() {
		if err := recover(); err != nil {
			panic(fmt.Errorf("digest %T: %v", value, err))
		}
	}()

	switch x := value.(type) {
	case *pb.Definition:
		if x == nil {
			break
		}

		cp, err := stabilizeDef(x)
		if err != nil {
			return err
		}

		value = cp

	case []byte:
		// base64-encode bytes rather than treating it like a slice
		return json.NewEncoder(dest).Encode(value)
	}

	rt := reflect.TypeOf(value)
	rv := reflect.ValueOf(value)
	if rt.Kind() == reflect.Ptr {
		if rv.IsNil() {
			_, err := fmt.Fprintln(dest, "nil")
			return err
		}
		rt = rt.Elem()
		rv = rv.Elem()
	}

	switch rt.Kind() {
	case reflect.Struct:
		if err := digestStructInto(rt, rv, dest); err != nil {
			return fmt.Errorf("digest struct: %w", err)
		}
	case reflect.Slice, reflect.Array:
		if err := digestSliceInto(rv, dest); err != nil {
			return fmt.Errorf("digest slice/array: %w", err)
		}
	case reflect.Map:
		if err := digestMapInto(rv, dest); err != nil {
			return fmt.Errorf("digest map: %w", err)
		}
	case reflect.String,
		reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		if err := json.NewEncoder(dest).Encode(value); err != nil {
			return err
		}
	default:
		return fmt.Errorf("don't know how to digest %T", value)
	}

	return nil
}

func digestStructInto(rt reflect.Type, rv reflect.Value, dest io.Writer) error {
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		fmt.Fprintln(dest, name)
		if err := digestInto(rv.Field(i).Interface(), dest); err != nil {
			return fmt.Errorf("field %s: %w", name, err)
		}
	}

	return nil
}

func digestSliceInto(rv reflect.Value, dest io.Writer) error {
	for i := 0; i < rv.Len(); i++ {
		fmt.Fprintln(dest, i)
		if err := digestInto(rv.Index(i).Interface(), dest); err != nil {
			return fmt.Errorf("index %d: %w", i, err)
		}
	}

	return nil
}

func digestMapInto(rv reflect.Value, dest io.Writer) error {
	keys := rv.MapKeys()
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].String() < keys[j].String()
	})

	for _, k := range keys {
		if err := digestInto(k.Interface(), dest); err != nil {
			return fmt.Errorf("key %v: %w", k, err)
		}
		if err := digestInto(rv.MapIndex(k).Interface(), dest); err != nil {
			return fmt.Errorf("value for key %v: %w", k, err)
		}
	}

	return nil
}

// stabilizeDef returns a copy of def that has been pruned of any ephemeral
// data so that it may be used as a cache key that is stable across sessions.
func stabilizeDef(def *pb.Definition) (*pb.Definition, error) {
	cp := *def
	cp.Def = cloneSlice(def.Def)
	cp.Metadata = cloneMap(def.Metadata)
	cp.Source = nil // discard source map

	stabilized := map[digest.Digest]digest.Digest{}

	// first, stabilize all Ops
	for i, dt := range cp.Def {
		digBefore := digest.FromBytes(dt)
		var op pb.Op
		if err := (&op).Unmarshal(dt); err != nil {
			return nil, errors.Wrap(err, "failed to parse llb proto op")
		}

		if src := op.GetSource(); src != nil {
			// prevent ephemeral metadata from busting caches
			delete(src.Attrs, pb.AttrLocalSessionID)
			delete(src.Attrs, pb.AttrLocalUniqueID)
			delete(src.Attrs, pb.AttrSharedKeyHint) // has session ID + path
		}

		stableDt, err := op.Marshal()
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal llb proto op")
		}

		digAfter := digest.FromBytes(stableDt)
		if digAfter != digBefore {
			stabilized[digBefore] = digAfter
		}

		cp.Def[i] = stableDt
	}

	// update all inputs to reference the new digests
	if err := stabilizeInputs(&cp, stabilized); err != nil {
		return nil, errors.Wrap(err, "failed to stabilize inputs")
	}

	// finally, sort Def since it's in unstable topological order
	sort.Slice(cp.Def, func(i, j int) bool {
		return bytes.Compare(cp.Def[i], cp.Def[j]) < 0
	})

	return &cp, nil
}

// stabilizeInputs takes a mapping from old digests to new digests and updates
// all Op inputs to use the new digests instead. Because inputs are addressed
// by Op digests, any Ops that needed to be updated will thereby yield a new
// mapping, so stabilizeInputs will keep recursing until no new mappings are
// yielded.
func stabilizeInputs(cp *pb.Definition, stabilized map[digest.Digest]digest.Digest) error {
	nextPass := map[digest.Digest]digest.Digest{}

	for before, after := range stabilized {
		meta, found := cp.Metadata[before]
		if !found {
			return fmt.Errorf("missing metadata for %s", before)
		}

		cp.Metadata[after] = meta
		delete(cp.Metadata, before)
	}

	for i, dt := range cp.Def {
		digBefore := digest.FromBytes(dt)

		var op pb.Op
		if err := (&op).Unmarshal(dt); err != nil {
			return errors.Wrap(err, "failed to parse llb proto op")
		}
		for before, after := range stabilized {
			for _, input := range op.Inputs {
				if input.Digest == before {
					input.Digest = after
				}
			}
		}
		stableDt, err := op.Marshal()
		if err != nil {
			return errors.Wrap(err, "failed to marshal llb proto op")
		}
		digAfter := digest.FromBytes(stableDt)
		if digAfter != digBefore {
			nextPass[digBefore] = digAfter
		}
		cp.Def[i] = stableDt
	}

	if len(nextPass) > 0 {
		return stabilizeInputs(cp, nextPass)
	}

	return nil
}
