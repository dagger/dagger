package core

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/dagger/dagger/dagql"
)

// HasWorkloadEvaluation reports a retained operation, not whether it still
// needs to run. Opening a snapshot with no saved operation is storage access,
// not evidence that execution-producing work was reused.
func (container *Container) HasWorkloadEvaluation() bool {
	if container == nil {
		return false
	}
	if view := container.acquiredOutput.Load(); view != nil {
		return len(view.Payload.LazyJSON) != 0
	}
	lazy, saved := container.lazyForPersistence()
	if restore, ok := lazy.(*ContainerRestoreLazy); ok {
		return restore.recipe != nil || len(saved) != 0
	}
	return lazy != nil || len(saved) != 0
}

// WorkloadEvaluationGroups reads the retained operation after whole-result
// completion. Metadata is already final; no sibling is evaluated.
func (container *Container) WorkloadEvaluationGroups(ctx context.Context, frame *dagql.ResultCall, parts []dagql.PartKey) ([]dagql.LazyGroupKey, error) {
	lazy, saved := container.lazyForPersistence()
	if view := container.acquiredOutput.Load(); view != nil {
		saved = view.Payload.LazyJSON
	}
	if restore, ok := lazy.(*ContainerRestoreLazy); ok {
		lazy = restore.recipe
	}
	if op, refined := lazy.(LazyContainerParts); refined {
		return op.ContainerLazyGroups(ctx, container, parts)
	}
	if lazy != nil {
		return []dagql.LazyGroupKey{dagql.LazyGroupWhole}, nil
	}
	if len(saved) != 0 && frame != nil {
		// These retained recipes evaluate the whole value, as RouteParts does.
		if frame.Field == "_builtinContainer" || frame.Field == "import" {
			return []dagql.LazyGroupKey{dagql.LazyGroupWhole}, nil
		}
		return rawContainerGroups(frame.Field, saved, container, parts)
	}
	return nil, nil
}

func (container *Container) WorkloadEvaluationGroup(ctx context.Context, frame *dagql.ResultCall, group dagql.LazyGroupKey) (dagql.LazyGroupKey, error) {
	part, stored := strings.CutPrefix(string(group), containerStoredOpenPrefix)
	if !stored {
		if group != ContainerLazyGroupMetadata {
			return group, nil
		}
		part = string(ContainerPartMetadata)
	}
	groups, err := container.WorkloadEvaluationGroups(ctx, frame, []dagql.PartKey{dagql.PartKey(part)})
	if err != nil || len(groups) != 1 {
		return "", err
	}
	return groups[0], nil
}

// WorkloadEvaluationParent reads retained routing without capturing snapshots.
// Snapshot publication for a sibling must not hide a completed group's owner.
func (container *Container) WorkloadEvaluationParent(ctx context.Context, frame *dagql.ResultCall, group dagql.LazyGroupKey) (*dagql.PartDelegation, error) {
	if group != dagql.LazyGroupKey(ContainerPartFS) && group != dagql.LazyGroupKey(ContainerPartExecMeta) &&
		!strings.HasPrefix(string(group), containerPartMountPrefix) {
		return nil, nil
	}
	part := dagql.PartKey(group)
	groups, err := container.WorkloadEvaluationGroups(ctx, frame, []dagql.PartKey{part})
	if err != nil || len(groups) != 1 || groups[0] != group {
		return nil, err
	}
	lazy, saved := container.lazyForPersistence()
	if view := container.acquiredOutput.Load(); view != nil {
		saved = view.Payload.LazyJSON
	}
	if restore, ok := lazy.(*ContainerRestoreLazy); ok {
		lazy = restore.recipe
	}
	if op, ok := lazy.(LazyContainerParts); ok {
		parent := op.ContainerLazyParent()
		if parent.Self() != nil {
			if id, ok := dagql.CacheResultNumber(parent); ok {
				return &dagql.PartDelegation{ParentResultID: id, Address: dagql.PersistedPartAddress{Part: part}}, nil
			}
		}
	}
	var parent struct {
		ParentResultID                 uint64 `json:"parentResultID"`
		VolatileCacheHitParentResultID uint64 `json:"volatileCacheHitParentResultID"`
	}
	if len(saved) != 0 {
		if err := json.Unmarshal(saved, &parent); err != nil {
			return nil, err
		}
	}
	if parent.VolatileCacheHitParentResultID != 0 {
		parent.ParentResultID = parent.VolatileCacheHitParentResultID
	}
	if parent.ParentResultID == 0 {
		return nil, nil
	}
	return &dagql.PartDelegation{ParentResultID: parent.ParentResultID, Address: dagql.PersistedPartAddress{Part: part}}, nil
}

func (family foreignFamilyCodec) WorkloadPartDelegation(v dagql.PersistedPayloadVisit, group dagql.LazyGroupKey) (*dagql.PartDelegation, error) {
	if family != "Container" || (group != dagql.LazyGroupKey(ContainerPartFS) &&
		group != dagql.LazyGroupKey(ContainerPartExecMeta) && !strings.HasPrefix(string(group), containerPartMountPrefix)) {
		return nil, nil
	}
	var payload persistedContainerPayload
	if err := json.Unmarshal(v.Payload, &payload); err != nil {
		return nil, err
	}
	part := dagql.PartKey(group)
	if len(payload.LazyJSON) == 0 {
		return containerPartDelegation(v, payload, part)
	}
	var parent struct {
		ParentResultID                 uint64 `json:"parentResultID"`
		VolatileCacheHitParentResultID uint64 `json:"volatileCacheHitParentResultID"`
	}
	if err := json.Unmarshal(payload.LazyJSON, &parent); err != nil {
		return nil, err
	}
	if parent.VolatileCacheHitParentResultID != 0 {
		parent.ParentResultID = parent.VolatileCacheHitParentResultID
	}
	if parent.ParentResultID == 0 {
		return nil, nil
	}
	route, err := family.RouteParts(v, part)
	if err != nil || route.NeedsMetadata || route.Group.Group != group {
		return nil, err
	}
	return &dagql.PartDelegation{ParentResultID: parent.ParentResultID, Address: dagql.PersistedPartAddress{Part: part}}, nil
}

func (dir *Directory) HasWorkloadEvaluation() bool {
	if dir == nil {
		return false
	}
	dir.outputMu.Lock()
	defer dir.outputMu.Unlock()
	if _, restored := dir.Lazy.(*DirectoryRestoreLazy); restored {
		return dir.lazyKind != ""
	}
	return dir.Lazy != nil || dir.lazyKind != ""
}

func (file *File) HasWorkloadEvaluation() bool {
	if file == nil {
		return false
	}
	file.outputMu.Lock()
	defer file.outputMu.Unlock()
	if _, restored := file.Lazy.(*FileRestoreLazy); restored {
		return file.lazyKind != ""
	}
	return file.Lazy != nil || file.lazyKind != ""
}
