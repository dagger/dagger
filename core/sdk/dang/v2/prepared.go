package dangv2

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/vito/dang/v2/pkg/dang"
)

// Compilation is an invocation-local acceleration, never a replacement for a
// function result or its distributed cache identity. The session delimits its
// lifetime, and what is compiled identifies it: the staged source, whose
// directory names its content (see stagedDangSource), the Dang version it is
// compiled as, and the schema File it imports. A module source's recipe is no
// such identity: each load of a module (a session's, a client's, a dependent
// module's) has its own recipe for the same content. The staged directory is
// also where the compiled source's locations point, and it stays the same for
// as long as the source content does, however often the source is mounted
// again.
//
// An edited module is a new identity, so a long session can see many
// identities for one module. Only the maxPreparedDangPrograms most recently
// used of them are kept per module; see preparedDangPrograms.
func runPreparedDangDir(ctx context.Context, staged stagedDangSource, engineVersion string, schema dagql.Result[*core.File], module dagql.ObjectResult[*core.Module]) (dang.ValueScope, error) {
	dir := staged.dir
	run := func() (dang.ValueScope, error) { return dang.RunDir(ctx, dir, false) }
	_, cacheErr := dagql.EngineCache(ctx)
	metadata, metadataErr := engine.ClientMetadataFromContext(ctx)
	if cacheErr != nil || metadataErr != nil {
		return run()
	}
	schemaID, err := schema.ID()
	if err != nil {
		return run()
	}
	schemaKey, err := schemaID.Encode()
	if err != nil {
		return nil, err
	}
	self := module.Self()
	if self == nil {
		return run()
	}
	modKey := preparedDangKey(metadata.SessionID, self.Name(), self.OriginalName)
	key := preparedDangCompilationKey(staged, engineVersion, schemaKey)
	program, err := cachedPreparedDangDir(ctx, modKey, key, func(ctx context.Context) (*dang.PreparedDirectory, error) {
		program, err := dang.PrepareDir(ctx, dir)
		if errors.Is(err, dang.ErrUnpreparableDirectory) {
			return nil, nil
		}
		return program, err
	})
	if err != nil {
		return nil, err
	}
	if program == nil {
		return run()
	}
	// Evaluation failures must not fall back and repeat initializers/effects.
	return program.Evaluate(ctx)
}

// preparedDangCompilationKey identifies the compilation of the staged source
// for a module of engineVersion, importing the schema File encoded as
// schemaKey.
func preparedDangCompilationKey(source stagedDangSource, engineVersion, schemaKey string) string {
	return preparedDangKey(source.dir, source.version.String(), engineVersion, schemaKey)
}

func preparedDangKey(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		var size [8]byte
		binary.LittleEndian.PutUint64(size[:], uint64(len(part)))
		h.Write(size[:])
		h.Write([]byte(part))
	}
	return fmt.Sprintf("native-dang-prepared:%x", h.Sum(nil))
}

// cachedPreparedDangDir returns the program prepare compiles for the
// compilation identity key of the module identified by modKey, which must
// include the session. The session owns one preparedDangPrograms per module,
// released with it.
func cachedPreparedDangDir(ctx context.Context, modKey, key string, prepare func(context.Context) (*dang.PreparedDirectory, error)) (*dang.PreparedDirectory, error) {
	cache, cacheErr := dagql.EngineCache(ctx)
	metadata, metadataErr := engine.ClientMetadataFromContext(ctx)
	if cacheErr != nil || metadataErr != nil {
		return prepare(ctx)
	}
	result, err := cache.GetOrInitArbitrary(ctx, metadata.SessionID, "native-dang-prepared-module:"+modKey, func(context.Context) (any, error) {
		return &preparedDangPrograms{}, nil
	})
	if err != nil {
		return nil, err
	}
	programs, ok := result.Value().(*preparedDangPrograms)
	if !ok {
		return nil, fmt.Errorf("invalid prepared Dang programs %T", result.Value())
	}
	return programs.get(ctx, key, prepare)
}

// maxPreparedDangPrograms bounds the compilations a session keeps for one
// module. A compilation holds the module's syntax and every type of the API
// it imports, inferred.
const maxPreparedDangPrograms = 4

// preparedDangPrograms holds a module's most recently used compilations. Calls
// for one compilation identity share one preparation, failures are forgotten,
// and when a new identity is prepared beyond the bound the least recently
// used is dropped. A dropped compilation stays valid for the calls already
// using it.
type preparedDangPrograms struct {
	mu sync.Mutex
	// builds holds the kept compilations, most recently used first.
	builds []*preparedDangBuild
}

type preparedDangBuild struct {
	key string
	// done is closed once program, err and ownerCanceled are set.
	done    chan struct{}
	program *dang.PreparedDirectory
	err     error
	// ownerCanceled says the preparation failed once the call running it was
	// canceled.
	ownerCanceled bool
	// joined counts the calls that found the compilation kept; guarded by
	// preparedDangPrograms.mu.
	joined int
}

func (p *preparedDangPrograms) get(ctx context.Context, key string, prepare func(context.Context) (*dang.PreparedDirectory, error)) (*dang.PreparedDirectory, error) {
	for {
		build, owner := p.acquire(key)
		if owner {
			return p.prepare(ctx, build, prepare)
		}
		select {
		case <-build.done:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
		if build.err == nil {
			return build.program, nil
		}
		// A preparation that failed once its own caller was canceled, with
		// whatever cause, says nothing about this one, which prepares again.
		if ctx.Err() == nil && build.ownerCanceled {
			continue
		}
		return nil, build.err
	}
}

// prepare runs the preparation of build and publishes its outcome to the calls
// waiting on it.
func (p *preparedDangPrograms) prepare(ctx context.Context, build *preparedDangBuild, prepare func(context.Context) (*dang.PreparedDirectory, error)) (*dang.PreparedDirectory, error) {
	defer func() {
		if r := recover(); r != nil {
			build.err = fmt.Errorf("prepare Dang module: panic: %v", r)
			p.remove(build)
			close(build.done)
			panic(r)
		}
	}()
	build.program, build.err = prepare(ctx)
	build.ownerCanceled = build.err != nil && ctx.Err() != nil
	if build.err != nil {
		p.remove(build)
	}
	close(build.done)
	return build.program, build.err
}

// acquire returns the compilation for key, and whether the caller must
// prepare it.
func (p *preparedDangPrograms) acquire(key string) (*preparedDangBuild, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, build := range p.builds {
		if build.key == key {
			copy(p.builds[1:i+1], p.builds[:i])
			p.builds[0] = build
			build.joined++
			return build, false
		}
	}
	build := &preparedDangBuild{key: key, done: make(chan struct{})}
	if len(p.builds) >= maxPreparedDangPrograms {
		clear(p.builds[maxPreparedDangPrograms-1:])
		p.builds = p.builds[:maxPreparedDangPrograms-1]
	}
	p.builds = slices.Insert(p.builds, 0, build)
	return build, true
}

// remove forgets a failed compilation, so that the next call prepares again.
func (p *preparedDangPrograms) remove(build *preparedDangBuild) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.builds = slices.DeleteFunc(p.builds, func(b *preparedDangBuild) bool { return b == build })
}
