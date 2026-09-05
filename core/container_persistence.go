package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

const (
	containerPartPending      = "pending"
	containerPartAbsent       = "absent"
	containerPartDirectory    = "directory"
	containerPartFile         = "file"
	containerPartSnapshot     = "snapshot"
	containerStoredOpenPrefix = "open:"
)

// Snapshot identity lives in the envelope's ordinary role links. The part
// record carries the detached value needed to construct its accessor later.
type persistedContainerPart struct {
	Kind     string                    `json:"kind"`
	Role     string                    `json:"role,omitempty"`
	Path     string                    `json:"path,omitempty"`
	Platform *Platform                 `json:"platform,omitempty"`
	Services []persistedServiceBinding `json:"services,omitempty"`
}

type containerStoredPart struct {
	Kind       string
	Role       string
	SnapshotID string
	Path       string
	Platform   Platform
	Services   ServiceBindings
}

func (ctr *Container) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	if ctr == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted container: nil container")
	}
	lazy := ctr.lazyOpForRouting()
	metadata, err := ctr.encodeContainerMetadata(ctx, cache)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	pending, parts, links, err := ctr.encodeContainerParts(ctx, cache, lazy)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	payload := persistedContainerPayload{
		Metadata: persistedContainerMetadata{Consumed: ctr.containerPartComputed(ctx, lazy, ContainerPartMetadata), Value: metadata},
		Parts:    parts,
	}
	if pending {
		if restore, ok := lazy.(*ContainerRestoreLazy); ok {
			lazy = restore.recipe
		}
		if lazy == nil {
			return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode pending container: missing recipe")
		}
		payload.LazyJSON, err = lazy.EncodePersisted(ctx, cache)
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		if len(payload.LazyJSON) == 0 {
			return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode pending container: empty recipe")
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted container: %w", err)
	}
	return dagql.PersistedObjectEncoding{JSON: encoded, SnapshotLinks: links}, nil
}

func containerStoredOpenGroup(part dagql.PartKey) dagql.LazyGroupKey {
	return dagql.LazyGroupKey(containerStoredOpenPrefix + string(part))
}

var _ dagql.HasLazyEvaluationReporting = (*Container)(nil)

func (ctr *Container) LazyGroupStoredPart(group dagql.LazyGroupKey) dagql.PartKey {
	if ctr == nil {
		return ""
	}
	part, ok := strings.CutPrefix(string(group), containerStoredOpenPrefix)
	if ok && hasStoredContainerPart(ctr, dagql.PartKey(part)) {
		return dagql.PartKey(part)
	}
	return ""
}

func (ctr *Container) HasPendingLazyComputation() bool {
	if ctr == nil {
		return false
	}
	lazy := ctr.lazyOpForRouting()
	if lazy == nil {
		return false
	}
	// Current settled mappings inspect plain metadata only. Metadata consumption
	// orders those reads after the metadata body; no context service is needed.
	ctx := context.Background()
	if !ctr.containerPartComputed(ctx, lazy, ContainerPartMetadata) {
		return true
	}
	for _, part := range containerSnapshotParts(ctr) {
		if !ctr.containerPartComputed(ctx, lazy, part) {
			return true
		}
	}
	return false
}

// containerPartComputed consults saved descriptors before the live recipe.
// Mapping errors leave that part pending: current runners must resolve the
// same settled mapping before consuming its body. Other parts can still have
// succeeded, such as an exec metadata copy beside an unsupported write target.
func (container *Container) containerPartComputed(ctx context.Context, lazy Lazy[*Container], part dagql.PartKey) bool {
	if _, stored := container.storedParts[part]; stored {
		return true
	}
	if lazy == nil {
		return true
	}
	if restore, ok := lazy.(*ContainerRestoreLazy); ok {
		if part == ContainerPartMetadata || restore.recipe == nil {
			return true
		}
		lazy = restore.recipe
	}
	op, ok := lazy.(LazyContainerParts)
	if !ok {
		return false
	}
	state := op.ContainerLazyState()
	if !state.GroupConsumed(ContainerLazyGroupMetadata) {
		return false
	}
	if part == ContainerPartMetadata {
		return true
	}
	groups, err := op.ContainerLazyGroups(ctx, container, []dagql.PartKey{part})
	return err == nil && len(groups) == 1 && state.GroupConsumed(groups[0])
}

// ContainerRestoreLazy is constructed only by persisted decode. Pending work
// uses the original recipe's state and bodies. Completed snapshots use that
// same state with separate opening groups, whose mapping never changes.
type ContainerRestoreLazy struct {
	*LazyState
	recipe LazyContainerParts
}

var _ LazyContainerParts = (*ContainerRestoreLazy)(nil)

func (lazy *ContainerRestoreLazy) Evaluate(ctx context.Context, ctr *Container) error {
	return ctr.evaluateAllLazyGroups(ctx, lazy)
}

func (lazy *ContainerRestoreLazy) ContainerLazyParent() dagql.ObjectResult[*Container] {
	if lazy.recipe != nil {
		return lazy.recipe.ContainerLazyParent()
	}
	return dagql.ObjectResult[*Container]{}
}

func (lazy *ContainerRestoreLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	if lazy.recipe == nil {
		return nil, nil
	}
	return lazy.recipe.AttachDependencies(ctx, attach)
}

func (lazy *ContainerRestoreLazy) EncodePersisted(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	if lazy.recipe == nil {
		return nil, nil
	}
	return lazy.recipe.EncodePersisted(ctx, cache)
}

func (lazy *ContainerRestoreLazy) ContainerLazyGroups(ctx context.Context, ctr *Container, parts []dagql.PartKey) ([]dagql.LazyGroupKey, error) {
	if parts == nil {
		parts = append([]dagql.PartKey{ContainerPartMetadata}, containerSnapshotParts(ctr)...)
	}
	groups := make([]dagql.LazyGroupKey, 0, len(parts))
	for _, part := range parts {
		var mapped []dagql.LazyGroupKey
		switch {
		case part == ContainerPartMetadata:
			mapped = []dagql.LazyGroupKey{ContainerLazyGroupMetadata}
		case hasStoredContainerPart(ctr, part):
			mapped = []dagql.LazyGroupKey{containerStoredOpenGroup(part)}
		case lazy.recipe != nil:
			var err error
			mapped, err = lazy.recipe.ContainerLazyGroups(ctx, ctr, []dagql.PartKey{part})
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("container part %q has no stored value or recipe", part)
		}
		for _, group := range mapped {
			if !slices.Contains(groups, group) {
				groups = append(groups, group)
			}
		}
	}
	return groups, nil
}

func hasStoredContainerPart(ctr *Container, part dagql.PartKey) bool {
	_, ok := ctr.storedParts[part]
	return ok
}

// containerPartValue describes the container-owned value, without evaluating
// an accessor or opening a snapshot. Stored descriptors remain authoritative
// after opening, and after the restore operation has been cleared.
func (ctr *Container) containerPartValue(part dagql.PartKey) (containerStoredPart, error) {
	if stored, ok := ctr.storedParts[part]; ok {
		return stored, nil
	}
	var value containerStoredPart
	var dir *Directory
	var file *File
	var snapshot bkcache.ImmutableRef
	switch part {
	case ContainerPartFS:
		if ctr.FS != nil {
			dir, _ = ctr.FS.Peek()
		}
		value.Kind, value.Role = containerPartDirectory, "fs"
		if dir == nil {
			return containerStoredPart{Kind: containerPartAbsent}, nil
		}
	case ContainerPartExecMeta:
		if ctr.MetaSnapshot != nil {
			snapshot, _ = ctr.MetaSnapshot.Peek()
		}
		value.Kind, value.Role = containerPartSnapshot, "meta"
		if snapshot == nil {
			return containerStoredPart{Kind: containerPartAbsent}, nil
		}
	default:
		target, ok := strings.CutPrefix(string(part), containerPartMountPrefix)
		if !ok {
			return value, fmt.Errorf("unknown container part %q", part)
		}
		found := false
		for i, mnt := range ctr.Mounts {
			if mnt.Target != target {
				continue
			}
			found = true
			switch {
			case mnt.DirectorySource != nil:
				dir, _ = mnt.DirectorySource.Peek()
				value.Kind, value.Role = containerPartDirectory, fmt.Sprintf("mount_dir:%d", i)
			case mnt.FileSource != nil:
				file, _ = mnt.FileSource.Peek()
				value.Kind, value.Role = containerPartFile, fmt.Sprintf("mount_file:%d", i)
			}
			break
		}
		if !found || dir == nil && file == nil {
			return value, fmt.Errorf("completed container mount part %q has no value", part)
		}
	}
	if dir != nil {
		value.Platform, value.Services = dir.Platform, dir.Services
		if dir.Dir != nil {
			value.Path, _ = dir.Dir.Peek()
		}
		if dir.Snapshot != nil {
			snapshot, _ = dir.Snapshot.Peek()
		}
	}
	if file != nil {
		value.Platform, value.Services = file.Platform, file.Services
		if file.File != nil {
			value.Path, _ = file.File.Peek()
		}
		if file.Snapshot != nil {
			snapshot, _ = file.Snapshot.Peek()
		}
	}
	if snapshot == nil || snapshot.SnapshotID() == "" {
		return value, fmt.Errorf("completed container part %q has no snapshot", part)
	}
	value.SnapshotID = snapshot.SnapshotID()
	return value, nil
}

func (ctr *Container) encodeContainerParts(ctx context.Context, cache dagql.PersistedObjectCache, lazy Lazy[*Container]) (bool, map[dagql.PartKey]persistedContainerPart, []dagql.PersistedSnapshotRefLink, error) {
	parts := make(map[dagql.PartKey]persistedContainerPart)
	if !ctr.containerPartComputed(ctx, lazy, ContainerPartMetadata) {
		return true, parts, nil, nil
	}
	pending := false
	var links []dagql.PersistedSnapshotRefLink
	for _, part := range containerSnapshotParts(ctr) {
		if !ctr.containerPartComputed(ctx, lazy, part) {
			parts[part] = persistedContainerPart{Kind: containerPartPending}
			pending = true
			continue
		}
		value, err := ctr.containerPartValue(part)
		if err != nil {
			return false, nil, nil, err
		}
		services, err := encodePersistedServiceBindings(cache, "container part", value.Services)
		if err != nil {
			return false, nil, nil, err
		}
		record := persistedContainerPart{Kind: value.Kind, Role: value.Role, Path: value.Path, Services: services}
		if value.Platform.OS != "" {
			record.Platform = &value.Platform
		}
		parts[part] = record
		if value.Kind != containerPartAbsent {
			if value.SnapshotID == "" || value.Role == "" {
				return false, nil, nil, fmt.Errorf("completed container part %q has no snapshot link", part)
			}
			links = append(links, dagql.PersistedSnapshotRefLink{RefKey: value.SnapshotID, Role: value.Role})
		}
	}
	return pending, parts, links, nil
}

// installContainerParts runs before publication. The encoder derives uniform
// joint completion from shared group consumption at quiescent flush. Decode
// maps completed entries only; pending targets may have an ordinary demand
// error, and remain attached to their original recipe.
func (ctr *Container) installContainerParts(ctx context.Context, dag *dagql.Server, metadataConsumed bool, parts map[dagql.PartKey]persistedContainerPart, links []dagql.PersistedSnapshotRefLink, recipe Lazy[*Container]) error {
	if !metadataConsumed {
		if len(parts) != 0 {
			return fmt.Errorf("container with pending metadata has snapshot part records")
		}
		if recipe == nil {
			return fmt.Errorf("container with pending metadata has no recipe")
		}
		ctr.Lazy = recipe
		return nil
	}
	var refined LazyContainerParts
	state := NewLazyState()
	statePtr := &state
	if recipe != nil {
		var ok bool
		refined, ok = recipe.(LazyContainerParts)
		if !ok {
			return fmt.Errorf("container with completed metadata has unrefined recipe %T", recipe)
		}
		statePtr = refined.ContainerLazyState()
	}
	statePtr.seedConsumedGroups(ContainerLazyGroupMetadata)
	expected := containerSnapshotParts(ctr)
	if len(parts) != len(expected) {
		return fmt.Errorf("container part records do not match metadata")
	}
	byRole := make(map[string]string, len(links))
	for _, link := range links {
		if old, found := byRole[link.Role]; found && old != link.RefKey {
			return fmt.Errorf("container snapshot role %q has conflicting identities", link.Role)
		}
		byRole[link.Role] = link.RefKey
	}
	ctr.storedParts = make(map[dagql.PartKey]containerStoredPart, len(parts))
	needsOp := false
	for _, part := range expected {
		record, found := parts[part]
		if !found {
			return fmt.Errorf("missing container part %q", part)
		}
		if record.Kind == containerPartPending {
			if refined == nil {
				return fmt.Errorf("pending container part %q has no recipe", part)
			}
			needsOp = true
			continue
		}
		kind, role := containerPartSnapshot, "meta"
		switch part {
		case ContainerPartFS:
			kind, role = containerPartDirectory, "fs"
		case ContainerPartExecMeta:
		default:
			for i, mnt := range ctr.Mounts {
				if ContainerPartMount(mnt.Target) != part {
					continue
				}
				if mnt.DirectorySource != nil {
					kind, role = containerPartDirectory, fmt.Sprintf("mount_dir:%d", i)
				} else {
					kind, role = containerPartFile, fmt.Sprintf("mount_file:%d", i)
				}
				break
			}
		}
		value := containerStoredPart{Kind: record.Kind}
		if record.Kind == containerPartAbsent {
			if part != ContainerPartFS && part != ContainerPartExecMeta {
				return fmt.Errorf("container mount part %q cannot be absent", part)
			}
			statePtr.seedConsumedGroups(containerStoredOpenGroup(part))
		} else {
			if record.Kind != kind || record.Role != role {
				return fmt.Errorf("invalid container part %q kind %q or role %q", part, record.Kind, record.Role)
			}
			if byRole[role] == "" {
				return fmt.Errorf("container part %q has no snapshot link for role %q", part, role)
			}
			services, err := decodePersistedServiceBindings(ctx, dag, "container part", record.Services)
			if err != nil {
				return err
			}
			value.Role, value.SnapshotID = role, byRole[role]
			value.Path, value.Services = record.Path, services
			if record.Platform != nil {
				value.Platform = *record.Platform
			}
			needsOp = true
		}
		if refined != nil {
			groups, err := refined.ContainerLazyGroups(ctx, ctr, []dagql.PartKey{part})
			if err != nil {
				return fmt.Errorf("map completed container part %q: %w", part, err)
			}
			if len(groups) != 1 {
				return fmt.Errorf("completed container part %q maps to %d groups", part, len(groups))
			}
			statePtr.seedConsumedGroups(groups[0])
		}
		ctr.storedParts[part] = value
	}
	if needsOp {
		ctr.Lazy = &ContainerRestoreLazy{LazyState: statePtr, recipe: refined}
	}
	return nil
}

func (lazy *ContainerRestoreLazy) EvaluateContainerGroup(ctx context.Context, ctr *Container, group dagql.LazyGroupKey) error {
	if group == ContainerLazyGroupMetadata {
		return lazy.EvaluateGroup(ctx, "Container metadata", group, nil)
	}
	if part, opening := strings.CutPrefix(string(group), containerStoredOpenPrefix); opening {
		return lazy.EvaluateGroup(ctx, "Container stored part", group, func(ctx context.Context) error {
			return ctr.openStoredContainerPart(ctx, dagql.PartKey(part))
		})
	}
	if lazy.recipe == nil {
		return fmt.Errorf("container group %q has no recipe", group)
	}
	// The recipe already holds its group's exclusion across its body.
	return lazy.recipe.EvaluateContainerGroup(ctx, ctr, group)
}

func (ctr *Container) openStoredContainerPart(ctx context.Context, part dagql.PartKey) error {
	stored, ok := ctr.storedParts[part]
	if !ok {
		return fmt.Errorf("container part %q has no stored value", part)
	}
	if stored.Kind == containerPartAbsent {
		return nil
	}
	// Validate and construct the destination before acquiring a handle. Nothing
	// after a successful open can fail before that handle is published.
	var publish func(bkcache.ImmutableRef)
	switch stored.Kind {
	case containerPartSnapshot:
		if part != ContainerPartExecMeta || ctr.MetaSnapshot == nil {
			return fmt.Errorf("invalid stored snapshot part %q", part)
		}
		publish = ctr.MetaSnapshot.setValue
	case containerPartDirectory:
		dest := ctr.FS
		if part != ContainerPartFS {
			target, ok := strings.CutPrefix(string(part), containerPartMountPrefix)
			mnt := ctr.mountAt(target)
			if !ok || mnt == nil {
				return fmt.Errorf("invalid stored directory part %q", part)
			}
			dest = mnt.DirectorySource
		}
		if dest == nil {
			return fmt.Errorf("missing directory accessor for stored part %q", part)
		}
		dir := &Directory{
			Dir:      new(LazyAccessor[string, *Directory]),
			Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]),
			Platform: stored.Platform, Services: slices.Clone(stored.Services),
		}
		if stored.Path != "" {
			dir.Dir.setValue(stored.Path)
		}
		publish = func(ref bkcache.ImmutableRef) {
			dir.Snapshot.setValue(ref)
			dest.setValue(dir)
		}
	case containerPartFile:
		target, ok := strings.CutPrefix(string(part), containerPartMountPrefix)
		mnt := ctr.mountAt(target)
		if !ok || mnt == nil || mnt.FileSource == nil {
			return fmt.Errorf("invalid stored file part %q", part)
		}
		file := &File{
			File:     new(LazyAccessor[string, *File]),
			Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]),
			Platform: stored.Platform, Services: slices.Clone(stored.Services),
		}
		if stored.Path != "" {
			file.File.setValue(stored.Path)
		}
		publish = func(ref bkcache.ImmutableRef) {
			file.Snapshot.setValue(ref)
			mnt.FileSource.setValue(file)
		}
	default:
		return fmt.Errorf("invalid stored container part %q kind %q", part, stored.Kind)
	}
	if stored.SnapshotID == "" {
		return fmt.Errorf("stored container part %q has no snapshot", part)
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	ref, err := query.SnapshotManager().GetBySnapshotID(ctx, stored.SnapshotID, bkcache.NoUpdateLastUsed)
	if err != nil {
		return fmt.Errorf("open stored container part %q: %w", part, err)
	}
	publish(ref)
	ctr.recordPartDiagnostic("storedOpen", part)
	return nil
}
