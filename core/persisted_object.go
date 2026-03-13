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
	case dagql.PersistedResultIDHolder:
		resultID := x.PersistedResultID()
		if resultID == 0 {
			return 0, fmt.Errorf("encode persisted %s ref: zero persisted result ID", label)
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

func loadPersistedCallIDByResultID(ctx context.Context, dag *dagql.Server, resultID uint64, label string) (*call.ID, error) {
	if resultID == 0 {
		return nil, nil
	}
	query, err := persistedDecodeQuery(dag)
	if err != nil {
		return nil, fmt.Errorf("load persisted %s query: %w", label, err)
	}
	cache, err := query.Cache(ctx)
	if err != nil {
		return nil, fmt.Errorf("load persisted %s cache: %w", label, err)
	}
	id, err := cache.PersistedCallIDByResultID(ctx, resultID)
	if err != nil {
		return nil, fmt.Errorf("load persisted %s ID: %w", label, err)
	}
	return id, nil
}

func loadPersistedObjectResultByResultID[T dagql.Typed](ctx context.Context, dag *dagql.Server, resultID uint64, label string) (dagql.ObjectResult[T], error) {
	if resultID == 0 {
		return dagql.ObjectResult[T]{}, nil
	}
	query, err := persistedDecodeQuery(dag)
	if err != nil {
		return dagql.ObjectResult[T]{}, fmt.Errorf("load persisted %s query: %w", label, err)
	}
	cache, err := query.Cache(ctx)
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

func loadPersistedSnapshotLink(ctx context.Context, dag *dagql.Server, id *call.ID, role string) (dagql.PersistedSnapshotRefLink, error) {
	if id == nil {
		return dagql.PersistedSnapshotRefLink{}, fmt.Errorf("load persisted snapshot link: nil ID")
	}
	query, err := persistedDecodeQuery(dag)
	if err != nil {
		return dagql.PersistedSnapshotRefLink{}, fmt.Errorf("load persisted snapshot link query: %w", err)
	}
	cache, err := query.Cache(ctx)
	if err != nil {
		return dagql.PersistedSnapshotRefLink{}, fmt.Errorf("load persisted snapshot link cache: %w", err)
	}
	links, err := cache.PersistedSnapshotLinks(ctx, id)
	if err != nil {
		return dagql.PersistedSnapshotRefLink{}, err
	}
	for _, link := range links {
		if link.Role == role {
			return link, nil
		}
	}
	return dagql.PersistedSnapshotRefLink{}, fmt.Errorf("missing persisted snapshot link role %q for %s", role, id.Digest())
}

func loadPersistedSnapshotLinksForID(ctx context.Context, dag *dagql.Server, id *call.ID) ([]dagql.PersistedSnapshotRefLink, error) {
	if id == nil {
		return nil, fmt.Errorf("load persisted snapshot links: nil ID")
	}
	query, err := persistedDecodeQuery(dag)
	if err != nil {
		return nil, fmt.Errorf("load persisted snapshot links query: %w", err)
	}
	cache, err := query.Cache(ctx)
	if err != nil {
		return nil, fmt.Errorf("load persisted snapshot links cache: %w", err)
	}
	return cache.PersistedSnapshotLinks(ctx, id)
}

func loadPersistedImmutableSnapshot(ctx context.Context, dag *dagql.Server, id *call.ID, role string) (bkcache.ImmutableRef, dagql.PersistedSnapshotRefLink, error) {
	link, err := loadPersistedSnapshotLink(ctx, dag, id, role)
	if err != nil {
		return nil, dagql.PersistedSnapshotRefLink{}, err
	}
	query, err := persistedDecodeQuery(dag)
	if err != nil {
		return nil, dagql.PersistedSnapshotRefLink{}, err
	}
	ref, err := query.BuildkitCache().GetBySnapshotID(ctx, link.RefKey, bkcache.NoUpdateLastUsed)
	if err != nil {
		return nil, dagql.PersistedSnapshotRefLink{}, fmt.Errorf("load persisted immutable snapshot %q: %w", link.RefKey, err)
	}
	return ref, link, nil
}

func loadPersistedMutableSnapshot(ctx context.Context, dag *dagql.Server, id *call.ID, role string) (bkcache.MutableRef, dagql.PersistedSnapshotRefLink, error) {
	link, err := loadPersistedSnapshotLink(ctx, dag, id, role)
	if err != nil {
		return nil, dagql.PersistedSnapshotRefLink{}, err
	}
	query, err := persistedDecodeQuery(dag)
	if err != nil {
		return nil, dagql.PersistedSnapshotRefLink{}, err
	}
	ref, err := query.BuildkitCache().GetMutableBySnapshotID(ctx, link.RefKey, bkcache.NoUpdateLastUsed)
	if err != nil {
		return nil, dagql.PersistedSnapshotRefLink{}, fmt.Errorf("load persisted mutable snapshot %q: %w", link.RefKey, err)
	}
	return ref, link, nil
}

func retainImmutableRefChain(ctx context.Context, ref bkcache.ImmutableRef) error {
	if ref == nil {
		return nil
	}
	if err := ref.SetCachePolicyRetain(); err != nil {
		return err
	}
	chain := ref.LayerChain()
	defer chain.Release(context.WithoutCancel(ctx))
	for _, layer := range chain {
		if layer == nil {
			continue
		}
		if err := layer.SetCachePolicyRetain(); err != nil {
			return err
		}
	}
	return nil
}
