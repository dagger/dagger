package dagql

import (
	"context"
	"fmt"

	"github.com/opencontainers/go-digest"
)

func (c *cache) PersistedSnapshotLinksByResultID(_ context.Context, resultID uint64) ([]PersistedSnapshotRefLink, error) {
	res, err := c.sharedResultByResultID(sharedResultID(resultID))
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

func (c *cache) sharedResultByResultID(resultID sharedResultID) (*sharedResult, error) {
	if c == nil {
		return nil, fmt.Errorf("resolve result %d: nil cache", resultID)
	}
	if resultID == 0 {
		return nil, fmt.Errorf("resolve result: zero result ID")
	}

	c.egraphMu.RLock()
	res := c.resultsByID[resultID]
	c.egraphMu.RUnlock()

	if res == nil {
		return nil, fmt.Errorf("resolve result %d: missing shared result", resultID)
	}
	return res, nil
}

func (c *cache) LoadResultByResultID(ctx context.Context, dag *Server, resultID uint64) (AnyResult, error) {
	res, err := c.sharedResultByResultID(sharedResultID(resultID))
	if err != nil {
		return nil, err
	}
	wrapped, err := c.persistedResultForShared(ctx, res)
	if err != nil {
		return nil, err
	}
	return c.ensurePersistedHitValueLoaded(ctx, dag, wrapped)
}

func (c *SessionCache) LoadResultByResultID(ctx context.Context, dag *Server, resultID uint64) (AnyResult, error) {
	base, err := c.basePersistedCache()
	if err != nil {
		return nil, err
	}
	return base.LoadResultByResultID(ctx, dag, resultID)
}

func (c *cache) LoadPersistedObjectByResultID(ctx context.Context, dag *Server, resultID uint64) (AnyObjectResult, error) {
	res, err := c.LoadResultByResultID(ctx, dag, resultID)
	if err != nil {
		return nil, err
	}
	obj, ok := res.(AnyObjectResult)
	if ok {
		return obj, nil
	}
	if dag == nil || res.Type() == nil || res.Type().Elem != nil {
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

func (c *cache) persistedResultForShared(ctx context.Context, res *sharedResult) (AnyResult, error) {
	if res == nil {
		return nil, fmt.Errorf("wrap persisted shared result: nil result")
	}
	requestedFrame := c.resultCallSnapshot(res.id)
	if requestedFrame == nil {
		return nil, fmt.Errorf("derive persisted requested frame for result %d: missing result call frame", res.id)
	}
	requestDigest, err := requestedFrame.RecipeDigest()
	if err != nil {
		return nil, fmt.Errorf("derive persisted requested digest for result %d: %w", res.id, err)
	}
	requestSelf, requestInputRefs, err := requestedFrame.SelfDigestAndInputRefs()
	if err != nil {
		return nil, fmt.Errorf("derive persisted requested term digests for result %d: %w", res.id, err)
	}
	requestInputs := make([]digest.Digest, 0, len(requestInputRefs))
	for _, ref := range requestInputRefs {
		dig, err := ref.InputDigest()
		if err != nil {
			return nil, fmt.Errorf("derive persisted requested term input digest for result %d: %w", res.id, err)
		}
		requestInputs = append(requestInputs, dig)
	}

	c.egraphMu.Lock()
	if err := c.teachResultIdentityLocked(ctx, res, requestedFrame, requestDigest, requestSelf, requestInputs, requestInputRefs); err != nil {
		c.egraphMu.Unlock()
		return nil, fmt.Errorf("teach persisted shared result identity for result %d: %w", res.id, err)
	}
	objType := res.objType
	c.egraphMu.Unlock()

	retRes := Result[Typed]{
		shared:   res,
		hitCache: true,
	}
	if objType == nil {
		return retRes, nil
	}
	objRes, err := objType.New(retRes)
	if err != nil {
		return nil, fmt.Errorf("wrap persisted shared result %d: %w", res.id, err)
	}
	return objRes, nil
}
