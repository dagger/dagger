package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"

	"github.com/dagger/dagger/engine/sources/netconf"
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

		digest, err := digestLLB(context.TODO(), x, stabilizeSourcePolicy{})
		if err != nil {
			return err
		}

		_, err = fmt.Fprintln(dest, digest)
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

type sourcePolicyEvaluator interface {
	Evaluate(ctx context.Context, op *pb.Op) (bool, error)
}

// TODO(vito): this is an extracted/trimmed down implementation of
// llbsolver.Load from upstream Buildkit. Ideally we would use it directly but
// we have to avoid importing that package because it breaks the Darwin build.
func digestLLB(ctx context.Context, def *pb.Definition, polEngine sourcePolicyEvaluator) (digest.Digest, error) {
	if len(def.Def) == 0 {
		return "", errors.New("invalid empty definition")
	}

	allOps := make(map[digest.Digest]*pb.Op)
	mutatedDigests := make(map[digest.Digest]digest.Digest) // key: old, val: new

	var lastDgst digest.Digest

	for _, dt := range def.Def {
		var op pb.Op
		if err := (&op).Unmarshal(dt); err != nil {
			return "", errors.Wrap(err, "failed to parse llb proto op")
		}
		dgst := digest.FromBytes(dt)
		if polEngine != nil {
			mutated, err := polEngine.Evaluate(ctx, &op)
			if err != nil {
				return "", errors.Wrap(err, "error evaluating the source policy")
			}
			if mutated {
				dtMutated, err := op.Marshal()
				if err != nil {
					return "", err
				}
				dgstMutated := digest.FromBytes(dtMutated)
				mutatedDigests[dgst] = dgstMutated
				dgst = dgstMutated
			}
		}
		allOps[dgst] = &op
		lastDgst = dgst
	}

	for dgst := range allOps {
		_, err := recomputeDigests(ctx, allOps, mutatedDigests, dgst)
		if err != nil {
			return "", err
		}
	}

	if len(allOps) < 2 {
		return "", errors.Errorf("invalid LLB with %d vertexes", len(allOps))
	}

	for {
		newDgst, ok := mutatedDigests[lastDgst]
		if !ok || newDgst == lastDgst {
			break
		}
		lastDgst = newDgst
	}

	lastOp := allOps[lastDgst]
	delete(allOps, lastDgst)
	if len(lastOp.Inputs) == 0 {
		return "", errors.Errorf("invalid LLB with no inputs on last vertex")
	}

	dgst := lastOp.Inputs[0].Digest

	return dgst, nil
}

func recomputeDigests(ctx context.Context, all map[digest.Digest]*pb.Op, visited map[digest.Digest]digest.Digest, dgst digest.Digest) (digest.Digest, error) {
	if dgst, ok := visited[dgst]; ok {
		return dgst, nil
	}
	op := all[dgst]

	var mutated bool
	for _, input := range op.Inputs {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		iDgst, err := recomputeDigests(ctx, all, visited, input.Digest)
		if err != nil {
			return "", err
		}
		if input.Digest != iDgst {
			mutated = true
			input.Digest = iDgst
		}
	}

	if !mutated {
		visited[dgst] = dgst
		return dgst, nil
	}

	dt, err := op.Marshal()
	if err != nil {
		return "", err
	}
	newDgst := digest.FromBytes(dt)
	visited[dgst] = newDgst
	all[newDgst] = op
	delete(all, dgst)
	return newDgst, nil
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
				pb.AttrSharedKeyHint,  // contains session ID
				netconf.AttrSessionID: // network config session IDs
				delete(src.Attrs, k)
				modified = true
			}
		}
		return modified, nil
	}

	return false, nil
}
