package core

import (
	"context"
	"maps"
	"os"
	"strings"
	"sync"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// This opt-in diagnostic supports real engine restart tests. It records only
// this process's work and is never persisted or copied to schema children.
var containerPartDiagnosticsEnabled = os.Getenv("_DAGGER_TEST_CONTAINER_PART_DIAGNOSTICS") == "1"

type containerPartDiagnostics struct {
	mu     sync.Mutex
	counts map[string]uint64
}

func (ctr *Container) recordPartDiagnostic(event string, part dagql.PartKey) {
	if !containerPartDiagnosticsEnabled {
		return
	}
	ctr.lazyOpMu.Lock()
	if ctr.partDiagnostics == nil {
		ctr.partDiagnostics = &containerPartDiagnostics{counts: map[string]uint64{}}
	}
	stats := ctr.partDiagnostics
	ctr.lazyOpMu.Unlock()
	if part != "" {
		event += ":" + string(part)
	}
	stats.mu.Lock()
	stats.counts[event]++
	stats.mu.Unlock()
}

func (ctr *Container) directoryCommitObserver() func() {
	if !containerPartDiagnosticsEnabled {
		return nil
	}
	return func() { ctr.recordPartDiagnostic("directoryCommit", "") }
}

type containerPartDebugValue struct {
	Computed         bool               `json:"computed"`
	Group            dagql.LazyGroupKey `json:"group,omitempty"`
	Consumed         bool               `json:"consumed"`
	StoredKind       string             `json:"storedKind,omitempty"`
	StoredSnapshotID string             `json:"storedSnapshotID,omitempty"`
	OpenSnapshotID   string             `json:"openSnapshotID,omitempty"`
}

// CacheDebugValue reads synchronized body latches and accessors only. Metadata
// must have completed before its settled mount list can be inspected.
func (ctr *Container) CacheDebugValue() any {
	if !containerPartDiagnosticsEnabled || ctr == nil {
		return nil
	}
	ctr.lazyOpMu.Lock()
	lazy, stats := ctr.Lazy, ctr.partDiagnostics
	ctr.lazyOpMu.Unlock()
	counts := map[string]uint64{}
	if stats != nil {
		stats.mu.Lock()
		counts = maps.Clone(stats.counts)
		stats.mu.Unlock()
	}
	ctx := context.Background()
	parts := []dagql.PartKey{ContainerPartMetadata}
	if ctr.containerPartComputed(ctx, lazy, ContainerPartMetadata) {
		parts = append(parts, containerSnapshotParts(ctr)...)
	}
	values := make(map[dagql.PartKey]containerPartDebugValue, len(parts))
	for _, part := range parts {
		stored := ctr.storedParts[part]
		value := containerPartDebugValue{
			Computed: ctr.containerPartComputed(ctx, lazy, part),
			Consumed: lazy == nil, StoredKind: stored.Kind, StoredSnapshotID: stored.SnapshotID,
		}
		if op, ok := lazy.(LazyContainerParts); ok {
			if groups, err := op.ContainerLazyGroups(ctx, ctr, []dagql.PartKey{part}); err == nil && len(groups) == 1 {
				value.Group = groups[0]
				value.Consumed = op.ContainerLazyState().GroupConsumed(groups[0])
			}
		}
		if part != ContainerPartMetadata {
			value.OpenSnapshotID = ctr.openContainerSnapshotID(part)
		}
		values[part] = value
	}
	return struct {
		Parts  map[dagql.PartKey]containerPartDebugValue `json:"parts"`
		Counts map[string]uint64                         `json:"counts"`
	}{values, counts}
}

func (ctr *Container) openContainerSnapshotID(part dagql.PartKey) string {
	var snapshot bkcache.ImmutableRef
	var dir *Directory
	var file *File
	switch part {
	case ContainerPartFS:
		if ctr.FS != nil {
			dir, _ = ctr.FS.Peek()
		}
	case ContainerPartExecMeta:
		if ctr.MetaSnapshot != nil {
			snapshot, _ = ctr.MetaSnapshot.Peek()
		}
	default:
		target, ok := strings.CutPrefix(string(part), containerPartMountPrefix)
		if !ok {
			return ""
		}
		mnt := ctr.mountAt(target)
		if mnt == nil {
			return ""
		}
		if mnt.DirectorySource != nil {
			dir, _ = mnt.DirectorySource.Peek()
		}
		if mnt.FileSource != nil {
			file, _ = mnt.FileSource.Peek()
		}
	}
	if dir != nil && dir.Snapshot != nil {
		snapshot, _ = dir.Snapshot.Peek()
	}
	if file != nil && file.Snapshot != nil {
		snapshot, _ = file.Snapshot.Peek()
	}
	if snapshot != nil {
		return snapshot.SnapshotID()
	}
	return ""
}
