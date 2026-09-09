package core

import "sync/atomic"

// These counters are process-local and only allocated for opted-in restores.
type storedSnapshotDiagnostics struct {
	storedOpen atomic.Uint64
}

func newStoredSnapshotDiagnostics() *storedSnapshotDiagnostics {
	if !containerPartDiagnosticsEnabled {
		return nil
	}
	return new(storedSnapshotDiagnostics)
}

func (stats *storedSnapshotDiagnostics) opened() {
	if stats != nil {
		stats.storedOpen.Add(1)
	}
}

type storedSnapshotDebugValue struct {
	StoredSnapshotID string            `json:"storedSnapshotID"`
	OpenSnapshotID   string            `json:"openSnapshotID"`
	Counts           map[string]uint64 `json:"counts"`
}

func snapshotDebugValue(stored *storedSnapshot, stats *storedSnapshotDiagnostics, openID string) storedSnapshotDebugValue {
	value := storedSnapshotDebugValue{OpenSnapshotID: openID, Counts: map[string]uint64{"storedOpen": 0}}
	if stored != nil {
		value.StoredSnapshotID = stored.SnapshotID
	}
	if stats != nil {
		value.Counts["storedOpen"] = stats.storedOpen.Load()
	}
	return value
}

func (dir *Directory) CacheDebugValue() any {
	if !containerPartDiagnosticsEnabled || dir == nil {
		return nil
	}
	var openID string
	if dir.Snapshot != nil {
		if snapshot, ok := dir.Snapshot.Peek(); ok && snapshot != nil {
			openID = snapshot.SnapshotID()
		}
	}
	return snapshotDebugValue(dir.stored, dir.storedDiagnostics, openID)
}

func (file *File) CacheDebugValue() any {
	if !containerPartDiagnosticsEnabled || file == nil {
		return nil
	}
	var openID string
	if file.Snapshot != nil {
		if snapshot, ok := file.Snapshot.Peek(); ok && snapshot != nil {
			openID = snapshot.SnapshotID()
		}
	}
	return snapshotDebugValue(file.stored, file.storedDiagnostics, openID)
}
