package core

import (
	"context"
	"strconv"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
)

const buildkitSnapshotSizeMetadataKey = "snapshot.size"

func (dir *Directory) DagqlCacheUsage(ctx context.Context) (dagql.CacheValueUsage, bool, error) {
	if dir == nil || dir.Snapshot == nil {
		return dagql.CacheValueUsage{}, false, nil
	}
	return cacheRefUsage(dir.Snapshot), true, nil
}

func (file *File) DagqlCacheUsage(ctx context.Context) (dagql.CacheValueUsage, bool, error) {
	if file == nil || file.Snapshot == nil {
		return dagql.CacheValueUsage{}, false, nil
	}
	return cacheRefUsage(file.Snapshot), true, nil
}

func (container *Container) DagqlCacheUsage(ctx context.Context) (dagql.CacheValueUsage, bool, error) {
	if container == nil || container.FS == nil || container.FS.Self() == nil {
		return dagql.CacheValueUsage{}, false, nil
	}
	if container.FS.Self().Snapshot == nil {
		return dagql.CacheValueUsage{}, false, nil
	}
	return cacheRefUsage(container.FS.Self().Snapshot), true, nil
}

func (cache *CacheVolume) DagqlCacheUsage(ctx context.Context) (dagql.CacheValueUsage, bool, error) {
	cache.mu.Lock()
	refs := make([]bkcache.ImmutableRef, 0, len(cache.snapshots))
	for _, ref := range cache.snapshots {
		if ref == nil {
			continue
		}
		refs = append(refs, ref.Clone())
	}
	cache.mu.Unlock()

	if len(refs) == 0 {
		return dagql.CacheValueUsage{}, false, nil
	}

	var (
		totalSize   int64
		recordType  string
		description string
	)
	for _, ref := range refs {
		usage := cacheRefUsage(ref)
		_ = ref.Release(ctx)
		totalSize += usage.SizeBytes
		if recordType == "" && usage.RecordType != "" {
			recordType = usage.RecordType
		}
		if description == "" && usage.Description != "" {
			description = usage.Description
		}
	}

	return dagql.CacheValueUsage{
		RecordType:  recordType,
		Description: description,
		SizeBytes:   totalSize,
	}, true, nil
}

func cacheRefUsage(ref bkcache.ImmutableRef) dagql.CacheValueUsage {
	usage := dagql.CacheValueUsage{
		RecordType:  string(ref.GetRecordType()),
		Description: ref.GetDescription(),
	}
	if metadataSize := ref.GetString(buildkitSnapshotSizeMetadataKey); metadataSize != "" {
		if parsedSize, err := strconv.ParseInt(metadataSize, 10, 64); err == nil && parsedSize > 0 {
			usage.SizeBytes = parsedSize
		}
	}
	return usage
}
