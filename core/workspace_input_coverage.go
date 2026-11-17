package core

import (
	"context"
	"errors"
	"slices"

	"github.com/dagger/dagger/dagql"
)

func (c *workspaceInputCollector) rememberReference(ref *dagql.ResultCallRef) {
	if ref == nil || ref.ResultID == 0 {
		return
	}
	if c.references == nil {
		c.references = map[uint64]*dagql.ResultCallRef{}
	}
	c.references[ref.ResultID] = ref
}

func (r *workspaceInputReads) rememberInputs(refs map[uint64]*dagql.ResultCallRef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = map[uint64]*dagql.ResultCallRef{}
	}
	for id, ref := range refs {
		r.values[id] = ref
	}
}

func (r *workspaceInputReads) hasInputLocked(id uint64) bool {
	if _, ok := r.values[id]; ok {
		return true
	}
	for _, base := range r.prepared {
		if _, ok := base.values[id]; ok {
			return true
		}
	}
	return false
}

func (r *workspaceInputReads) inputIDsLocked() []uint64 {
	ids := make([]uint64, 0, len(r.values))
	for id := range r.values {
		ids = append(ids, id)
	}
	for _, base := range r.prepared {
		for id := range base.values {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

func (r *workspaceInputReads) addPrepared(base *workspacePreparedInputs) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.prepared, base) {
		r.prepared = append(r.prepared, base)
	}
}

// Stored payloads can keep old IDs after an equal value is reused. Their exact
// membership in a current input proves coverage by that input's current facts.
// This walk follows declared payload references, not historical call inputs.
// It checks identity only; it does not copy the stored inputs' origins.
func (r *workspaceInputReads) containsInput(c *workspaceInputCollector, ref *dagql.ResultCallRef) bool {
	r.mu.Lock()
	covered := r.hasInputLocked(ref.ResultID)
	var pending []uint64
	if !covered {
		pending = r.inputIDsLocked()
	}
	r.mu.Unlock()
	if covered {
		return true
	}
	// Fixed payload inputs remain valid while their outputs are evaluated.
	// Read this graph anew for each lookup. Its metadata is partial, so a
	// miss or error must still use the full payload traversal below.
	fixedPending := slices.Clone(pending)
	fixedSeen := map[uint64]bool{}
	for len(fixedPending) != 0 {
		id := fixedPending[0]
		fixedPending = fixedPending[1:]
		if fixedSeen[id] {
			continue
		}
		fixedSeen[id] = true
		if id == ref.ResultID {
			value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, id)
			if err == nil && value != nil {
				r.rememberInputs(map[uint64]*dagql.ResultCallRef{ref.ResultID: ref})
				return true
			}
			continue
		}
		inputs, err := c.cache.ResultPayloadInputIDs(c.ctx, c.session, id)
		if err == nil {
			fixedPending = append(fixedPending, inputs...)
		}
	}
	slices.Sort(pending)
	seen := map[uint64]bool{}
	for len(pending) != 0 {
		id := pending[0]
		pending = pending[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, id)
		if err != nil || value == nil {
			continue
		}
		if id == ref.ResultID {
			r.rememberInputs(map[uint64]*dagql.ResultCallRef{ref.ResultID: ref})
			return true
		}
		record, err := c.cache.CapturePersistedRecord(c.ctx, value)
		if err != nil {
			if errors.Is(err, dagql.ErrPersistStateNotReady) {
				inputs, err := c.cache.ResultPayloadInputIDs(c.ctx, c.session, id)
				if err == nil {
					pending = append(pending, inputs...)
				}
			}
			continue
		}
		var children []uint64
		_, err = dagql.VisitEncodedReferences(record, func(child *dagql.PersistedRef) error {
			if child.Kind != dagql.PersistedRefChild || child.ResultID == 0 {
				return nil
			}
			// The envelope codec places offers at this root path. Their
			// storage dependencies are not references held by the payload.
			if len(child.Path) != 0 && !child.Path[0].IsIndex && child.Path[0].Field == "pendingOffers" {
				return nil
			}
			children = append(children, child.ResultID)
			return nil
		})
		if err != nil {
			continue
		}
		slices.Sort(children)
		pending = append(pending, children...)
	}
	return false
}

func (r *workspaceInputReads) checkReferenceOrigin(ctx context.Context, ref *dagql.ResultCallRef) {
	if ref == nil || ref.ResultID == 0 {
		return
	}
	r.mu.Lock()
	covered := r.hasInputLocked(ref.ResultID)
	r.mu.Unlock()
	if covered {
		return
	}
	operation := "unknown operation"
	c, err := newWorkspaceInputCollector(ctx)
	if err == nil {
		if frame, err := c.cache.ResultCallForRef(ref); err == nil && frame != nil {
			operation = workspaceInputOperation(ctx, frame)
		}
		c.ref(ref)
		if len(c.result.Sources) == 0 && len(c.result.FindUps) == 0 && len(c.result.Issues) == 0 {
			return
		}
		if r.containsInput(c, ref) {
			return
		}
	}
	r.mu.Lock()
	r.summary.incomplete = true
	r.summary.Issues = append(r.summary.Issues, "body input has a source origin not bound to prepared inputs or recorded source operations: "+operation)
	r.mu.Unlock()
}

func (r *workspaceInputReads) checkValueOrigin(ctx context.Context, value dagql.AnyResult) {
	if value == nil || value.Unwrap() == nil {
		return
	}
	id, err := value.ID()
	if err == nil && id != nil && id.EngineResultID() != 0 {
		r.checkReferenceOrigin(ctx, &dagql.ResultCallRef{ResultID: id.EngineResultID()})
	}
}

// Every completed operation contributes its source-bearing input references,
// including operations whose results are discarded or converted to scalars.
func (r *workspaceInputReads) checkBodyInputs(ctx context.Context, frame *dagql.ResultCall, result dagql.AnyResult) {
	r.checkReferenceOrigin(ctx, frame.Receiver)
	var literal func(*dagql.ResultCallLiteral)
	literal = func(value *dagql.ResultCallLiteral) {
		if value == nil {
			return
		}
		r.checkReferenceOrigin(ctx, value.ResultRef)
		for _, item := range value.ListItems {
			literal(item)
		}
		for _, field := range value.ObjectFields {
			if field != nil {
				literal(field.Value)
			}
		}
	}
	for _, arg := range frame.Args {
		if arg != nil {
			literal(arg.Value)
		}
	}
	for _, input := range frame.ImplicitInputs {
		if input != nil {
			literal(input.Value)
		}
	}
	if result != nil && result.Unwrap() != nil {
		id, err := result.ID()
		if err == nil && id != nil && id.EngineResultID() != 0 {
			r.rememberInputs(map[uint64]*dagql.ResultCallRef{id.EngineResultID(): {ResultID: id.EngineResultID()}})
		}
	}
}
