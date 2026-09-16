package dagql

import (
	"context"
	"fmt"
	"time"
)

// SchemaModuleCandidate keeps the installed operational Module separate from
// its implementation-scoped comparison row. Only schema recovery uses it.
type SchemaModuleCandidate struct {
	ModuleResultID uint64
	ScopedResultID uint64
}

func (c *Cache) LoadResultByResultIDForSchema(ctx context.Context, sessionID string, dag *Server, recordedID uint64, installed []SchemaModuleCandidate) (AnyResult, error) {
	op, err := c.beginSessionOperation(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load schema module %d: %w", recordedID, err)
	}
	lookup, err := c.schemaModuleLookup(ctx, sessionID, recordedID, installed)
	var result AnyResult
	if err == nil {
		result, err = c.loadSelectedResultByResultID(ctx, sessionID, dag, recordedID, lookup)
	}
	if op.finish(err == nil && result != nil) {
		return nil, fmt.Errorf("load schema module %d: %w: %q", recordedID, ErrCacheSessionReleased, sessionID)
	}
	return result, err
}

func (c *Cache) schemaModuleLookup(ctx context.Context, sessionID string, recordedID uint64, installed []SchemaModuleCandidate) (sharedResultLookup, error) {
	if recordedID == 0 {
		return sharedResultLookup{}, fmt.Errorf("resolve result: zero result ID")
	}
	if sessionID == "" {
		return sharedResultLookup{}, fmt.Errorf("schema module selection requires session ID")
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	recorded := c.resultsByID[sharedResultID(recordedID)]
	if recorded == nil {
		return sharedResultLookup{}, fmt.Errorf("resolve result %d: missing shared result", recordedID)
	}
	classes := c.outputEqClassesForResultLocked(recorded.id)
	var selected *sharedResult
	for _, candidate := range installed {
		operational := c.resultsByID[sharedResultID(candidate.ModuleResultID)]
		scoped := c.resultsByID[sharedResultID(candidate.ScopedResultID)]
		if operational == nil || scoped == nil || operational.attachmentState() != resultAttachmentClean || scoped.attachmentState() != resultAttachmentClean {
			continue
		}
		// An inaccessible installed candidate does not suppress the recorded
		// row or the ordinary canonical fallback.
		if !c.sessionSatisfiesResourceRequirementsLocked(sessionID, operational) || !c.sessionSatisfiesResourceRequirementsLocked(sessionID, scoped) {
			continue
		}
		for scopedClass := range c.outputEqClassesForResultLocked(scoped.id) {
			if _, ok := classes[c.findEqClassLocked(scopedClass)]; ok {
				selected = operational
				break
			}
		}
		if selected != nil {
			break
		}
	}
	if selected == nil {
		now := time.Now().Unix()
		if (recorded.expiresAtUnix == 0 || recorded.expiresAtUnix > now) && c.sessionSatisfiesResourceRequirementsLocked(sessionID, recorded) {
			selected = recorded
		} else {
			selected = c.canonicalEquivalentSharedResultLocked(sessionID, recorded, now, true)
		}
	}
	if !c.sessionSatisfiesResourceRequirementsLocked(sessionID, selected) {
		return sharedResultLookup{}, fmt.Errorf("resolve result %d: session %q has not bound the session resources this result requires", recordedID, sessionID)
	}
	generation := selected.requiredSessionResourcesGen.Load()
	tracked, count, err := c.acquireSessionResultLocked(ctx, sessionID, selected)
	if err != nil {
		return sharedResultLookup{}, err
	}
	return sharedResultLookup{res: selected, requiredGenAtCheck: generation, alreadyTracked: tracked, trackedCount: count}, nil
}
