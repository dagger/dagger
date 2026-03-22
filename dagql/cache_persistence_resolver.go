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

func (c *cache) loadResultByResultID(ctx context.Context, dag *Server, resultID uint64) (AnyResult, error) {
	res, err := c.sharedResultByResultID(sharedResultID(resultID))
	if err != nil {
		return nil, err
	}
	wrapped, err := c.persistedResultForShared(ctx, res)
	if err != nil {
		return nil, err
	}
	loaded, err := c.ensurePersistedHitValueLoaded(ctx, dag, wrapped)
	if err != nil {
		return nil, err
	}
	return loaded, nil
}

func (c *cache) LoadResultByResultID(ctx context.Context, sessionID string, dag *Server, resultID uint64) (AnyResult, error) {
	loaded, err := c.loadResultByResultID(ctx, dag, resultID)
	if err != nil {
		return nil, err
	}
	c.trackSessionResult(ctx, sessionID, loaded, true)
	return loaded, nil
}

func (c *cache) WalkResultCall(ctx context.Context, dag *Server, rootCall *ResultCall, visit func(AnyResult) error) error {
	if dag == nil {
		return fmt.Errorf("walk result call: nil dagql server")
	}
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
		res, err := c.loadResultByResultID(ctx, dag, ref.ResultID)
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

func (c *cache) LoadPersistedObjectByResultID(ctx context.Context, dag *Server, resultID uint64) (AnyObjectResult, error) {
	res, err := c.loadResultByResultID(ctx, dag, resultID)
	if err != nil {
		return nil, err
	}
	obj, ok := res.(AnyObjectResult)
	if !ok {
		return nil, fmt.Errorf("load persisted object by result ID %d: result is %T", resultID, res)
	}
	return obj, nil
}

func (c *cache) persistedResultForShared(ctx context.Context, res *sharedResult) (AnyResult, error) {
	if res == nil {
		return nil, fmt.Errorf("wrap persisted shared result: nil result")
	}
	requestedFrame := c.resultCallByResultID(res.id)
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
	c.egraphMu.Unlock()
	return Result[Typed]{
		shared:   res,
		hitCache: true,
	}, nil
}
