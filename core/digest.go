package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"time"

	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/sources/gitdns"
	"github.com/dagger/dagger/engine/sources/httpdns"
	"github.com/dagger/dagger/network"
	"github.com/koron-go/prefixw"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
)

var debugDigest = false
var debugDigestLogs = fmt.Sprintf("/tmp/digests-%d.log", time.Now().UnixNano())
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
	def.Source = nil // discard source map
	dag, err := defToDAG(def)
	if err != nil {
		return nil, err
	}
	err = dag.Walk(stabilizeOp)
	if err != nil {
		return nil, err
	}
	def, err = dag.Marshal()
	if err != nil {
		return nil, err
	}

	// sort Def since it's in unstable topological order
	sort.Slice(def.Def, func(i, j int) bool {
		return bytes.Compare(def.Def[i], def.Def[j]) < 0
	})

	return def, nil
}

func stabilizeOp(op *opDAG) error {
	if src := op.GetSource(); src != nil {
		for k := range src.Attrs {
			switch k {
			case pb.AttrLocalSessionID,
				pb.AttrLocalUniqueID,
				pb.AttrSharedKeyHint: // contains session ID
				delete(src.Attrs, k)
			}
		}

		var opts buildkit.LocalImportOpts
		if err := buildkit.DecodeIDHack("local", src.Identifier, &opts); err == nil {
			src.Identifier = "local://" + opts.Path
		}

		var httpHack httpdns.DaggerHTTPURLHack
		if err := buildkit.DecodeIDHack("https", src.Identifier, &httpHack); err == nil {
			src.Identifier = httpHack.URL
		}

		var gitHack gitdns.DaggerGitURLHack
		if err := buildkit.DecodeIDHack("git", src.Attrs[pb.AttrFullRemoteURL], &gitHack); err == nil {
			src.Attrs[pb.AttrFullRemoteURL] = gitHack.Remote
		}
	}

	if exe := op.GetExec(); exe != nil {
		if exe.Meta.ProxyEnv != nil {
			// NB(vito): ProxyEnv is used for passing network configuration along
			// without busting the cache.
			exe.Meta.ProxyEnv = nil
		}
	}

	return nil
}
