package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dagger/dagger/dagql"
)

// DirectoryScratchLazy opens the canonical empty directory on first use.
// Platform belongs to the receiver's persisted value.
type DirectoryScratchLazy struct{ LazyState }

type persistedDirectoryScratchLazy struct{}

func validateDirectoryScratchPayload(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("scratch Directory lazy: expected empty JSON object: %w", err)
	}
	if fields == nil || len(fields) != 0 {
		return fmt.Errorf("scratch Directory lazy: expected empty JSON object")
	}
	return nil
}

func (*DirectoryScratchLazy) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (*DirectoryScratchLazy) EncodePersisted(context.Context, *dagql.PersistEncodeContext) (json.RawMessage, error) {
	return json.Marshal(persistedDirectoryScratchLazy{})
}

func (lazy *DirectoryScratchLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return dir.evaluateLazy(ctx, &lazy.LazyState, "Query.directory", func(ctx context.Context) (rerr error) {
		if err := validateLazyDirectoryReceiver(dir); err != nil {
			return err
		}
		path, ref, err := loadCanonicalScratchDirectory(ctx)
		if err != nil {
			return err
		}
		defer func() {
			if ref != nil {
				rerr = errors.Join(rerr, ref.Release(context.WithoutCancel(ctx)))
			}
		}()
		if err := ctx.Err(); err != nil {
			return err
		}
		dir.SetPath(path)
		dir.SetSnapshot(ref)
		ref = nil
		return nil
	})
}
