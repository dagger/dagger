package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"

	"github.com/moby/buildkit/solver/llbsolver"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
)

func stableDigest(value any) (digest.Digest, error) {
	buf := new(bytes.Buffer)

	if err := digestInto(value, buf); err != nil {
		return "", err
	}

	return digest.FromReader(buf)
}

// stabilizeSourcePolicy removes ephemeral metadata from ops to prevent it from
// busting caches.
type stabilizeSourcePolicy struct{}

func (stabilizeSourcePolicy) Evaluate(ctx context.Context, op *pb.Op) (bool, error) {
	if src := op.GetSource(); src != nil {
		var modified bool
		for k := range src.Attrs {
			switch k {
			case pb.AttrLocalSessionID,
				pb.AttrLocalUniqueID,
				pb.AttrSharedKeyHint: // contains session ID
				delete(src.Attrs, k)
				modified = true
			}
		}
		return modified, nil
	}

	return false, nil
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

		edge, err := llbsolver.Load(context.TODO(), x, stabilizeSourcePolicy{})
		if err != nil {
			return err
		}

		_, err = fmt.Fprintln(dest, edge.Vertex.Digest())
		return err

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
