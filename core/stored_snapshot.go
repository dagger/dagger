package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// storedSnapshot belongs to one restored value. Its identity survives opening
// and removal of the operational lazy pointer; children do not inherit it.
type storedSnapshot struct {
	SnapshotID string
}

type DirectoryRestoreLazy struct{ LazyState }
type FileRestoreLazy struct{ LazyState }

func (lazy *DirectoryRestoreLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return lazy.LazyState.Evaluate(ctx, "Directory.restore", func(ctx context.Context) error {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}
		snapshot, err := query.SnapshotManager().GetBySnapshotID(ctx, dir.stored.SnapshotID, bkcache.NoUpdateLastUsed)
		if err != nil {
			return err
		}
		dir.Snapshot.setValue(snapshot)
		return nil
	})
}

func (*DirectoryRestoreLazy) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (*DirectoryRestoreLazy) EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error) {
	return nil, fmt.Errorf("restored directory must encode its stored snapshot")
}

func (lazy *FileRestoreLazy) Evaluate(ctx context.Context, file *File) error {
	return lazy.LazyState.Evaluate(ctx, "File.restore", func(ctx context.Context) error {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}
		snapshot, err := query.SnapshotManager().GetBySnapshotID(ctx, file.stored.SnapshotID, bkcache.NoUpdateLastUsed)
		if err != nil {
			return err
		}
		file.Snapshot.setValue(snapshot)
		return nil
	})
}

func (*FileRestoreLazy) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (*FileRestoreLazy) EncodePersisted(context.Context, dagql.PersistedObjectCache) (json.RawMessage, error) {
	return nil, fmt.Errorf("restored file must encode its stored snapshot")
}

var _ dagql.HasLazyEvaluationReporting = (*Directory)(nil)
var _ dagql.HasLazyEvaluationReporting = (*File)(nil)

func (dir *Directory) HasPendingLazyComputation() bool {
	return dir != nil && dir.stored == nil && dir.Lazy != nil
}

func (dir *Directory) LazyGroupStoredPart(group dagql.LazyGroupKey) dagql.PartKey {
	if dir != nil && dir.stored != nil && group == dagql.LazyGroupWhole {
		return "snapshot"
	}
	return ""
}

func (file *File) HasPendingLazyComputation() bool {
	return file != nil && file.stored == nil && file.Lazy != nil
}

func (file *File) LazyGroupStoredPart(group dagql.LazyGroupKey) dagql.PartKey {
	if file != nil && file.stored != nil && group == dagql.LazyGroupWhole {
		return "snapshot"
	}
	return ""
}

// PathOrEval returns saved metadata without opening its snapshot. Fresh values
// still need evaluation, even when their path accessor has been prefilled.
func (dir *Directory) PathOrEval(ctx context.Context, self dagql.ObjectResult[*Directory]) (string, error) {
	if dir.stored != nil {
		if path, ok := dir.Dir.Peek(); ok {
			return path, nil
		}
		return "", fmt.Errorf("restored directory has no saved path")
	}
	return dir.Dir.GetOrEval(ctx, self.Result)
}

func (file *File) PathOrEval(ctx context.Context, self dagql.ObjectResult[*File]) (string, error) {
	if file.stored != nil {
		if path, ok := file.File.Peek(); ok {
			return path, nil
		}
		return "", fmt.Errorf("restored file has no saved path")
	}
	return file.File.GetOrEval(ctx, self.Result)
}

// SourceFilePaths evaluates fresh sources together, then reads every path.
func SourceFilePaths(ctx context.Context, files []dagql.ObjectResult[*File]) ([]string, error) {
	var fresh []dagql.AnyResult
	for _, file := range files {
		if file.Self().stored == nil {
			fresh = append(fresh, file)
		}
	}
	if len(fresh) != 0 {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return nil, err
		}
		if err := cache.Evaluate(ctx, fresh...); err != nil {
			return nil, err
		}
	}
	paths := make([]string, len(files))
	for i, file := range files {
		path, err := file.Self().PathOrEval(ctx, file)
		if err != nil {
			return nil, err
		}
		paths[i] = path
	}
	return paths, nil
}
