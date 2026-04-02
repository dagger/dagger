package dagql

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagger/dagger/engine"
)

type sharedResultLookupMode uint8

const (
	sharedResultLookupExact sharedResultLookupMode = iota
	sharedResultLookupCanonicalEquivalent
)

func (c *Cache) PersistedSnapshotLinksByResultID(ctx context.Context, resultID uint64) ([]PersistedSnapshotRefLink, error) {
	// Startup/import paths intentionally inspect persisted entries without adding
	// session ownership. These results are retained by persisted edges directly
	// or by dependency closure from another persisted result.
	res, _, _, err := c.sharedResultByResultID(ctx, "", sharedResultID(resultID), sharedResultLookupExact)
	if err != nil {
		return nil, err
	}

	c.egraphMu.RLock()
	links := append([]PersistedSnapshotRefLink(nil), res.snapshotOwnerLinks...)
	c.egraphMu.RUnlock()
	return links, nil
}

func (c *Cache) PersistedResultID(res AnyResult) (uint64, error) {
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

func (c *Cache) sharedResultByResultID(ctx context.Context, sessionID string, resultID sharedResultID, mode sharedResultLookupMode) (*sharedResult, bool, int, error) {
	if c == nil {
		return nil, false, 0, fmt.Errorf("resolve result %d: nil cache", resultID)
	}
	if resultID == 0 {
		return nil, false, 0, fmt.Errorf("resolve result: zero result ID")
	}
	if mode == sharedResultLookupCanonicalEquivalent && sessionID == "" {
		return nil, false, 0, fmt.Errorf("resolve result %d: canonical equivalent lookup requires session ID", resultID)
	}
	if sessionID == "" {
		c.egraphMu.RLock()
		res := c.resultsByID[resultID]
		c.egraphMu.RUnlock()
		if res == nil {
			return nil, false, 0, fmt.Errorf("resolve result %d: missing shared result", resultID)
		}
		return res, false, 0, nil
	}

	c.egraphMu.Lock()
	res := c.resultsByID[resultID]
	if res == nil {
		c.egraphMu.Unlock()
		return nil, false, 0, fmt.Errorf("resolve result %d: missing shared result", resultID)
	}
	if mode == sharedResultLookupCanonicalEquivalent {
		res = c.canonicalEquivalentSharedResultLocked(sessionID, res, time.Now().Unix())
		if res == nil {
			c.egraphMu.Unlock()
			return nil, false, 0, fmt.Errorf("resolve result %d: canonical shared result missing", resultID)
		}
	}

	trackedCount := 0
	alreadyTracked := false
	c.sessionMu.Lock()
	if c.sessionResultIDsBySession == nil {
		c.sessionResultIDsBySession = make(map[string]map[sharedResultID]struct{})
	}
	if c.sessionResultIDsBySession[sessionID] == nil {
		c.sessionResultIDsBySession[sessionID] = make(map[sharedResultID]struct{})
	}
	if _, found := c.sessionResultIDsBySession[sessionID][res.id]; found {
		alreadyTracked = true
	} else {
		c.sessionResultIDsBySession[sessionID][res.id] = struct{}{}
		c.incrementIncomingOwnershipLocked(ctx, res)
	}
	trackedCount = len(c.sessionResultIDsBySession[sessionID])
	c.sessionMu.Unlock()
	c.egraphMu.Unlock()

	return res, alreadyTracked, trackedCount, nil
}

func (c *Cache) loadResultByResultID(ctx context.Context, sessionID string, dag *Server, resultID uint64) (AnyResult, error) {
	mode := sharedResultLookupExact
	if sessionID != "" {
		mode = sharedResultLookupCanonicalEquivalent
	}

	res, alreadyTracked, trackedCount, err := c.sharedResultByResultID(ctx, sessionID, sharedResultID(resultID), mode)
	if err != nil {
		return nil, err
	}

	wrapped := Result[Typed]{
		shared:   res,
		hitCache: true,
	}
	loaded, err := c.ensurePersistedHitValueLoaded(ctx, dag, wrapped)
	if err != nil {
		if sessionID != "" {
			c.egraphMu.Lock()
			c.sessionMu.Lock()
			if resultIDs := c.sessionResultIDsBySession[sessionID]; resultIDs != nil {
				delete(resultIDs, res.id)
				if len(resultIDs) == 0 {
					delete(c.sessionResultIDsBySession, sessionID)
				}
			}
			c.sessionMu.Unlock()
			queue := []*sharedResult(nil)
			var decErr error
			if !alreadyTracked {
				queue, decErr = c.decrementIncomingOwnershipLocked(ctx, res, nil)
			}
			collectReleases, collectErr := c.collectUnownedResultsLocked(context.WithoutCancel(ctx), queue)
			c.egraphMu.Unlock()
			return nil, errors.Join(err, decErr, collectErr, runOnReleaseFuncs(context.WithoutCancel(ctx), collectReleases))
		}
		return nil, err
	}
	if sessionID != "" && c.traceEnabled() {
		c.traceSessionResultTracked(ctx, sessionID, loaded, alreadyTracked, trackedCount)
	}
	return loaded, nil
}

func (c *Cache) LoadResultByResultID(ctx context.Context, sessionID string, dag *Server, resultID uint64) (AnyResult, error) {
	return c.loadResultByResultID(ctx, sessionID, dag, resultID)
}

func (c *Cache) WalkResultCall(ctx context.Context, dag *Server, rootCall *ResultCall, visit func(AnyResult) error) error {
	if dag == nil {
		return fmt.Errorf("walk result call: nil dagql server")
	}
	if rootCall == nil {
		return nil
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return fmt.Errorf("walk result call: current client metadata: %w", err)
	}
	if clientMetadata.SessionID == "" {
		return fmt.Errorf("walk result call: empty session ID")
	}
	sessionID := clientMetadata.SessionID
	seenCalls := map[*ResultCall]struct{}{}
	seenResultIDs := map[uint64]struct{}{}

	var walkLiteral func(*ResultCallLiteral) error
	var walkRef func(*ResultCallRef) error
	var walkCall func(*ResultCall) error

	walkLiteral = func(lit *ResultCallLiteral) error {
		if lit == nil {
			return nil
		}
		switch lit.Kind {
		case ResultCallLiteralKindResultRef:
			return walkRef(lit.ResultRef)
		case ResultCallLiteralKindList:
			for _, item := range lit.ListItems {
				if err := walkLiteral(item); err != nil {
					return err
				}
			}
		case ResultCallLiteralKindObject:
			for _, field := range lit.ObjectFields {
				if field == nil {
					continue
				}
				if err := walkLiteral(field.Value); err != nil {
					return fmt.Errorf("field %q: %w", field.Name, err)
				}
			}
		}
		return nil
	}

	walkRef = func(ref *ResultCallRef) error {
		if ref == nil {
			return nil
		}
		if ref.Call != nil {
			return walkCall(ref.Call)
		}
		if ref.ResultID == 0 {
			return nil
		}
		if _, seen := seenResultIDs[ref.ResultID]; seen {
			return nil
		}
		seenResultIDs[ref.ResultID] = struct{}{}
		res, err := c.loadResultByResultID(ctx, sessionID, dag, ref.ResultID)
		if err != nil {
			return fmt.Errorf("load result %d: %w", ref.ResultID, err)
		}
		if visit != nil {
			if err := visit(res); err != nil {
				return err
			}
		}
		call, err := res.ResultCall()
		if err != nil {
			return fmt.Errorf("result %d call: %w", ref.ResultID, err)
		}
		return walkCall(call)
	}

	walkCall = func(call *ResultCall) error {
		if call == nil {
			return nil
		}
		if _, seen := seenCalls[call]; seen {
			return nil
		}
		seenCalls[call] = struct{}{}

		if call.Module != nil {
			if err := walkRef(call.Module.ResultRef); err != nil {
				return fmt.Errorf("module %q: %w", call.Module.Name, err)
			}
		}
		if err := walkRef(call.Receiver); err != nil {
			return fmt.Errorf("receiver: %w", err)
		}
		for _, arg := range call.Args {
			if arg == nil {
				continue
			}
			if err := walkLiteral(arg.Value); err != nil {
				return fmt.Errorf("arg %q: %w", arg.Name, err)
			}
		}
		for _, input := range call.ImplicitInputs {
			if input == nil {
				continue
			}
			if err := walkLiteral(input.Value); err != nil {
				return fmt.Errorf("implicit input %q: %w", input.Name, err)
			}
		}
		return nil
	}

	return walkCall(rootCall)
}

func (c *Cache) LoadPersistedObjectByResultID(ctx context.Context, dag *Server, resultID uint64) (AnyObjectResult, error) {
	// Startup/import paths intentionally reload persisted objects without adding
	// session ownership. These results are retained by persisted edges directly
	// or by dependency closure from another persisted result.
	res, err := c.loadResultByResultID(ctx, "", dag, resultID)
	if err != nil {
		return nil, err
	}
	obj, ok := res.(AnyObjectResult)
	if !ok {
		return nil, fmt.Errorf("load persisted object by result ID %d: result is %T", resultID, res)
	}
	return obj, nil
}
