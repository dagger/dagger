package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// storedSnapshot belongs to one restored value. Its identity survives opening
// independently of the retained operation; children do not inherit it.
type storedSnapshot struct {
	SnapshotID string
}

type DirectoryRestoreLazy struct{ LazyState }
type FileRestoreLazy struct{ LazyState }

func (lazy *DirectoryRestoreLazy) Evaluate(ctx context.Context, dir *Directory) error {
	return dir.evaluateLazy(ctx, &lazy.LazyState, "Directory.restore", func(ctx context.Context) error {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}
		snapshot, err := query.SnapshotManager().GetBySnapshotID(ctx, dir.stored.SnapshotID, bkcache.NoUpdateLastUsed)
		if err != nil {
			return err
		}
		dir.SetSnapshot(snapshot)
		dir.storedDiagnostics.opened()
		return nil
	})
}

func (*DirectoryRestoreLazy) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (*DirectoryRestoreLazy) EncodePersisted(context.Context, *dagql.PersistEncodeContext) (json.RawMessage, error) {
	return nil, fmt.Errorf("restored directory must encode its stored snapshot")
}

func (lazy *FileRestoreLazy) Evaluate(ctx context.Context, file *File) error {
	return file.evaluateLazy(ctx, &lazy.LazyState, "File.restore", func(ctx context.Context) error {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return err
		}
		snapshot, err := query.SnapshotManager().GetBySnapshotID(ctx, file.stored.SnapshotID, bkcache.NoUpdateLastUsed)
		if err != nil {
			return err
		}
		file.SetSnapshot(snapshot)
		file.storedDiagnostics.opened()
		return nil
	})
}

func (*FileRestoreLazy) AttachDependencies(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

func (*FileRestoreLazy) EncodePersisted(context.Context, *dagql.PersistEncodeContext) (json.RawMessage, error) {
	return nil, fmt.Errorf("restored file must encode its stored snapshot")
}

var _ dagql.HasLazyEvaluationReporting = (*Directory)(nil)
var _ dagql.HasLazyEvaluationReporting = (*File)(nil)

func (dir *Directory) HasPendingLazyComputation() bool {
	if dir == nil {
		return false
	}
	dir.outputMu.RLock()
	defer dir.outputMu.RUnlock()
	return dir.stored == nil && dir.Lazy != nil && !dir.Lazy.IsEvaluated()
}

func (dir *Directory) LazyGroupStoredPart(group dagql.LazyGroupKey) dagql.PartKey {
	if dir != nil && dir.stored != nil && group == dagql.LazyGroupWhole {
		return "snapshot"
	}
	return ""
}

func (file *File) HasPendingLazyComputation() bool {
	if file == nil {
		return false
	}
	file.outputMu.RLock()
	defer file.outputMu.RUnlock()
	return file.stored == nil && file.Lazy != nil && !file.Lazy.IsEvaluated()
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
	dir.outputMu.RLock()
	known := dir.stored != nil || dir.transferPending != nil && dir.transferPending.ValueKnown
	path, ok := dir.Dir.Peek()
	dir.outputMu.RUnlock()
	if known {
		if ok {
			return path, nil
		}
		return "", fmt.Errorf("restored directory has no saved path")
	}
	return dir.Dir.GetOrEval(ctx, self.Result)
}

func (file *File) PathOrEval(ctx context.Context, self dagql.ObjectResult[*File]) (string, error) {
	file.outputMu.RLock()
	needsEval := file.pathNeedsEvalLocked()
	path, ok := file.File.Peek()
	file.outputMu.RUnlock()
	if needsEval {
		return file.File.GetOrEval(ctx, self.Result)
	}
	if !ok {
		return "", fmt.Errorf("restored file has no saved path")
	}
	return path, nil
}

// pathNeedsEvalLocked reports whether reading the file's path takes evaluating
// it: the path was neither saved with the value nor set by a computation that
// has finished. A path prefilled before then isn't final. Requires outputMu.
func (file *File) pathNeedsEvalLocked() bool {
	if file.stored != nil {
		return false
	}
	if file.transferPending != nil {
		return !file.transferPending.ValueKnown
	}
	if file.Lazy != nil && !file.Lazy.IsEvaluated() {
		return true
	}
	_, ok := file.File.Peek()
	return !ok
}

// SourceFilePaths evaluates together the sources whose paths need it, then reads
// every path.
func SourceFilePaths(ctx context.Context, files []dagql.ObjectResult[*File]) ([]string, error) {
	var fresh []dagql.AnyResult
	for _, file := range files {
		value := file.Self()
		value.outputMu.RLock()
		needsEval := value.pathNeedsEvalLocked()
		value.outputMu.RUnlock()
		if needsEval {
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
