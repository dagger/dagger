package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

func persistedDecodeQuery(dag *dagql.Server) (*Query, error) {
	if dag == nil {
		return nil, fmt.Errorf("persisted decode query: nil dagql server")
	}
	root := dag.Root()
	if root == nil {
		return nil, fmt.Errorf("persisted decode query: nil dagql root")
	}
	query, ok := dagql.UnwrapAs[*Query](root)
	if !ok {
		return nil, fmt.Errorf("persisted decode query: root is %T", root.Unwrap())
	}
	return query, nil
}

func encodePersistedCallID(id *call.ID) (string, error) {
	if id == nil {
		return "", fmt.Errorf("encode persisted call ID: nil ID")
	}
	return id.Encode()
}

func encodePersistedObjectRef(cache dagql.PersistedObjectCache, ref any, label string) (uint64, error) {
	if cache == nil {
		return 0, fmt.Errorf("encode persisted %s cache: nil cache", label)
	}
	switch x := ref.(type) {
	case nil:
		return 0, fmt.Errorf("encode persisted %s ref: nil value", label)
	case dagql.AnyResult:
		resultID, err := cache.PersistedResultID(x)
		if err != nil {
			return 0, fmt.Errorf("encode persisted %s ref: %w", label, err)
		}
		return resultID, nil
	default:
		return 0, fmt.Errorf("encode persisted %s ref: unsupported value %T", label, ref)
	}
}

func decodePersistedCallID(raw string) (*call.ID, error) {
	if raw == "" {
		return nil, nil
	}
	var id call.ID
	if err := id.Decode(raw); err != nil {
		return nil, fmt.Errorf("decode persisted call ID: %w", err)
	}
	return &id, nil
}

func loadPersistedResultByResultID(ctx context.Context, dag *dagql.Server, resultID uint64, label string) (dagql.AnyResult, error) {
	if resultID == 0 {
		return nil, nil
	}
	if _, err := persistedDecodeQuery(dag); err != nil {
		return nil, fmt.Errorf("load persisted %s query: %w", label, err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("load persisted %s cache: %w", label, err)
	}
	res, err := cache.LoadResultByResultID(ctx, "", dag, resultID)
	if err != nil {
		return nil, fmt.Errorf("load persisted %s result: %w", label, err)
	}
	return res, nil
}

func loadPersistedObjectResultByResultID[T dagql.Typed](ctx context.Context, dag *dagql.Server, resultID uint64, label string) (dagql.ObjectResult[T], error) {
	if resultID == 0 {
		return dagql.ObjectResult[T]{}, nil
	}
	if _, err := persistedDecodeQuery(dag); err != nil {
		return dagql.ObjectResult[T]{}, fmt.Errorf("load persisted %s query: %w", label, err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return dagql.ObjectResult[T]{}, fmt.Errorf("load persisted %s cache: %w", label, err)
	}
	obj, err := cache.LoadPersistedObjectByResultID(ctx, dag, resultID)
	if err != nil {
		return dagql.ObjectResult[T]{}, fmt.Errorf("load persisted %s object: %w", label, err)
	}
	typed, ok := obj.(dagql.ObjectResult[T])
	if !ok {
		return dagql.ObjectResult[T]{}, fmt.Errorf("load persisted %s: unexpected object result %T", label, obj)
	}
	return typed, nil
}

func loadPersistedSnapshotLinkByResultID(ctx context.Context, dag *dagql.Server, resultID uint64, label, role string) (dagql.PersistedSnapshotRefLink, error) {
	if resultID == 0 {
		return dagql.PersistedSnapshotRefLink{}, fmt.Errorf("load persisted %s snapshot link: zero result ID", label)
	}
	if _, err := persistedDecodeQuery(dag); err != nil {
		return dagql.PersistedSnapshotRefLink{}, fmt.Errorf("load persisted %s snapshot link query: %w", label, err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return dagql.PersistedSnapshotRefLink{}, fmt.Errorf("load persisted %s snapshot link cache: %w", label, err)
	}
	links, err := cache.PersistedSnapshotLinksByResultID(ctx, resultID)
	if err != nil {
		return dagql.PersistedSnapshotRefLink{}, fmt.Errorf("load persisted %s snapshot link: %w", label, err)
	}
	for _, link := range links {
		if link.Role == role {
			return link, nil
		}
	}
	return dagql.PersistedSnapshotRefLink{}, fmt.Errorf("missing persisted %s snapshot link role %q for result %d", label, role, resultID)
}

func loadPersistedSnapshotLinksByResultID(ctx context.Context, dag *dagql.Server, resultID uint64, label string) ([]dagql.PersistedSnapshotRefLink, error) {
	if resultID == 0 {
		return nil, fmt.Errorf("load persisted %s snapshot links: zero result ID", label)
	}
	if _, err := persistedDecodeQuery(dag); err != nil {
		return nil, fmt.Errorf("load persisted %s snapshot links query: %w", label, err)
	}
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("load persisted %s snapshot links cache: %w", label, err)
	}
	links, err := cache.PersistedSnapshotLinksByResultID(ctx, resultID)
	if err != nil {
		return nil, fmt.Errorf("load persisted %s snapshot links: %w", label, err)
	}
	return links, nil
}

func loadPersistedImmutableSnapshotByResultID(ctx context.Context, dag *dagql.Server, resultID uint64, label, role string) (bkcache.ImmutableRef, error) {
	link, err := loadPersistedSnapshotLinkByResultID(ctx, dag, resultID, label, role)
	if err != nil {
		return nil, err
	}
	query, err := persistedDecodeQuery(dag)
	if err != nil {
		return nil, err
	}
	ref, err := query.SnapshotManager().GetBySnapshotID(ctx, link.RefKey, bkcache.NoUpdateLastUsed)
	if err != nil {
		return nil, fmt.Errorf("load persisted immutable snapshot %q: %w", link.RefKey, err)
	}
	return ref, nil
}
