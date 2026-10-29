package dangv2

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/vito/dang/v2/pkg/dang"
)

// Compilation is an invocation-local acceleration, never a replacement for a
// function result or its distributed cache identity. A complete immutable
// source/schema identity and session delimit its lifetime. The mounted path is
// included so retained source locations always describe the current mount.
func runPreparedDangDir(ctx context.Context, dir string, source dagql.ObjectResult[*core.ModuleSource], schema dagql.Result[*core.File], module dagql.ObjectResult[*core.Module]) (dang.ValueScope, error) {
	run := func() (dang.ValueScope, error) { return dang.RunDir(ctx, dir, false) }
	_, cacheErr := dagql.EngineCache(ctx)
	metadata, metadataErr := engine.ClientMetadataFromContext(ctx)
	if cacheErr != nil || metadataErr != nil {
		return run()
	}
	sourceID, err := source.ID()
	if err != nil {
		return run()
	}
	sourceKey, err := sourceID.Encode()
	if err != nil {
		return nil, err
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
	key := preparedDangKey(metadata.SessionID, sourceKey, schemaKey, self.Name(), self.OriginalName, dir)
	program, err := cachedPreparedDangDir(ctx, key, func(ctx context.Context) (*dang.PreparedDirectory, error) {
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

func cachedPreparedDangDir(ctx context.Context, key string, prepare func(context.Context) (*dang.PreparedDirectory, error)) (*dang.PreparedDirectory, error) {
	cache, cacheErr := dagql.EngineCache(ctx)
	metadata, metadataErr := engine.ClientMetadataFromContext(ctx)
	if cacheErr != nil || metadataErr != nil {
		return prepare(ctx)
	}
	result, err := cache.GetOrInitArbitrary(ctx, metadata.SessionID, key, func(ctx context.Context) (any, error) { return prepare(ctx) })
	if err != nil {
		return nil, err
	}
	program, ok := result.Value().(*dang.PreparedDirectory)
	if !ok {
		return nil, fmt.Errorf("invalid prepared Dang source %T", result.Value())
	}
	return program, nil
}
