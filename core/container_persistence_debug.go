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

func (container *Container) recordPartDiagnostic(event string, part dagql.PartKey) {
	if !containerPartDiagnosticsEnabled {
		return
	}
	container.lazyOpMu.Lock()
	if container.partDiagnostics == nil {
		container.partDiagnostics = &containerPartDiagnostics{counts: map[string]uint64{}}
	}
	stats := container.partDiagnostics
	container.lazyOpMu.Unlock()
	if part != "" {
		event += ":" + string(part)
	}
	stats.mu.Lock()
	stats.counts[event]++
	stats.mu.Unlock()
}

func (container *Container) directoryCommitObserver() func() {
	if !containerPartDiagnosticsEnabled {
		return nil
	}
	return func() { container.recordPartDiagnostic("directoryCommit", "") }
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
func (container *Container) CacheDebugValue() any {
	if !containerPartDiagnosticsEnabled || container == nil {
		return nil
	}
	container.lazyOpMu.Lock()
	lazy, stats := container.Lazy, container.partDiagnostics
	container.lazyOpMu.Unlock()
	counts := map[string]uint64{}
	if stats != nil {
		stats.mu.Lock()
		counts = maps.Clone(stats.counts)
		stats.mu.Unlock()
	}
	ctx := context.Background()
	parts := []dagql.PartKey{ContainerPartMetadata}
	if container.containerPartComputed(ctx, lazy, ContainerPartMetadata) {
		parts = append(parts, containerSnapshotParts(container)...)
	}
	values := make(map[dagql.PartKey]containerPartDebugValue, len(parts))
	for _, part := range parts {
		stored := container.storedParts[part]
		value := containerPartDebugValue{
			Computed: container.containerPartComputed(ctx, lazy, part),
			Consumed: lazy == nil, StoredKind: stored.Kind, StoredSnapshotID: stored.SnapshotID,
		}
		if op, ok := lazy.(LazyContainerParts); ok {
			if groups, err := op.ContainerLazyGroups(ctx, container, []dagql.PartKey{part}); err == nil && len(groups) == 1 {
				value.Group = groups[0]
				value.Consumed = op.ContainerLazyState().GroupConsumed(groups[0])
			}
		}
		if part != ContainerPartMetadata {
			value.OpenSnapshotID = container.openContainerSnapshotID(part)
		}
		values[part] = value
	}
	return struct {
		Parts  map[dagql.PartKey]containerPartDebugValue `json:"parts"`
		Counts map[string]uint64                         `json:"counts"`
	}{values, counts}
}

func (container *Container) openContainerSnapshotID(part dagql.PartKey) string {
	var snapshot bkcache.ImmutableRef
	var dir *Directory
	var file *File
	switch part {
	case ContainerPartFS:
		if container.FS != nil {
			dir, _ = container.FS.Peek()
		}
	case ContainerPartExecMeta:
		if container.MetaSnapshot != nil {
			snapshot, _ = container.MetaSnapshot.Peek()
		}
	default:
		target, ok := strings.CutPrefix(string(part), containerPartMountPrefix)
		if !ok {
			return ""
		}
		mnt := container.mountAt(target)
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
