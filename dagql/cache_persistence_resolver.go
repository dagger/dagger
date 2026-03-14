package dagql

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql/call"
)

func (c *cache) LoadPersistedResult(ctx context.Context, dag *Server, id *call.ID) (AnyResult, error) {
	if c == nil {
		return nil, fmt.Errorf("load persisted result: nil cache")
	}
	if id == nil {
		return nil, fmt.Errorf("load persisted result: nil ID")
	}

	res, err := c.persistedSharedResultByID(ctx, id)
	if err != nil {
		return nil, err
	}

	wrapped, err := c.persistedResultForShared(ctx, res, id)
	if err != nil {
		return nil, err
	}

	return c.ensurePersistedHitValueLoaded(ctx, dag, wrapped)
}

func (c *cache) LoadPersistedObject(ctx context.Context, dag *Server, id *call.ID) (AnyObjectResult, error) {
	res, err := c.LoadPersistedResult(ctx, dag, id)
	if err != nil {
		return nil, err
	}
	obj, ok := res.(AnyObjectResult)
	if ok {
		return obj, nil
	}
	if dag == nil || res.Type() == nil || res.Type().Elem != nil {
		return nil, fmt.Errorf("load persisted object %q: result is %T", id.Digest(), res)
	}
	objType, ok := dag.ObjectType(res.Type().Name())
	if !ok {
		return nil, fmt.Errorf("load persisted object %q: result is %T", id.Digest(), res)
	}
	obj, err = objType.New(res)
	if err != nil {
		return nil, fmt.Errorf("load persisted object %q: wrap result as object: %w", id.Digest(), err)
	}
	return obj, nil
}

func (c *cache) PersistedSnapshotLinks(ctx context.Context, id *call.ID) ([]PersistedSnapshotRefLink, error) {
	if c == nil {
		return nil, fmt.Errorf("persisted snapshot links: nil cache")
	}
	if id == nil {
		return nil, fmt.Errorf("persisted snapshot links: nil ID")
	}

	res, err := c.persistedSharedResultByID(ctx, id)
	if err != nil {
		return nil, err
	}

	c.egraphMu.RLock()
	links := append([]PersistedSnapshotRefLink(nil), res.persistedSnapshotLinks...)
	c.egraphMu.RUnlock()
	return links, nil
}

func (c *cache) PersistedSnapshotLinksByResultID(_ context.Context, resultID uint64) ([]PersistedSnapshotRefLink, error) {
	res, err := c.persistedSharedResultByResultID(sharedResultID(resultID))
	if err != nil {
		return nil, err
	}

	c.egraphMu.RLock()
	links := append([]PersistedSnapshotRefLink(nil), res.persistedSnapshotLinks...)
	c.egraphMu.RUnlock()
	return links, nil
}

func (c *SessionCache) basePersistedCache() (*cache, error) {
	if c == nil {
		return nil, fmt.Errorf("persisted session cache: nil session cache")
	}
	base, ok := c.cache.(*cache)
	if !ok {
		return nil, fmt.Errorf("persisted session cache: unsupported base cache %T", c.cache)
	}
	return base, nil
}

func (c *SessionCache) LoadPersistedResult(ctx context.Context, dag *Server, id *call.ID) (AnyResult, error) {
	base, err := c.basePersistedCache()
	if err != nil {
		return nil, err
	}
	return base.LoadPersistedResult(ctx, dag, id)
}

func (c *SessionCache) LoadPersistedObject(ctx context.Context, dag *Server, id *call.ID) (AnyObjectResult, error) {
	base, err := c.basePersistedCache()
	if err != nil {
		return nil, err
	}
	return base.LoadPersistedObject(ctx, dag, id)
}

func (c *SessionCache) PersistedSnapshotLinks(ctx context.Context, id *call.ID) ([]PersistedSnapshotRefLink, error) {
	base, err := c.basePersistedCache()
	if err != nil {
		return nil, err
	}
	return base.PersistedSnapshotLinks(ctx, id)
}

func (c *SessionCache) PersistedSnapshotLinksByResultID(ctx context.Context, resultID uint64) ([]PersistedSnapshotRefLink, error) {
	base, err := c.basePersistedCache()
	if err != nil {
		return nil, err
	}
	return base.PersistedSnapshotLinksByResultID(ctx, resultID)
}

func (c *cache) PersistedResultID(res AnyResult) (uint64, error) {
	if res == nil {
		return 0, fmt.Errorf("persisted result ID: nil result")
	}
	if c == nil {
		return 0, fmt.Errorf("persisted result ID for %T: nil cache", res)
	}
	shared := res.cacheSharedResult()
	if shared == nil {
		return 0, fmt.Errorf("persisted result ID for %T: result is not cache-backed", res)
	}
	if shared.id == 0 {
		return 0, fmt.Errorf("persisted result ID for %T: zero shared result ID", res)
	}
	return uint64(shared.id), nil
}

func (c *SessionCache) PersistedResultID(res AnyResult) (uint64, error) {
	base, err := c.basePersistedCache()
	if err != nil {
		return 0, err
	}
	return base.PersistedResultID(res)
}

func (c *cache) persistedSharedResultByID(ctx context.Context, id *call.ID) (*sharedResult, error) {
	if c == nil {
		return nil, fmt.Errorf("resolve persisted result %q: nil cache", id.Digest())
	}
	if id == nil {
		return nil, fmt.Errorf("resolve persisted result: nil ID")
	}

	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()

	res, err := c.resolveSharedResultForInputIDLocked(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("resolve persisted result %q: %w", id.Digest(), err)
	}
	return res, nil
}

func (c *cache) persistedSharedResultByResultID(resultID sharedResultID) (*sharedResult, error) {
	if c == nil {
		return nil, fmt.Errorf("resolve persisted result %d: nil cache", resultID)
	}
	if resultID == 0 {
		return nil, fmt.Errorf("resolve persisted result: zero result ID")
	}

	c.egraphMu.RLock()
	res := c.resultsByID[resultID]
	c.egraphMu.RUnlock()

	if res == nil {
		return nil, fmt.Errorf("resolve persisted result %d: missing shared result", resultID)
	}
	return res, nil
}

func (c *cache) PersistedCallIDByResultID(ctx context.Context, resultID uint64) (*call.ID, error) {
	_, err := c.persistedSharedResultByResultID(sharedResultID(resultID))
	if err != nil {
		return nil, err
	}
	return c.persistedCallIDByResultID(ctx, sharedResultID(resultID))
}

func (c *SessionCache) PersistedCallIDByResultID(ctx context.Context, resultID uint64) (*call.ID, error) {
	base, err := c.basePersistedCache()
	if err != nil {
		return nil, err
	}
	return base.PersistedCallIDByResultID(ctx, resultID)
}

func (c *cache) LoadPersistedResultByResultID(ctx context.Context, dag *Server, resultID uint64) (AnyResult, error) {
	res, err := c.persistedSharedResultByResultID(sharedResultID(resultID))
	if err != nil {
		return nil, err
	}
	id, err := c.persistedCallIDByResultID(ctx, sharedResultID(resultID))
	if err != nil {
		return nil, err
	}
	wrapped, err := c.persistedResultForShared(ctx, res, id)
	if err != nil {
		return nil, err
	}
	return c.ensurePersistedHitValueLoaded(ctx, dag, wrapped)
}

func (c *SessionCache) LoadPersistedResultByResultID(ctx context.Context, dag *Server, resultID uint64) (AnyResult, error) {
	base, err := c.basePersistedCache()
	if err != nil {
		return nil, err
	}
	return base.LoadPersistedResultByResultID(ctx, dag, resultID)
}

func (c *cache) LoadPersistedObjectByResultID(ctx context.Context, dag *Server, resultID uint64) (AnyObjectResult, error) {
	res, err := c.LoadPersistedResultByResultID(ctx, dag, resultID)
	if err != nil {
		return nil, err
	}
	obj, ok := res.(AnyObjectResult)
	if ok {
		return obj, nil
	}
	id := res.ID()
	if dag == nil || id == nil || res.Type() == nil || res.Type().Elem != nil {
		return nil, fmt.Errorf("load persisted object by result ID %d: result is %T", resultID, res)
	}
	objType, ok := dag.ObjectType(res.Type().Name())
	if !ok {
		return nil, fmt.Errorf("load persisted object by result ID %d: result is %T", resultID, res)
	}
	obj, err = objType.New(res)
	if err != nil {
		return nil, fmt.Errorf("load persisted object by result ID %d: wrap result as object: %w", resultID, err)
	}
	return obj, nil
}

func (c *SessionCache) LoadPersistedObjectByResultID(ctx context.Context, dag *Server, resultID uint64) (AnyObjectResult, error) {
	base, err := c.basePersistedCache()
	if err != nil {
		return nil, err
	}
	return base.LoadPersistedObjectByResultID(ctx, dag, resultID)
}

func (c *cache) persistedResultForShared(ctx context.Context, res *sharedResult, requestedID *call.ID) (AnyResult, error) {
	if res == nil {
		return nil, fmt.Errorf("wrap persisted shared result: nil result")
	}
	if requestedID == nil {
		return nil, fmt.Errorf("wrap persisted shared result: nil requested ID")
	}

	c.egraphMu.Lock()
	if err := c.teachResultIdentityLocked(ctx, res, requestedID); err != nil {
		c.egraphMu.Unlock()
		return nil, fmt.Errorf("teach persisted shared result identity %q: %w", requestedID.Digest(), err)
	}
	retID := requestedID
	for outputEqID := range c.outputEqClassesForResultLocked(res.id) {
		// NOTE: if multiple content-labeled digests end up in one eq class, we
		// intentionally tolerate that for now and just use the first one we
		// encounter.
		for extra := range c.eqClassExtraDigests[outputEqID] {
			if extra.Label != call.ExtraDigestLabelContent || extra.Digest == "" {
				continue
			}
			retID = retID.With(call.WithExtraDigest(extra))
			break
		}
		if retID.ContentDigest() != "" {
			break
		}
	}
	outputEffectIDs := append([]string(nil), res.outputEffectIDs...)
	objType := res.objType
	c.egraphMu.Unlock()

	retID = retID.AppendEffectIDs(outputEffectIDs...)
	retRes := Result[Typed]{
		shared:   res,
		id:       retID,
		hitCache: true,
	}
	if objType == nil {
		return retRes, nil
	}
	objRes, err := objType.New(retRes)
	if err != nil {
		return nil, fmt.Errorf("wrap persisted shared result %q: %w", requestedID.Digest(), err)
	}
	return objRes, nil
}
