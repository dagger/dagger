package core

import (
	"context"
	"fmt"
	"slices"
	"strings"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	"golang.org/x/sync/errgroup"

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
	// ContainerLazyGroupWrite fills the snapshot parts produced by a
	// non-exec writer; which parts it covers is decided by the op's
	// ContainerLazyGroups from settled metadata.
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

	// ContainerLazyParent returns the result whose snapshot parts are
	// copied by this op's delegation groups.
	ContainerLazyParent() dagql.ObjectResult[*Container]

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
	_ LazyContainerParts = (*ContainerWithMountedCacheLazy)(nil)
	_ LazyContainerParts = (*ContainerWithMountedTempLazy)(nil)
	_ LazyContainerParts = (*ContainerWithMountedVolumeLazy)(nil)
	_ LazyContainerParts = (*ContainerWithMountedSecretLazy)(nil)
	_ LazyContainerParts = (*ContainerWithoutMountLazy)(nil)
	_ LazyContainerParts = (*ContainerWithUnixSocketLazy)(nil)
	_ LazyContainerParts = (*ContainerWithoutUnixSocketLazy)(nil)

	// Template B (snapshot writers): the exec's joint output group and
	// the static rootfs and image writers.
	_ LazyContainerParts = (*ContainerExecLazy)(nil)
	_ LazyContainerParts = (*ContainerWithRootFSLazy)(nil)
	_ LazyContainerParts = (*ContainerFromImageRefLazy)(nil)
	_ LazyContainerParts = (*ContainerWithMountedDirectoryLazy)(nil)
	_ LazyContainerParts = (*ContainerWithMountedFileLazy)(nil)
	_ LazyContainerParts = (*ContainerWithMountedPathDockerfileCompatLazy)(nil)
	_ LazyContainerParts = (*ContainerWithDirectoryLazy)(nil)
	_ LazyContainerParts = (*ContainerWithFileLazy)(nil)
	_ LazyContainerParts = (*ContainerWithoutPathLazy)(nil)
	_ LazyContainerParts = (*ContainerWithSymlinkLazy)(nil)
)

func (lazy *ContainerWithEntrypointLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithoutEntrypointLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithDefaultArgsLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithoutDefaultArgsLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithUserLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithoutUserLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithWorkdirLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithoutWorkdirLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithEnvVariableLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithEnvFileVariablesLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithSystemEnvVariableLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithVolatileVariableLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithoutEnvVariableLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithoutVolatileVariableLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithLabelLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithoutLabelLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithImageConfigMetadataLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithHealthcheckLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithoutHealthcheckLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerSetGPUsLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithAnnotationLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithoutAnnotationLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithSecretVariableLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithoutSecretVariableLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithServiceBindingLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithExposedPortLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithoutExposedPortLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithDefaultTerminalCmdLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerVolatileExecCacheHitLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithMountedCacheLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithMountedTempLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithMountedVolumeLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithMountedSecretLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithoutMountLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithUnixSocketLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithoutUnixSocketLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerExecLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	if lazy == nil || lazy.State == nil {
		return dagql.ObjectResult[*Container]{}
	}
	return lazy.State.Parent
}

func (lazy *ContainerWithRootFSLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerFromImageRefLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithMountedDirectoryLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithMountedFileLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithMountedPathDockerfileCompatLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithDirectoryLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithFileLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithoutPathLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

func (lazy *ContainerWithSymlinkLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	return lazy.Parent
}

// lazyOpForRouting reads the current Lazy op under lazyOpMu. One rule
// covers every access: after construction, the op pointer is only ever
// read or cleared under lazyOpMu, each a short hold with no body ever
// under it. (Construction-time sets - schema shells, WithExec, persisted
// decode - precede publication and therefore any concurrent access.)
// nil means the op is consumed - callers treat it exactly like a value
// with no deferred work (and the cache side independently routes any
// pending bookkeeping, see evaluateResolved).
func (container *Container) lazyOpForRouting() Lazy[*Container] {
	container.lazyOpMu.Lock()
	defer container.lazyOpMu.Unlock()
	return container.Lazy
}

// consumeLazyOp is the locked-store half of the one-rule contract: every
// post-construction clear of the op pointer - unrefined bodies consuming
// themselves, and the refined all-groups-consumed clear - goes through
// here. Safe to call while holding an op's LazyMu (the lock order is
// LazyMu, then lazyOpMu, one way).
func (container *Container) consumeLazyOp() {
	container.lazyOpMu.Lock()
	container.Lazy = nil
	container.lazyOpMu.Unlock()
}

// ResolveLazyEvalGroups implements dagql.HasLazyEvaluationParts. An
// unrefined op maps every part to the whole-result group, which is
// today's behavior byte-for-byte; a refined op settles its own metadata
// part first (the only self-demand anywhere: it runs before any
// requested group's body starts, so bodies read plain fields directly
// and never demand sibling groups) and then delegates the mapping to
// the op.
func (container *Container) ResolveLazyEvalGroups(ctx context.Context, self dagql.AnyResult, parts []dagql.PartKey) ([]dagql.LazyGroupKey, error) {
	if container == nil {
		return nil, nil
	}
	lazy := container.lazyOpForRouting()
	if lazy == nil {
		return nil, nil
	}
	op, ok := lazy.(LazyContainerParts)
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
	lazy := container.lazyOpForRouting()
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
	// Keep the sweep inside the attempt callback. It must finish before the
	// cache decides whether the attempt's resume span is partial.
	if err := container.consumeFinalParentDelegations(ctx, op); err != nil {
		return err
	}
	return container.clearLazyWhenConsumed(ctx, op)
}

// consumeFinalParentDelegations copies every remaining delegated snapshot
// part whose parent part is already final. These copies start no parent work:
// an unrefined pending parent is never final, and a refined parent is final
// for a part only after the group that fills that part has completed.
func (container *Container) consumeFinalParentDelegations(ctx context.Context, op LazyContainerParts) error {
	parent := op.ContainerLazyParent()
	parentCtr := parent.Self()
	if parentCtr == nil {
		return nil
	}

	state := op.ContainerLazyState()
	for _, part := range containerSnapshotParts(container) {
		delegation := containerDelegationGroup(part)
		// Delegation groups are exactly the snapshot groups whose key is the
		// part key. Producer groups such as execOutputs and write never match.
		groups, err := op.ContainerLazyGroups(ctx, container, []dagql.PartKey{part})
		if err != nil {
			return err
		}
		if len(groups) != 1 || groups[0] != delegation || state.GroupConsumed(delegation) {
			continue
		}

		final, err := containerParentPartFinal(ctx, parentCtr, part)
		if err != nil {
			return err
		}
		if !final {
			continue
		}
		if err := op.EvaluateContainerGroup(ctx, container, delegation); err != nil {
			return err
		}
	}
	return nil
}

func containerParentPartFinal(ctx context.Context, parent *Container, part dagql.PartKey) (bool, error) {
	lazy := parent.lazyOpForRouting()
	if lazy == nil {
		return true, nil
	}
	parentOp, ok := lazy.(LazyContainerParts)
	if !ok {
		return false, nil
	}
	// Every refined mapping reads settled metadata. Do not inspect a parent's
	// part mapping until its metadata group is final.
	if !parentOp.ContainerLazyState().GroupConsumed(ContainerLazyGroupMetadata) {
		return false, nil
	}
	groups, err := parentOp.ContainerLazyGroups(ctx, parent, []dagql.PartKey{part})
	if err != nil {
		return false, err
	}
	if len(groups) != 1 {
		return false, fmt.Errorf("container parent part %q maps to %d groups", part, len(groups))
	}
	return parentOp.ContainerLazyState().GroupConsumed(groups[0]), nil
}

// clearLazyWhenConsumed clears container.Lazy when every group of the op
// is consumed. Callable only after some group body succeeded, which
// implies metadata is settled and the op's group set is final.
//
// The consumed-check and the clear happen under one LazyMu hold: two
// sibling groups finishing concurrently may both observe full
// consumption, and the mutex serializes their writes. The store itself
// goes through consumeLazyOp, per the one-rule contract on the op
// pointer (see lazyOpForRouting): after construction, every read and
// every clear of container.Lazy happens under lazyOpMu.
func (container *Container) clearLazyWhenConsumed(ctx context.Context, op LazyContainerParts) error {
	groups, err := op.ContainerLazyGroups(ctx, container, nil)
	if err != nil {
		return err
	}
	state := op.ContainerLazyState()
	state.LazyMu.Lock()
	defer state.LazyMu.Unlock()
	for _, group := range groups {
		if !state.groupConsumedLocked(group) {
			return nil
		}
	}
	container.consumeLazyOp()
	return nil
}

// evaluatePartsDirect is the direct object-side narrow force: settle
// metadata, then run only the groups filling the given parts. For an
// unrefined op it degenerates to full evaluation, exactly like the
// cache-side EvaluateParts. Used by internal reads that hold the
// container value but not its attached result (metaFileContents).
func (container *Container) evaluatePartsDirect(ctx context.Context, parts ...dagql.PartKey) error {
	lazy := container.lazyOpForRouting()
	if lazy == nil {
		return nil
	}
	op, ok := lazy.(LazyContainerParts)
	if !ok {
		return container.Evaluate(ctx)
	}
	if err := container.runLazyGroup(ctx, op, ContainerLazyGroupMetadata); err != nil {
		return err
	}
	groups, err := op.ContainerLazyGroups(ctx, container, parts)
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

// containerMountWriterGroups maps one newly mounted snapshot part to a
// write group and delegates every other snapshot part. Metadata is
// already settled, so target resolution sees the final working
// directory and mount list.
func containerMountWriterGroups(ctr *Container, target string, parts []dagql.PartKey) ([]dagql.LazyGroupKey, error) {
	target = absPath(ctr.Config.WorkingDir, target)
	writtenPart := ContainerPartMount(target)
	if parts == nil {
		groups := []dagql.LazyGroupKey{ContainerLazyGroupMetadata, ContainerLazyGroupWrite}
		for _, part := range containerSnapshotParts(ctr) {
			if part == writtenPart {
				continue
			}
			groups = append(groups, containerDelegationGroup(part))
		}
		return groups, nil
	}
	var groups []dagql.LazyGroupKey
	seen := make(map[dagql.LazyGroupKey]struct{}, len(parts))
	for _, part := range parts {
		var group dagql.LazyGroupKey
		switch part {
		case ContainerPartMetadata:
			group = ContainerLazyGroupMetadata
		case writtenPart:
			group = ContainerLazyGroupWrite
		default:
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

func containerSnapshotPartsForPaths(ctr *Container, paths []string) ([]dagql.PartKey, error) {
	parts := make([]dagql.PartKey, 0, len(paths))
	seen := make(map[dagql.PartKey]struct{}, len(paths))
	for _, target := range paths {
		mnt, _, err := locatePath(ctr, target)
		if err != nil {
			return nil, err
		}
		part := ContainerPartFS
		if mnt != nil {
			part = ContainerPartMount(mnt.Target)
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		parts = append(parts, part)
	}
	return parts, nil
}

// containerPathWriterGroups maps every snapshot part containing a target
// path to one joint write group. Metadata is settled before this runs, so
// exact mount removals have already changed the target resolution.
// Targets in cache or tmpfs mounts return their unsupported-path error during
// the post-metadata nil enumeration used to check lazy consumption.
func containerPathWriterGroups(ctr *Container, paths []string, parts []dagql.PartKey) ([]dagql.LazyGroupKey, error) {
	resolveTargets := parts == nil
	for _, part := range parts {
		if part != ContainerPartMetadata && part != ContainerPartExecMeta {
			resolveTargets = true
			break
		}
	}
	var writtenParts []dagql.PartKey
	if resolveTargets {
		var err error
		writtenParts, err = containerSnapshotPartsForPaths(ctr, paths)
		if err != nil {
			return nil, err
		}
	}
	written := make(map[dagql.PartKey]struct{}, len(writtenParts))
	for _, part := range writtenParts {
		written[part] = struct{}{}
	}
	if parts == nil {
		groups := []dagql.LazyGroupKey{ContainerLazyGroupMetadata, ContainerLazyGroupWrite}
		for _, part := range containerSnapshotParts(ctr) {
			if _, ok := written[part]; ok {
				continue
			}
			groups = append(groups, containerDelegationGroup(part))
		}
		return groups, nil
	}
	groups := make([]dagql.LazyGroupKey, 0, len(parts))
	seen := make(map[dagql.LazyGroupKey]struct{}, len(parts))
	for _, part := range parts {
		group := containerDelegationGroup(part)
		_, isWritten := written[part]
		switch {
		case part == ContainerPartMetadata:
			group = ContainerLazyGroupMetadata
		case isWritten:
			group = ContainerLazyGroupWrite
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		groups = append(groups, group)
	}
	return groups, nil
}

func removableExactContainerMount(mnt *ContainerMount) bool {
	return mnt.CacheSource == nil && mnt.TmpfsSource == nil
}

func evaluateContainerPathWriterGroup(
	ctx context.Context,
	container *Container,
	state *LazyState,
	parent dagql.ObjectResult[*Container],
	group dagql.LazyGroupKey,
	opName string,
	paths []string,
	removeExactMount func(*ContainerMount) bool,
	source dagql.AnyResult,
	write func(context.Context) error,
) error {
	switch group {
	case ContainerLazyGroupMetadata:
		return state.EvaluateGroup(ctx, opName, group, func(ctx context.Context) error {
			if err := materializeContainerMetadataFromParent(ctx, container, parent); err != nil {
				return err
			}
			for _, target := range paths {
				target = absPath(container.Config.WorkingDir, target)
				mnt := container.mountAt(target)
				if mnt == nil || !removeExactMount(mnt) {
					continue
				}
				if _, err := container.WithoutMount(ctx, target); err != nil {
					return err
				}
			}
			container.ImageRef = ""
			return nil
		})
	case ContainerLazyGroupWrite:
		return state.EvaluateGroup(ctx, opName, group, func(ctx context.Context) error {
			parts, err := containerSnapshotPartsForPaths(container, paths)
			if err != nil {
				return err
			}
			eg, egCtx := errgroup.WithContext(ctx)
			for _, part := range parts {
				part := part
				eg.Go(func() error {
					return delegateContainerPart(egCtx, container, parent, part)
				})
			}
			if source != nil {
				eg.Go(func() error {
					cache, err := dagql.EngineCache(egCtx)
					if err != nil {
						return err
					}
					return cache.Evaluate(egCtx, source)
				})
			}
			if err := eg.Wait(); err != nil {
				return err
			}
			return write(ctx)
		})
	default:
		return state.EvaluateGroup(ctx, opName, group, func(ctx context.Context) error {
			return delegateContainerPart(ctx, container, parent, dagql.PartKey(group))
		})
	}
}

// materializeContainerMetadataFromParent evaluates only the parent's
// metadata part and copies the plain fields onto dst: the split-out
// metadata half of materializeContainerStateFromParent. The mount list
// shape is copied from the parent, carrying over dst's existing source
// accessor RECORD for a mount whose target and kind match - keeping the
// accessor pointer stable for the group that fills it (any pre-copied
// value it holds is stale-until-overwritten; delegation always copies
// the parent's part over it) - and giving any other mount a fresh empty
// accessor. Parent accessor values are never cloned here: filling
// snapshot parts is the delegation groups' job, not metadata's.
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
// same cloning the CloneContainer* helpers do. It always evaluates the
// parent and copies, even over an already-set destination accessor: a
// construction-time pre-copy proves nothing about provenance, because
// schema shells clone whatever accessor value is currently visible
// without forcing the parent, so an unrefined writer between an
// evaluated ancestor and this op leaves the shell holding the
// ancestor's value while the parent's pending body will replace it.
// (The overwritten pre-copy's reopened ref is dropped without an
// explicit release, exactly as the whole-op parent-state copy always
// did when it replaced construction-time clones.)
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
		if dstMnt.DirectorySource == nil && dstMnt.FileSource == nil {
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
