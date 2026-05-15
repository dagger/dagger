package dagql

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/opencontainers/go-digest"
)

func (c *Cache) liveRuntimeDependencies(deps runtimeResultDependencySet) (runtimeResultDependencySet, error) {
	if len(deps) == 0 {
		return nil, nil
	}
	live := make(runtimeResultDependencySet, 0, len(deps))
	seen := make(map[string]struct{}, len(deps))
	appendLive := func(dep runtimeResultDependency) error {
		if dep.Frame == nil || dep.Digest == "" {
			return nil
		}
		frameDigest := dep.FrameDigest
		if frameDigest == "" {
			dig, err := dep.Frame.deriveRecipeDigest(c)
			if err != nil {
				return fmt.Errorf("derive runtime dependency frame digest for result %d: %w", dep.ResultID, err)
			}
			frameDigest = dig
		}
		if frameDigest == "" {
			return fmt.Errorf("runtime dependency for result %d has empty frame digest", dep.ResultID)
		}
		key := runtimeResultDependencyValidationKey(runtimeResultDependency{
			FrameDigest: frameDigest,
			Digest:      dep.Digest,
		})
		if _, ok := seen[key]; ok {
			return nil
		}
		seen[key] = struct{}{}
		live = append(live, runtimeResultDependency{
			ResultID:    dep.ResultID,
			Frame:       dep.Frame.clone(),
			FrameDigest: frameDigest,
			Digest:      dep.Digest,
		})
		return nil
	}
	for _, dep := range deps {
		if c.resultCallRequiresLiveSourceValidation(dep.Frame) {
			if err := appendLive(dep); err != nil {
				return nil, err
			}
		}
		for _, freshnessDep := range dep.FreshnessDeps {
			if err := appendLive(freshnessDep); err != nil {
				return nil, err
			}
		}
	}
	slices.SortFunc(live, func(a, b runtimeResultDependency) int {
		return cmp.Compare(runtimeResultDependencyValidationKey(a), runtimeResultDependencyValidationKey(b))
	})
	return live, nil
}

func runtimeResultDependencyValidationKey(dep runtimeResultDependency) string {
	return fmt.Sprintf("%s\x00%s", dep.FrameDigest, dep.Digest)
}

func (c *Cache) resultCallRequiresLiveSourceValidation(frame *ResultCall) bool {
	if frame == nil || !c.resultCallDependsOnLiveSource(frame) {
		return false
	}
	if frame.Type != nil {
		switch frame.Type.NamedType {
		case "Directory", "File":
			return true
		}
	}
	return frame.Field == "contents" && c.resultCallRefDependsOnLiveSource(frame.Receiver)
}

func (c *Cache) resultCallDependsOnLiveSource(frame *ResultCall) bool {
	return c.resultCallDependsOnLiveSourceWithSeen(frame, map[*ResultCall]struct{}{}, map[sharedResultID]struct{}{})
}

func (c *Cache) resultCallDependsOnLiveSourceWithSeen(
	frame *ResultCall,
	seenCalls map[*ResultCall]struct{},
	seenResults map[sharedResultID]struct{},
) bool {
	if frame == nil {
		return false
	}
	if _, seen := seenCalls[frame]; seen {
		return false
	}
	seenCalls[frame] = struct{}{}

	if c.resultCallIsLiveSourceRoot(frame) {
		return true
	}
	if c.resultCallRefDependsOnLiveSourceWithSeen(frame.Receiver, seenCalls, seenResults) {
		return true
	}
	if frame.Module != nil && c.resultCallRefDependsOnLiveSourceWithSeen(frame.Module.ResultRef, seenCalls, seenResults) {
		return true
	}
	for _, arg := range frame.Args {
		if arg != nil && c.resultCallLiteralDependsOnLiveSource(arg.Value, seenCalls, seenResults) {
			return true
		}
	}
	for _, input := range frame.ImplicitInputs {
		if input != nil && c.resultCallLiteralDependsOnLiveSource(input.Value, seenCalls, seenResults) {
			return true
		}
	}
	return false
}

func (c *Cache) resultCallIsLiveSourceRoot(frame *ResultCall) bool {
	if frame == nil {
		return false
	}
	if frame.Field == "currentWorkspace" {
		return true
	}
	if (frame.Field == "directory" || frame.Field == "file") &&
		resultCallBoolArg(frame.Args, "noCache") &&
		c.resultCallRefIsHost(frame.Receiver) {
		return true
	}
	return false
}

func (c *Cache) resultCallRefIsHost(ref *ResultCallRef) bool {
	frame := c.resultCallFrameForRef(ref)
	return frame != nil && ((frame.Type != nil && frame.Type.NamedType == "Host") || frame.Field == "host")
}

func resultCallBoolArg(args []*ResultCallArg, name string) bool {
	for _, arg := range args {
		if arg == nil || arg.Name != name || arg.Value == nil {
			continue
		}
		return arg.Value.Kind == ResultCallLiteralKindBool && arg.Value.BoolValue
	}
	return false
}

func (c *Cache) resultCallRefDependsOnLiveSource(ref *ResultCallRef) bool {
	return c.resultCallRefDependsOnLiveSourceWithSeen(ref, map[*ResultCall]struct{}{}, map[sharedResultID]struct{}{})
}

func (c *Cache) resultCallRefDependsOnLiveSourceWithSeen(
	ref *ResultCallRef,
	seenCalls map[*ResultCall]struct{},
	seenResults map[sharedResultID]struct{},
) bool {
	frame := c.resultCallFrameForRefWithSeen(ref, seenResults)
	return c.resultCallDependsOnLiveSourceWithSeen(frame, seenCalls, seenResults)
}

func (c *Cache) resultCallLiteralDependsOnLiveSource(
	lit *ResultCallLiteral,
	seenCalls map[*ResultCall]struct{},
	seenResults map[sharedResultID]struct{},
) bool {
	if lit == nil {
		return false
	}
	switch lit.Kind {
	case ResultCallLiteralKindResultRef:
		return c.resultCallRefDependsOnLiveSourceWithSeen(lit.ResultRef, seenCalls, seenResults)
	case ResultCallLiteralKindList:
		for _, item := range lit.ListItems {
			if c.resultCallLiteralDependsOnLiveSource(item, seenCalls, seenResults) {
				return true
			}
		}
	case ResultCallLiteralKindObject:
		for _, field := range lit.ObjectFields {
			if field != nil && c.resultCallLiteralDependsOnLiveSource(field.Value, seenCalls, seenResults) {
				return true
			}
		}
	}
	return false
}

func (c *Cache) resultCallFrameForRef(ref *ResultCallRef) *ResultCall {
	return c.resultCallFrameForRefWithSeen(ref, map[sharedResultID]struct{}{})
}

func (c *Cache) resultCallFrameForRefWithSeen(ref *ResultCallRef, seenResults map[sharedResultID]struct{}) *ResultCall {
	if ref == nil {
		return nil
	}
	if ref.Call != nil {
		return ref.Call
	}
	resultID := sharedResultID(ref.ResultID)
	if resultID == 0 {
		return nil
	}
	if _, seen := seenResults[resultID]; seen {
		return nil
	}
	seenResults[resultID] = struct{}{}
	defer delete(seenResults, resultID)
	return c.resultCallByResultID(resultID)
}

func (c *Cache) validateRuntimeDependencySets(ctx context.Context, depSets []runtimeResultDependencySet) (runtimeResultDependencySet, error) {
	if len(depSets) == 0 {
		return nil, nil
	}
	var staleErr error
	for _, deps := range depSets {
		err := c.validateRuntimeDependencySet(ctx, deps)
		if err == nil {
			return deps, nil
		}
		if !isCacheValidationFailed(err) {
			return nil, err
		}
		staleErr = err
	}
	if staleErr == nil {
		return nil, nil
	}
	return nil, staleErr
}

func (c *Cache) validateRuntimeDependencySet(ctx context.Context, deps runtimeResultDependencySet) error {
	for _, dep := range deps {
		if dep.Frame == nil {
			return fmt.Errorf("runtime dependency for result %d has nil frame", dep.ResultID)
		}
		if dep.Digest == "" {
			return fmt.Errorf("runtime dependency for result %d has empty digest", dep.ResultID)
		}
		current, err := c.refreshRuntimeDependencyDigest(ctx, dep)
		if err != nil {
			return fmt.Errorf("%w: refresh runtime dependency result %d: %w", ErrCacheValidationFailed, dep.ResultID, err)
		}
		if current != dep.Digest {
			return fmt.Errorf("%w: runtime dependency result %d expected %s got %s", ErrCacheValidationFailed, dep.ResultID, dep.Digest, current)
		}
	}
	return nil
}

func isCacheValidationFailed(err error) bool {
	return errors.Is(err, ErrCacheValidationFailed)
}

func (c *Cache) refreshRuntimeDependencyDigest(ctx context.Context, dep runtimeResultDependency) (digest.Digest, error) {
	refreshed, err := c.refreshResultCall(ctx, dep.Frame, map[sharedResultID]struct{}{})
	if err != nil {
		return "", err
	}
	frame, err := refreshed.ResultCall()
	if err != nil {
		return "", err
	}
	current, err := frame.deriveContentPreferredDigest(c)
	if err != nil {
		return "", err
	}
	return current, nil
}

func (c *Cache) refreshResultCall(
	ctx context.Context,
	frame *ResultCall,
	seenResults map[sharedResultID]struct{},
) (AnyResult, error) {
	if frame == nil {
		return nil, fmt.Errorf("nil result call")
	}
	srv := CurrentDagqlServer(ctx)
	if srv == nil {
		return nil, fmt.Errorf("current dagql server not found")
	}
	var base AnyResult = srv.root
	var err error
	if frame.Receiver != nil {
		base, err = c.refreshResultCallRef(ctx, frame.Receiver, seenResults)
		if err != nil {
			return nil, fmt.Errorf("receiver: %w", err)
		}
	}
	baseObj, err := srv.toSelectable(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("instantiate base: %w", err)
	}
	sel, err := c.selectorFromResultCallForRefresh(ctx, frame, baseObj, seenResults)
	if err != nil {
		return nil, err
	}
	return baseObj.Select(ctx, srv, sel)
}

func (c *Cache) refreshResultCallRef(
	ctx context.Context,
	ref *ResultCallRef,
	seenResults map[sharedResultID]struct{},
) (AnyResult, error) {
	if ref == nil {
		return nil, fmt.Errorf("nil result ref")
	}
	if ref.Call != nil {
		return c.refreshResultCall(ctx, ref.Call, seenResults)
	}
	resultID := sharedResultID(ref.ResultID)
	if resultID == 0 {
		return nil, fmt.Errorf("result ref has no result ID")
	}
	if _, seen := seenResults[resultID]; seen {
		return nil, fmt.Errorf("cycle while refreshing result %d", resultID)
	}
	seenResults[resultID] = struct{}{}
	defer delete(seenResults, resultID)
	frame := c.resultCallByResultID(resultID)
	if frame == nil {
		return nil, fmt.Errorf("result %d has no call frame", resultID)
	}
	return c.refreshResultCall(ctx, frame, seenResults)
}

func (c *Cache) selectorFromResultCallForRefresh(
	ctx context.Context,
	frame *ResultCall,
	baseObj AnyObjectResult,
	seenResults map[sharedResultID]struct{},
) (Selector, error) {
	view := frame.View
	fieldSpec, ok := baseObj.ObjectType().FieldSpec(frame.Field, view)
	if !ok {
		return Selector{}, fmt.Errorf("field %q not found on %s", frame.Field, baseObj.Type().Name())
	}
	args := make([]NamedInput, 0, len(frame.Args))
	for _, argSpec := range fieldSpec.Args.Inputs(view) {
		var frameArg *ResultCallArg
		for _, arg := range frame.Args {
			if arg != nil && arg.Name == argSpec.Name {
				frameArg = arg
				break
			}
		}
		if frameArg == nil {
			continue
		}
		inputVal, err := c.inputValueFromResultCallLiteralForRefresh(ctx, frameArg.Value, seenResults)
		if err != nil {
			return Selector{}, fmt.Errorf("request arg %q literal input: %w", argSpec.Name, err)
		}
		input, err := argSpec.Type.Decoder().DecodeInput(inputVal)
		if err != nil {
			return Selector{}, fmt.Errorf("request arg %q value as %T (%s) using %T: %w", argSpec.Name, argSpec.Type, argSpec.Type.Type(), argSpec.Type.Decoder(), err)
		}
		args = append(args, NamedInput{Name: argSpec.Name, Value: input})
	}
	return Selector{
		Field: frame.Field,
		Args:  args,
		Nth:   int(frame.Nth),
		View:  view,
	}, nil
}

func (c *Cache) inputValueFromResultCallLiteralForRefresh(
	ctx context.Context,
	lit *ResultCallLiteral,
	seenResults map[sharedResultID]struct{},
) (any, error) {
	if lit == nil {
		return nil, nil
	}
	switch lit.Kind {
	case ResultCallLiteralKindResultRef:
		if c.resultCallRefDependsOnLiveSource(lit.ResultRef) {
			refreshed, err := c.refreshResultCallRef(ctx, lit.ResultRef, seenResults)
			if err != nil {
				return nil, err
			}
			return refreshed.ID()
		}
		return handleIDFromResultCallRef(ctx, lit.ResultRef)
	case ResultCallLiteralKindList:
		values := make([]any, 0, len(lit.ListItems))
		for _, item := range lit.ListItems {
			val, err := c.inputValueFromResultCallLiteralForRefresh(ctx, item, seenResults)
			if err != nil {
				return nil, err
			}
			values = append(values, val)
		}
		return values, nil
	case ResultCallLiteralKindObject:
		values := make(map[string]any, len(lit.ObjectFields))
		for _, field := range lit.ObjectFields {
			if field == nil {
				continue
			}
			val, err := c.inputValueFromResultCallLiteralForRefresh(ctx, field.Value, seenResults)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", field.Name, err)
			}
			values[field.Name] = val
		}
		return values, nil
	default:
		return inputValueFromResultCallLiteral(ctx, lit)
	}
}
