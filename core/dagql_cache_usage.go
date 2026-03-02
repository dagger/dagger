package core

import (
	"context"
	"strconv"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
)

const buildkitSnapshotSizeMetadataKey = "snapshot.size"

func (dir *Directory) DagqlCacheUsage(ctx context.Context) (dagql.CacheValueUsage, bool, error) {
	if dir == nil || dir.Snapshot == nil {
		return dagql.CacheValueUsage{}, false, nil
	}
	return cacheRefUsage(ctx, dir.Snapshot)
}

func (file *File) DagqlCacheUsage(ctx context.Context) (dagql.CacheValueUsage, bool, error) {
	if file == nil || file.Snapshot == nil {
		return dagql.CacheValueUsage{}, false, nil
	}
	return cacheRefUsage(ctx, file.Snapshot)
}

func (container *Container) DagqlCacheUsage(ctx context.Context) (dagql.CacheValueUsage, bool, error) {
	if container == nil || container.FS == nil || container.FS.Self() == nil {
		return dagql.CacheValueUsage{}, false, nil
	}
	if container.FS.Self().Snapshot == nil {
		return dagql.CacheValueUsage{}, false, nil
	}
	return cacheRefUsage(ctx, container.FS.Self().Snapshot)
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
		usage, hasUsage, err := cacheRefUsage(ctx, ref)
		_ = ref.Release(ctx)
		if err != nil {
			return dagql.CacheValueUsage{}, false, err
		}
		if !hasUsage {
			continue
		}
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

func cacheRefUsage(ctx context.Context, ref bkcache.ImmutableRef) (dagql.CacheValueUsage, bool, error) {
	if ref == nil {
		return dagql.CacheValueUsage{}, false, nil
	}

	usage := dagql.CacheValueUsage{
		RecordType:  string(ref.GetRecordType()),
		Description: ref.GetDescription(),
	}
	if metadataSize := ref.GetString(buildkitSnapshotSizeMetadataKey); metadataSize != "" {
		if parsedSize, err := strconv.ParseInt(metadataSize, 10, 64); err == nil && parsedSize > 0 {
			usage.SizeBytes = parsedSize
		}
	}

	if usage.SizeBytes <= 0 {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return usage, true, nil
		}
		diskUsage, err := query.BuildkitCache().DiskUsage(ctx, bkclient.DiskUsageInfo{
			Filter: []string{"id==" + ref.ID()},
		})
		if err != nil {
			return usage, true, err
		}
		for _, entry := range diskUsage {
			if entry == nil {
				continue
			}
			if entry.Size > usage.SizeBytes {
				usage.SizeBytes = entry.Size
			}
			if usage.RecordType == "" && entry.RecordType != "" {
				usage.RecordType = string(entry.RecordType)
			}
			if usage.Description == "" && entry.Description != "" {
				usage.Description = entry.Description
			}
		}
	}

	return usage, true, nil
}
