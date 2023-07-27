package core

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/network"
	"github.com/koron-go/prefixw"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

var debugDigest = false
var debugDigestLogs string = fmt.Sprintf("/tmp/digests-%d.log", time.Now().UnixNano())
var debugDigestLogsW = io.Discard

func init() {
	if debugDigest {
		var err error
		debugDigestLogsW, err = os.Create(debugDigestLogs)
		if err != nil {
			panic(err)
		}
	}
}

func stableDigest(value any) (digest.Digest, error) {
	buf := new(bytes.Buffer)

	var dest io.Writer = buf
	var debugTag string
	var debugW = io.Discard

	if debugDigest {
		if x, ok := value.(Digestible); ok && x != nil {
			debugTag = identity.NewID()
			debugW = prefixw.New(debugDigestLogsW, fmt.Sprintf("%s %T >> ", debugTag, x))
			fmt.Fprintln(debugW, "BEGIN")
			dest = io.MultiWriter(dest, debugW)
		}
	}

	if err := stableDigestInto(value, dest); err != nil {
		return "", err
	}

	dig, err := digest.FromReader(buf)
	if err != nil {
		return "", err
	}

	if debugDigest {
		if x, ok := value.(Digestible); ok && x != nil {
			fmt.Fprintln(debugW, "END", network.HostHash(dig))
		}
	}

	return dig, nil
}

// stableDigestInto handles digesting Go built-in types without any sort of
// specialization.
func stableDigestInto(value any, dest io.Writer) (err error) {
	defer func() {
		if err := recover(); err != nil {
			panic(fmt.Errorf("digest %T: %v", value, err))
		}
	}()

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

var stableDefCache = newCacheMap[*pb.Definition, *pb.Definition]()

// digestInner handles digesting inner content, looking for special types and
// respecting the Digestible interface.
//
// It is separate from digestBuiltin so that Digestible implementations can use
// digestBuiltin against themselves.
func digestInner(value any, dest io.Writer) error {
	switch x := value.(type) {
	case *pb.Definition:
		if x == nil {
			_, err := fmt.Fprintln(dest, "nil")
			return err
		}

		stabilized, err := stableDefCache.GetOrInitialize(x, func() (*pb.Definition, error) {
			return stabilizeDef(x)
		})
		if err != nil {
			return err
		}

		return stableDigestInto(stabilized, dest)

	case Digestible:
		digest, err := x.Digest()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(dest, digest)
		return err

	case []byte:
		// base64-encode bytes rather than treating it like a slice
		return json.NewEncoder(dest).Encode(value)

	default:
		return stableDigestInto(value, dest)
	}
}

func digestStructInto(rt reflect.Type, rv reflect.Value, dest io.Writer) error {
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if _, err := fmt.Fprintln(dest, name); err != nil {
			return err
		}
		if err := digestInner(rv.Field(i).Interface(), dest); err != nil {
			return fmt.Errorf("field %s: %w", name, err)
		}
	}

	return nil
}

func digestSliceInto(rv reflect.Value, dest io.Writer) error {
	for i := 0; i < rv.Len(); i++ {
		if _, err := fmt.Fprintln(dest, i); err != nil {
			return err
		}
		if err := digestInner(rv.Index(i).Interface(), dest); err != nil {
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
		if err := digestInner(k.Interface(), dest); err != nil {
			return fmt.Errorf("key %v: %w", k, err)
		}
		if err := digestInner(rv.MapIndex(k).Interface(), dest); err != nil {
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

		modified, err := stabilizeOp(&op)
		if err != nil {
			return nil, errors.Wrap(err, "failed to stabilize op")
		}

		if modified {
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
	// swap metadata from old digests to new digests
	for before, after := range stabilized {
		meta, found := cp.Metadata[before]
		if !found {
			return fmt.Errorf("missing metadata for %s", before)
		}

		cp.Metadata[after] = meta
		delete(cp.Metadata, before)
	}

	nextPass := map[digest.Digest]digest.Digest{}

	// swap inputs from old digests to new digests
	//
	// doing so yields a new Op payload and a new digest
	for i, dt := range cp.Def {
		digBefore := digest.FromBytes(dt)

		var op pb.Op
		if err := (&op).Unmarshal(dt); err != nil {
			return errors.Wrap(err, "failed to parse llb proto op")
		}

		var modified bool
		for _, input := range op.Inputs {
			after, found := stabilized[input.Digest]
			if found {
				input.Digest = after
				modified = true
			}
		}

		if modified {
			stableDt, err := op.Marshal()
			if err != nil {
				return errors.Wrap(err, "failed to marshal llb proto op")
			}

			cp.Def[i] = stableDt

			digAfter := digest.FromBytes(stableDt)

			// track the new mapping for any Ops that had this Op as an
			nextPass[digBefore] = digAfter
		}
	}

	if len(nextPass) > 0 {
		return stabilizeInputs(cp, nextPass)
	}

	return nil
}

func stabilizeOp(op *pb.Op) (bool, error) {
	var modified bool

	if src := op.GetSource(); src != nil {
		for k := range src.Attrs {
			switch k {
			case pb.AttrLocalSessionID,
				pb.AttrLocalUniqueID,
				pb.AttrSharedKeyHint: // contains session ID
				delete(src.Attrs, k)
				modified = true
			}
		}
		if _, name, ok := strings.Cut(src.Identifier, "local://"); ok {
			var opts buildkit.LocalImportOpts
			jsonBytes, err := base64.URLEncoding.DecodeString(name)
			if err != nil {
				return false, fmt.Errorf("invalid import local dir name: %q", name)
			}
			if err := json.Unmarshal(jsonBytes, &opts); err != nil {
				return false, fmt.Errorf("invalid import local dir name: %q", name)
			}
			src.Identifier = "local://" + opts.Path
			modified = true
		}
	}

	if exe := op.GetExec(); exe != nil {
		if exe.Meta.ProxyEnv != nil {
			// NB(vito): ProxyEnv is used for passing network configuration along
			// without busting the cache.
			exe.Meta.ProxyEnv = nil
			modified = true
		}
	}

	return modified, nil
}
