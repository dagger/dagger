package dagql

import (
	"context"
	"fmt"
	"time"
)

type sharedResultLookupMode uint8

const (
	sharedResultLookupExact sharedResultLookupMode = iota
	sharedResultLookupCanonicalEquivalent
	// sharedResultLookupCanonicalEquivalentGated additionally refuses to
	// serve a result whose required session resources the session has not
	// bound. Value loads use it: without the check, an exact-ID load served
	// the result whenever no canonical candidate passed the session filter,
	// bypassing the gating that request and digest lookups enforce.
	sharedResultLookupCanonicalEquivalentGated
)

func (c *Cache) PersistedSnapshotLinksByResultID(ctx context.Context, resultID uint64) ([]PersistedSnapshotRefLink, error) {
	// Startup/import paths intentionally inspect persisted entries without adding
	// session ownership. These results are retained by persisted edges directly
	// or by dependency closure from another persisted result.
	res, _, _, err := c.sharedResultByResultID(ctx, "", sharedResultID(resultID), sharedResultLookupExact)
	if err != nil {
		return nil, err
	}

	return res.loadSnapshotOwnerLinks(), nil
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
	if mode != sharedResultLookupExact && sessionID == "" {
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
	if mode != sharedResultLookupExact {
		// Require clean attachment, as publication adoption does: without it
		// the canonicalization can redirect an ID load onto a sibling whose
		// attachment is still open or has failed, and the load then inherits
		// that sibling's barrier wait or attachment error even though the
		// exact result it asked for is settled and healthy.
		res = c.canonicalEquivalentSharedResultLocked(sessionID, res, time.Now().Unix(), true)
	}
	if mode == sharedResultLookupCanonicalEquivalentGated &&
		!c.sessionSatisfiesResourceRequirementsLocked(sessionID, res) {
		c.egraphMu.Unlock()
		return nil, false, 0, fmt.Errorf("resolve result %d: session %q has not bound the session resources this result requires", resultID, sessionID)
	}

	alreadyTracked, trackedCount, err := c.acquireSessionResultLocked(ctx, sessionID, res)
	c.egraphMu.Unlock()
	if err != nil {
		return nil, false, 0, err
	}

	return res, alreadyTracked, trackedCount, nil
}

func (c *Cache) loadResultByResultID(ctx context.Context, sessionID string, dag *Server, resultID uint64) (AnyResult, error) {
	mode := sharedResultLookupExact
	if sessionID != "" {
		mode = sharedResultLookupCanonicalEquivalentGated
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
		return nil, err
	}
	if sessionID != "" && !c.sessionStillSatisfiesResourceRequirements(sessionID, res) {
		// Attachment grew the required set after the gated pre-check in
		// sharedResultByResultID; refuse rather than serve. The session
		// edge recorded there stays until session release.
		return nil, fmt.Errorf("resolve result %d: session %q has not bound the session resources this result requires", resultID, sessionID)
	}
	if sessionID != "" && c.traceEnabled() {
		c.traceSessionResultTracked(ctx, sessionID, loaded, alreadyTracked, trackedCount)
	}
	return loaded, nil
}

func (c *Cache) LoadResultByResultID(ctx context.Context, sessionID string, dag *Server, resultID uint64) (AnyResult, error) {
	op, err := c.beginSessionOperation(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load result %d: %w", resultID, err)
	}
	res, loadErr := c.loadResultByResultID(ctx, sessionID, dag, resultID)
	if op.finish(loadErr == nil && res != nil) {
		return nil, fmt.Errorf("load result %d: %w: %q", resultID, ErrCacheSessionReleased, sessionID)
	}
	return res, loadErr
}

func (c *Cache) ResultCallByResultID(ctx context.Context, sessionID string, resultID uint64) (*ResultCall, error) {
	op, err := c.beginSessionOperation(sessionID)
	if err != nil {
		return nil, fmt.Errorf("resolve result %d call frame: %w", resultID, err)
	}
	frame, callErr := c.loadResultCallByResultID(ctx, sessionID, resultID)
	if op.finish(callErr == nil && frame != nil) {
		return nil, fmt.Errorf("resolve result %d call frame: %w: %q", resultID, ErrCacheSessionReleased, sessionID)
	}
	return frame, callErr
}

func (c *Cache) loadResultCallByResultID(ctx context.Context, sessionID string, resultID uint64) (*ResultCall, error) {
	mode := sharedResultLookupExact
	if sessionID != "" {
		mode = sharedResultLookupCanonicalEquivalent
	}

	shared, _, _, err := c.sharedResultByResultID(ctx, sessionID, sharedResultID(resultID), mode)
	if err != nil {
		return nil, err
	}
	frame := shared.loadResultCall()
	if frame == nil {
		return nil, fmt.Errorf("resolve result %d call frame: missing result call frame", resultID)
	}
	return frame.clone(), nil
}

func (c *Cache) resultCallRefByResultID(ctx context.Context, sessionID string, resultID uint64) (*ResultCallRef, error) {
	op, err := c.beginSessionOperation(sessionID)
	if err != nil {
		return nil, fmt.Errorf("resolve result %d call ref: %w", resultID, err)
	}
	shared, _, _, lookupErr := c.sharedResultByResultID(
		ctx,
		sessionID,
		sharedResultID(resultID),
		sharedResultLookupCanonicalEquivalent,
	)
	var ref *ResultCallRef
	if lookupErr == nil {
		frame := shared.loadResultCall()
		if frame == nil {
			lookupErr = fmt.Errorf("resolve result %d call ref: missing result call frame", resultID)
		} else if frame.Type == nil || frame.Type.NamedType != "Query" {
			ref = &ResultCallRef{ResultID: uint64(shared.id), shared: shared}
		}
	}
	if op.finish(lookupErr == nil && ref != nil) {
		return nil, fmt.Errorf("resolve result %d call ref: %w: %q", resultID, ErrCacheSessionReleased, sessionID)
	}
	if lookupErr != nil {
		return nil, lookupErr
	}
	return ref, nil
}

func (c *Cache) WalkResultCall(rootCall *ResultCall, visit func(*ResultCallRef, *ResultCall) error) error {
	if rootCall == nil {
		return nil
	}
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
		frame := ref.Call
		if frame == nil {
			if ref.ResultID == 0 {
				return nil
			}
			if _, seen := seenResultIDs[ref.ResultID]; seen {
				return nil
			}
			seenResultIDs[ref.ResultID] = struct{}{}
			frame = c.resultCallByResultID(sharedResultID(ref.ResultID))
			if frame == nil {
				return fmt.Errorf("missing result call frame for result %d", ref.ResultID)
			}
		}
		if visit != nil {
			if err := visit(ref, frame); err != nil {
				return err
			}
		}
		return walkCall(frame)
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
