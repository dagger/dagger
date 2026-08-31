package core

import (
	"context"
	"fmt"
	"slices"
	"strings"

	bkcache "github.com/dagger/dagger/engine/snapshots"

	"github.com/dagger/dagger/dagql"
)

// Container part keys: one separately evaluable piece of a container.
// The metadata part covers every plain field (Config, Platform, the
// mount list shape, Ports, Annotations, Secrets, Sockets, Services,
// VolatileEnv, EnabledGPUs, ImageRef, DefaultTerminalCmd,
// SystemEnvNames, DefaultArgs); the snapshot parts are the accessors.
const (
	ContainerPartMetadata dagql.PartKey = "metadata"
	ContainerPartFS       dagql.PartKey = "fs"
	ContainerPartExecMeta dagql.PartKey = "execMeta"
)

const containerPartMountPrefix = "mount:"

// ContainerPartMount is the part of the mount source at the given target
// path. Mount parts are keyed by target, not index: targets are unique
// within a mount list (ContainerMounts.With) and survive add, remove,
// and replace.
func ContainerPartMount(target string) dagql.PartKey {
	return dagql.PartKey(containerPartMountPrefix + target)
}

// Container evaluation-group keys. Delegation groups are named by the
// part they fill, so demanding one part never forces another; the keys
// below name the remaining group shapes.
const (
	// ContainerLazyGroupMetadata fills the metadata part.
	ContainerLazyGroupMetadata dagql.LazyGroupKey = "metadata"
	// ContainerLazyGroupExecOutputs is the exec's joint group: running
	// the process fills fs, execMeta, and every writable mount part at
	// once.
	ContainerLazyGroupExecOutputs dagql.LazyGroupKey = "execOutputs"
	// ContainerLazyGroupWrite is the single written part of a
	// target-resolved writer; which part it covers is decided by the
	// op's ContainerLazyGroups from settled metadata.
	ContainerLazyGroupWrite dagql.LazyGroupKey = "write"
)

// containerDelegationGroup names the delegation group that fills one
// snapshot part by copying it from the parent.
func containerDelegationGroup(part dagql.PartKey) dagql.LazyGroupKey {
	return dagql.LazyGroupKey(part)
}

// LazyContainerParts is implemented by refined container lazy ops.
// Unrefined ops implement only Lazy[*Container] and keep whole-result
// behavior byte-for-byte.
type LazyContainerParts interface {
	Lazy[*Container]

	// ContainerLazyGroups maps parts to the groups that fill them, in
	// evaluation order; nil parts means every group of the op, metadata
	// first. The owning result's metadata part is already settled when
	// this is called with nil or positional (mount) parts.
	ContainerLazyGroups(ctx context.Context, ctr *Container, parts []dagql.PartKey) ([]dagql.LazyGroupKey, error)

	// EvaluateContainerGroup runs one group's body against ctr. It must
	// write only the parts of that group and is idempotent per group
	// (LazyState.EvaluateGroup's per-group run-once latch).
	EvaluateContainerGroup(ctx context.Context, ctr *Container, group dagql.LazyGroupKey) error

	// ContainerLazyState exposes the op's per-group latch state for
	// consumption checks. Provided by the embedded LazyState.
	ContainerLazyState() *LazyState
}

var _ dagql.HasLazyEvaluationParts = (*Container)(nil)

// The refined (template A) container ops. An op missing from this list
// is unrefined and keeps whole-result evaluation.
var (
	_ LazyContainerParts = (*ContainerWithEntrypointLazy)(nil)
	_ LazyContainerParts = (*ContainerWithoutEntrypointLazy)(nil)
	_ LazyContainerParts = (*ContainerWithDefaultArgsLazy)(nil)
	_ LazyContainerParts = (*ContainerWithoutDefaultArgsLazy)(nil)
	_ LazyContainerParts = (*ContainerWithUserLazy)(nil)
	_ LazyContainerParts = (*ContainerWithoutUserLazy)(nil)
	_ LazyContainerParts = (*ContainerWithWorkdirLazy)(nil)
	_ LazyContainerParts = (*ContainerWithoutWorkdirLazy)(nil)
	_ LazyContainerParts = (*ContainerWithEnvVariableLazy)(nil)
	_ LazyContainerParts = (*ContainerWithEnvFileVariablesLazy)(nil)
	_ LazyContainerParts = (*ContainerWithSystemEnvVariableLazy)(nil)
	_ LazyContainerParts = (*ContainerWithVolatileVariableLazy)(nil)
	_ LazyContainerParts = (*ContainerWithoutEnvVariableLazy)(nil)
	_ LazyContainerParts = (*ContainerWithoutVolatileVariableLazy)(nil)
	_ LazyContainerParts = (*ContainerWithLabelLazy)(nil)
	_ LazyContainerParts = (*ContainerWithoutLabelLazy)(nil)
	_ LazyContainerParts = (*ContainerWithImageConfigMetadataLazy)(nil)
	_ LazyContainerParts = (*ContainerWithHealthcheckLazy)(nil)
	_ LazyContainerParts = (*ContainerWithoutHealthcheckLazy)(nil)
	_ LazyContainerParts = (*ContainerSetGPUsLazy)(nil)
	_ LazyContainerParts = (*ContainerWithAnnotationLazy)(nil)
	_ LazyContainerParts = (*ContainerWithoutAnnotationLazy)(nil)
	_ LazyContainerParts = (*ContainerWithSecretVariableLazy)(nil)
	_ LazyContainerParts = (*ContainerWithoutSecretVariableLazy)(nil)
	_ LazyContainerParts = (*ContainerWithServiceBindingLazy)(nil)
	_ LazyContainerParts = (*ContainerWithExposedPortLazy)(nil)
	_ LazyContainerParts = (*ContainerWithoutExposedPortLazy)(nil)
	_ LazyContainerParts = (*ContainerWithDefaultTerminalCmdLazy)(nil)
	_ LazyContainerParts = (*ContainerVolatileExecCacheHitLazy)(nil)

	// Template B (snapshot writers): the exec's joint output group and
	// the static rootfs writer.
	_ LazyContainerParts = (*ContainerExecLazy)(nil)
	_ LazyContainerParts = (*ContainerWithRootFSLazy)(nil)
)

// ResolveLazyEvalGroups implements dagql.HasLazyEvaluationParts. An
// unrefined op maps every part to the whole-result group, which is
// today's behavior byte-for-byte; a refined op settles its own metadata
// part first (the only self-demand anywhere: it runs before any
// requested group's body starts, so bodies read plain fields directly
// and never demand sibling groups) and then delegates the mapping to
// the op.
func (container *Container) ResolveLazyEvalGroups(ctx context.Context, self dagql.AnyResult, parts []dagql.PartKey) ([]dagql.LazyGroupKey, error) {
	if container == nil || container.Lazy == nil {
		return nil, nil
	}
	op, ok := container.Lazy.(LazyContainerParts)
	if !ok {
		return []dagql.LazyGroupKey{dagql.LazyGroupWhole}, nil
	}

	metadataOnly := parts != nil
	for _, part := range parts {
		if part != ContainerPartMetadata {
			metadataOnly = false
			break
		}
	}
	if !metadataOnly {
		cache, err := dagql.EngineCache(ctx)
		if err != nil {
			return nil, err
		}
		if err := cache.EvaluateParts(ctx, self, ContainerPartMetadata); err != nil {
			return nil, err
		}
	}
	return op.ContainerLazyGroups(ctx, container, parts)
}

// LazyEvalFuncForGroup implements dagql.HasLazyEvaluationParts.
func (container *Container) LazyEvalFuncForGroup(group dagql.LazyGroupKey) dagql.LazyEvalFunc {
	if container == nil {
		return nil
	}
	lazy := container.Lazy
	if lazy == nil {
		return nil
	}
	op, ok := lazy.(LazyContainerParts)
	if !ok {
		if group == dagql.LazyGroupWhole {
			return container.LazyEvalFunc()
		}
		return nil
	}
	if group == dagql.LazyGroupWhole {
		// A refined op's work lives entirely in its named groups.
		return nil
	}
	if op.ContainerLazyState().GroupConsumed(group) {
		return nil
	}
	return func(ctx context.Context) error {
		return container.runLazyGroup(ctx, op, group)
	}
}

// runLazyGroup runs one group's body and clears container.Lazy once the
// op's last group is consumed, which keeps the two existing whole-result
// signals truthful: LazyEvalFunc() != nil means "something is still
// deferred", and persistence's ready-form selection (Lazy == nil) keeps
// meaning "fully materialized".
func (container *Container) runLazyGroup(ctx context.Context, op LazyContainerParts, group dagql.LazyGroupKey) error {
	if err := op.EvaluateContainerGroup(ctx, container, group); err != nil {
		return err
	}
	return container.clearLazyWhenConsumed(ctx, op)
}

// clearLazyWhenConsumed clears container.Lazy when every group of the op
// is consumed. Callable only after some group body succeeded, which
// implies metadata is settled and the op's group set is final. Two
// sibling groups finishing concurrently may both observe full
// consumption and both clear; the write is idempotent.
func (container *Container) clearLazyWhenConsumed(ctx context.Context, op LazyContainerParts) error {
	groups, err := op.ContainerLazyGroups(ctx, container, nil)
	if err != nil {
		return err
	}
	state := op.ContainerLazyState()
	state.LazyMu.Lock()
	for _, group := range groups {
		if !state.groupConsumedLocked(group) {
			state.LazyMu.Unlock()
			return nil
		}
	}
	state.LazyMu.Unlock()
	container.Lazy = nil
	return nil
}

// evaluateAllLazyGroups is the direct object-side whole-op path for a
// refined op: run the remaining groups sequentially, metadata group
// first, each under its own per-group latch. It never holds two group
// mutexes at once.
func (container *Container) evaluateAllLazyGroups(ctx context.Context, op LazyContainerParts) error {
	if err := container.runLazyGroup(ctx, op, ContainerLazyGroupMetadata); err != nil {
		return err
	}
	groups, err := op.ContainerLazyGroups(ctx, container, nil)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if group == ContainerLazyGroupMetadata {
			continue
		}
		if err := container.runLazyGroup(ctx, op, group); err != nil {
			return err
		}
	}
	return nil
}

// containerSnapshotParts enumerates the snapshot parts of a container
// from its settled metadata: the rootfs, the exec metadata, and every
// directory- or file-backed mount, keyed by target. Cache, volume, and
// tmpfs mounts have no snapshot part: their state is the mount list
// shape plus dependency results.
func containerSnapshotParts(ctr *Container) []dagql.PartKey {
	parts := []dagql.PartKey{ContainerPartFS, ContainerPartExecMeta}
	for i := range ctr.Mounts {
		mnt := &ctr.Mounts[i]
		if mnt.DirectorySource != nil || mnt.FileSource != nil {
			parts = append(parts, ContainerPartMount(mnt.Target))
		}
	}
	return parts
}

// templateAContainerGroups is the group mapping shared by every
// metadata-only mutation: the metadata part maps to the metadata group
// and every snapshot part to its own delegation group.
func templateAContainerGroups(ctr *Container, parts []dagql.PartKey) ([]dagql.LazyGroupKey, error) {
	if parts == nil {
		groups := []dagql.LazyGroupKey{ContainerLazyGroupMetadata}
		for _, part := range containerSnapshotParts(ctr) {
			groups = append(groups, containerDelegationGroup(part))
		}
		return groups, nil
	}
	var groups []dagql.LazyGroupKey
	seen := make(map[dagql.LazyGroupKey]struct{}, len(parts))
	for _, part := range parts {
		group := ContainerLazyGroupMetadata
		if part != ContainerPartMetadata {
			group = containerDelegationGroup(part)
		}
		if _, dup := seen[group]; dup {
			continue
		}
		seen[group] = struct{}{}
		groups = append(groups, group)
	}
	return groups, nil
}

// materializeContainerMetadataFromParent evaluates only the parent's
// metadata part and copies the plain fields onto dst: the split-out
// metadata half of materializeContainerStateFromParent. The mount list
// shape is copied from the parent, carrying over dst's existing source
// accessor for a mount whose target and kind match (parts are keyed by
// target and written once, so an already-set accessor holds the final
// value) and giving any other mount a fresh empty accessor for its
// delegation group to fill.
func materializeContainerMetadataFromParent(ctx context.Context, dst *Container, parent dagql.ObjectResult[*Container]) error {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}
	if err := cache.EvaluateParts(ctx, parent, ContainerPartMetadata); err != nil {
		return err
	}

	parentCtr := parent.Self()
	if parentCtr == nil {
		return fmt.Errorf("materialize container metadata: nil parent container")
	}

	existingMounts := make(map[string]*ContainerMount, len(dst.Mounts))
	for i := range dst.Mounts {
		existingMounts[dst.Mounts[i].Target] = &dst.Mounts[i]
	}
	mounts := make(ContainerMounts, len(parentCtr.Mounts))
	for i, mnt := range parentCtr.Mounts {
		cp := mnt
		cp.DirectorySource = nil
		cp.FileSource = nil
		existing := existingMounts[mnt.Target]
		switch {
		case mnt.DirectorySource != nil:
			if existing != nil && existing.DirectorySource != nil {
				cp.DirectorySource = existing.DirectorySource
			} else {
				cp.DirectorySource = new(LazyAccessor[*Directory, *Container])
			}
		case mnt.FileSource != nil:
			if existing != nil && existing.FileSource != nil {
				cp.FileSource = existing.FileSource
			} else {
				cp.FileSource = new(LazyAccessor[*File, *Container])
			}
		}
		mounts[i] = cp
	}

	dst.Config = CloneContainerImageConfig(parentCtr.Config)
	dst.EnabledGPUs = slices.Clone(parentCtr.EnabledGPUs)
	dst.Mounts = mounts
	dst.Platform = parentCtr.Platform
	dst.Annotations = slices.Clone(parentCtr.Annotations)
	dst.Secrets = slices.Clone(parentCtr.Secrets)
	dst.VolatileEnv = slices.Clone(parentCtr.VolatileEnv)
	dst.Sockets = slices.Clone(parentCtr.Sockets)
	dst.ImageRef = parentCtr.ImageRef
	dst.Ports = slices.Clone(parentCtr.Ports)
	dst.Services = slices.Clone(parentCtr.Services)
	dst.DefaultTerminalCmd = parentCtr.DefaultTerminalCmd
	dst.SystemEnvNames = slices.Clone(parentCtr.SystemEnvNames)
	dst.DefaultArgs = parentCtr.DefaultArgs
	return nil
}

// delegateContainerPart evaluates the parent's part and copies its value
// into dst's accessor for the same part: a detached clone for
// Directory/File values and a reopened handle for snapshot refs, the
// same cloning the CloneContainer* helpers do. An already-set dst
// accessor is left alone: parts are written once, so a value pre-seeded
// at construction is the parent part's final value already.
func delegateContainerPart(ctx context.Context, dst *Container, parent dagql.ObjectResult[*Container], part dagql.PartKey) error {
	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return err
	}

	parentCtr := parent.Self()
	if parentCtr == nil {
		return fmt.Errorf("delegate container part %q: nil parent container", part)
	}

	switch {
	case part == ContainerPartFS:
		if dst.FS != nil {
			if _, set := dst.FS.Peek(); set {
				return nil
			}
		}
		if err := cache.EvaluateParts(ctx, parent, part); err != nil {
			return err
		}
		cloned, err := CloneContainerDirectoryAccessor(ctx, parentCtr.FS)
		if err != nil {
			return err
		}
		copyContainerDirectoryAccessorValue(dst.ensureFSAccessor(), cloned)
		return nil

	case part == ContainerPartExecMeta:
		if dst.MetaSnapshot != nil {
			if _, set := dst.MetaSnapshot.Peek(); set {
				return nil
			}
		}
		if err := cache.EvaluateParts(ctx, parent, part); err != nil {
			return err
		}
		cloned, err := CloneContainerMetaSnapshot(ctx, parentCtr.MetaSnapshot)
		if err != nil {
			return err
		}
		if cloned != nil {
			if snapshot, ok := cloned.Peek(); ok && snapshot != nil {
				dst.ensureMetaSnapshotAccessor().SetValue(snapshot)
			}
		}
		return nil

	case strings.HasPrefix(string(part), containerPartMountPrefix):
		target := strings.TrimPrefix(string(part), containerPartMountPrefix)
		dstMnt := dst.mountAt(target)
		if dstMnt == nil {
			return fmt.Errorf("delegate container part %q: no mount at target", part)
		}
		switch {
		case dstMnt.DirectorySource != nil:
			if _, set := dstMnt.DirectorySource.Peek(); set {
				return nil
			}
		case dstMnt.FileSource != nil:
			if _, set := dstMnt.FileSource.Peek(); set {
				return nil
			}
		default:
			return fmt.Errorf("delegate container part %q: mount has no snapshot source", part)
		}
		if err := cache.EvaluateParts(ctx, parent, part); err != nil {
			return err
		}
		parentMnt := parentCtr.mountAt(target)
		if parentMnt == nil {
			return fmt.Errorf("delegate container part %q: no parent mount at target", part)
		}
		switch {
		case dstMnt.DirectorySource != nil:
			cloned, err := CloneContainerDirectoryAccessor(ctx, parentMnt.DirectorySource)
			if err != nil {
				return err
			}
			copyContainerDirectoryAccessorValue(dstMnt.DirectorySource, cloned)
		case dstMnt.FileSource != nil:
			cloned, err := CloneContainerFileAccessor(ctx, parentMnt.FileSource)
			if err != nil {
				return err
			}
			if cloned != nil {
				if file, ok := cloned.Peek(); ok && file != nil {
					dstMnt.FileSource.SetValue(file)
				}
			}
		}
		return nil

	default:
		return fmt.Errorf("delegate container part %q: unknown part", part)
	}
}

// evaluateTemplateAContainerGroup runs one group of a metadata-only
// mutation: the metadata group copies the parent's metadata and applies
// the op's field edit; every other group delegates its part from the
// parent.
func evaluateTemplateAContainerGroup(
	ctx context.Context,
	op LazyContainerParts,
	typeName string,
	ctr *Container,
	parent dagql.ObjectResult[*Container],
	group dagql.LazyGroupKey,
	applyMetadata func(context.Context) error,
) error {
	state := op.ContainerLazyState()
	if group == ContainerLazyGroupMetadata {
		return state.EvaluateGroup(ctx, typeName, group, func(ctx context.Context) error {
			if err := materializeContainerMetadataFromParent(ctx, ctr, parent); err != nil {
				return err
			}
			return applyMetadata(ctx)
		})
	}
	return state.EvaluateGroup(ctx, typeName, group, func(ctx context.Context) error {
		return delegateContainerPart(ctx, ctr, parent, dagql.PartKey(group))
	})
}

func (container *Container) ensureFSAccessor() *LazyAccessor[*Directory, *Container] {
	if container.FS == nil {
		container.FS = new(LazyAccessor[*Directory, *Container])
	}
	return container.FS
}

func (container *Container) ensureMetaSnapshotAccessor() *LazyAccessor[bkcache.ImmutableRef, *Container] {
	if container.MetaSnapshot == nil {
		container.MetaSnapshot = new(LazyAccessor[bkcache.ImmutableRef, *Container])
	}
	return container.MetaSnapshot
}

// mountAt returns the mount with the given target, or nil.
func (container *Container) mountAt(target string) *ContainerMount {
	for i := range container.Mounts {
		if container.Mounts[i].Target == target {
			return &container.Mounts[i]
		}
	}
	return nil
}

// copyContainerDirectoryAccessorValue moves a cloned accessor's value
// onto the destination accessor when the clone produced one.
func copyContainerDirectoryAccessorValue(dst, cloned *LazyAccessor[*Directory, *Container]) {
	if dst == nil || cloned == nil {
		return
	}
	if dir, ok := cloned.Peek(); ok && dir != nil {
		dst.SetValue(dir)
	}
}
