package dagql

import (
	"context"
	"fmt"
	"sync/atomic"
)

// resultCallFrameGeneration counts replacements of a result's call frame
// (storeResultCall swapping one frame for another). A frame's own fields don't
// change once it is stored, but the frames of the results it references can
// be replaced, and an equivalent replacement can reference different Module
// results: canonicalization goes by e-graph class, not result identity. So a
// frame's memoized module result IDs hold only for the generation they were
// computed in.
var resultCallFrameGeneration atomic.Uint64

type moduleResultIDsMemo struct {
	gen uint64
	ids []uint64
}

// ModuleResultIDs returns the result IDs of the Module results that rootCall's
// call graph references, each once, in the order WalkResultCall visits them.
// Each frame's answer is memoized on the frame for the current frame
// generation, built from the answers of the frames it references, so repeated
// calls over shared call graphs don't walk them again. It stops with ctx's
// error once ctx is done.
func (c *Cache) ModuleResultIDs(ctx context.Context, rootCall *ResultCall) ([]uint64, error) {
	if rootCall == nil {
		return nil, nil
	}
	r := newModuleIDsRebuild(ctx, resultCallFrameGeneration.Load())
	ids, _, err := c.moduleResultIDs(rootCall, r)
	return ids, err
}

// moduleIDsRebuild is one ModuleResultIDs call's state. Each frame it computes
// is recorded in done, whether or not it could be memoized, so the call
// computes every frame at most once, as WalkResultCall visits every frame at
// most once.
type moduleIDsRebuild struct {
	ctx    context.Context
	gen    uint64
	onPath map[*ResultCall]bool
	done   map[*ResultCall]moduleIDsDone
	// computed counts the frames this rebuild computed.
	computed int
}

type moduleIDsDone struct {
	ids    []uint64
	cyclic bool
}

func newModuleIDsRebuild(ctx context.Context, gen uint64) *moduleIDsRebuild {
	return &moduleIDsRebuild{
		ctx:    ctx,
		gen:    gen,
		onPath: map[*ResultCall]bool{},
		done:   map[*ResultCall]moduleIDsDone{},
	}
}

// moduleResultIDs reports, besides the IDs, whether the walk met a frame
// already on its path: the call graph has a cycle. A frame reached through a
// cycle can't be memoized, since its answer would miss its ancestors' modules,
// but it's still recorded for the rest of this rebuild: without that, every
// further path to it would compute it again, exponentially in how much of the
// graph is shared.
func (c *Cache) moduleResultIDs(frame *ResultCall, r *moduleIDsRebuild) ([]uint64, bool, error) {
	if m := frame.moduleResultIDs.Load(); m != nil && m.gen == r.gen {
		return m.ids, false, nil
	}
	if d, ok := r.done[frame]; ok {
		return d.ids, d.cyclic, nil
	}
	if r.onPath[frame] {
		return nil, true, nil
	}
	r.onPath[frame] = true
	defer delete(r.onPath, frame)
	r.computed++
	if r.computed%256 == 0 {
		if err := r.ctx.Err(); err != nil {
			return nil, false, err
		}
	}

	var ids []uint64
	seen := map[uint64]struct{}{}
	add := func(list []uint64) {
		for _, id := range list {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	cyclic := false

	var walkLiteral func(*ResultCallLiteral) error
	walkRef := func(ref *ResultCallRef) error {
		if ref == nil {
			return nil
		}
		child := ref.Call
		if child == nil {
			if ref.ResultID == 0 {
				return nil
			}
			child = c.resultCallByResultID(sharedResultID(ref.ResultID))
			if child == nil {
				return fmt.Errorf("missing result call frame for result %d", ref.ResultID)
			}
		}
		if ref.ResultID != 0 && child.Type != nil && child.Type.NamedType == "Module" {
			add([]uint64{ref.ResultID})
		}
		childIDs, childCyclic, err := c.moduleResultIDs(child, r)
		if err != nil {
			return err
		}
		cyclic = cyclic || childCyclic
		add(childIDs)
		return nil
	}
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

	if frame.Module != nil {
		if err := walkRef(frame.Module.ResultRef); err != nil {
			return nil, false, fmt.Errorf("module %q: %w", frame.Module.Name, err)
		}
	}
	if err := walkRef(frame.Receiver); err != nil {
		return nil, false, fmt.Errorf("receiver: %w", err)
	}
	for _, arg := range frame.Args {
		if arg == nil {
			continue
		}
		if err := walkLiteral(arg.Value); err != nil {
			return nil, false, fmt.Errorf("arg %q: %w", arg.Name, err)
		}
	}
	for _, input := range frame.ImplicitInputs {
		if input == nil {
			continue
		}
		if err := walkLiteral(input.Value); err != nil {
			return nil, false, fmt.Errorf("implicit input %q: %w", input.Name, err)
		}
	}
	r.done[frame] = moduleIDsDone{ids: ids, cyclic: cyclic}
	if !cyclic {
		frame.moduleResultIDs.Store(&moduleResultIDsMemo{gen: r.gen, ids: ids})
	}
	return ids, cyclic, nil
}
