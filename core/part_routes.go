package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql"
)

// This closed inventory describes already captured metadata effects. No
// arguments are interpreted, and no operation or recipe is reconstructed.
func containerPartDelegation(v dagql.PersistedPayloadVisit, p persistedContainerPayload, demand dagql.PartKey) (*dagql.PartDelegation, error) {
	if len(p.LazyJSON) != 0 || !p.Metadata.Consumed || demand == ContainerPartMetadata {
		return nil, nil
	}
	if v.Call == nil {
		return nil, nil
	}
	switch v.Call.Field {
	case "withWorkdir", "withEnvVariable", "withoutDefaultArgs", "__withSystemEnvVariable", "withEntrypoint", "withMountedCache", "withoutMount", "withUnixSocket", "withoutUnixSocket", "withoutEnvVariable":
	default:
		return nil, nil
	}
	if v.Call.Kind != dagql.ResultCallKindField || v.Call.Module != nil || v.Call.Nth != 0 {
		return nil, nil
	}
	if v.Call.Type == nil || v.Call.Type.NamedType != "Container" || v.Call.Type.Elem != nil {
		return nil, fmt.Errorf("Container delegation: invalid result type")
	}
	if v.Call.Receiver == nil || v.Call.Receiver.ResultID == 0 || v.Call.Receiver.Call != nil {
		return nil, fmt.Errorf("Container delegation: missing exact receiver")
	}
	outputs, err := mapContainerTransferParts(v, p)
	if err != nil {
		return nil, err
	}
	for _, output := range outputs {
		if output.Address.Part == demand {
			if output.State == containerPartAbsent {
				return nil, nil
			}
			return &dagql.PartDelegation{ParentResultID: v.Call.Receiver.ResultID, Address: dagql.PersistedPartAddress{Part: demand}}, nil
		}
	}
	return nil, fmt.Errorf("Container delegation: undeclared part %q", demand)
}

// RouteParts describes the saved operation without resolving references or
// constructing an operational lazy state. Mount keys are always target paths.
func (family foreignFamilyCodec) RouteParts(v dagql.PersistedPayloadVisit, demand dagql.PartKey) (dagql.LazyOperationRoute, error) {
	route := dagql.LazyOperationRoute{Group: dagql.LazyGroupAddress{OutputPath: slices.Clone(v.Path)}}
	add := func(part dagql.PartKey) {
		route.WriteSet = append(route.WriteSet, dagql.PersistedPartAddress{OutputPath: slices.Clone(v.Path), Part: part})
	}
	switch family {
	case "Directory":
		var p persistedDirectoryPayload
		if err := json.Unmarshal(v.Payload, &p); err != nil {
			return route, err
		}
		if p.LazyKind == "" {
			return route, nil
		}
		if _, ok := persistedDirectoryLazyVisitors[p.LazyKind]; !ok {
			return route, fmt.Errorf("unknown Directory operation %q", p.LazyKind)
		}
		if demand != "snapshot" {
			return route, fmt.Errorf("unknown Directory part %q", demand)
		}
		route.HasLazyOperation = true
		route.Group.Group = dagql.LazyGroupWhole
		add("snapshot")
	case "File":
		var p persistedFilePayload
		if err := json.Unmarshal(v.Payload, &p); err != nil {
			return route, err
		}
		if p.LazyKind == "" {
			return route, nil
		}
		if _, ok := persistedFileLazyVisitors[p.LazyKind]; !ok {
			return route, fmt.Errorf("unknown File operation %q", p.LazyKind)
		}
		if demand != "snapshot" {
			return route, fmt.Errorf("unknown File part %q", demand)
		}
		route.HasLazyOperation = true
		route.Group.Group = dagql.LazyGroupWhole
		add("snapshot")
	case "Container":
		var p persistedContainerPayload
		if err := json.Unmarshal(v.Payload, &p); err != nil {
			return route, err
		}
		if len(p.LazyJSON) == 0 {
			delegation, err := containerPartDelegation(v, p, demand)
			route.Delegation = delegation
			return route, err
		}
		if v.Call == nil {
			return route, fmt.Errorf("Container lazy: missing recorded call")
		}
		field := v.Call.Field
		if _, ok := persistedContainerRecipeVisitors[field]; !ok {
			return route, fmt.Errorf("unknown Container operation %q", field)
		}
		route.HasLazyOperation = true
		ctr, err := containerRoutingMetadata(p.Metadata.Value)
		if err != nil {
			return route, err
		}
		parts := append([]dagql.PartKey{ContainerPartMetadata}, containerSnapshotParts(ctr)...)
		if field == "_builtinContainer" || field == "import" {
			route.Group.Group = dagql.LazyGroupWhole
			for _, part := range parts {
				add(part)
			}
			return route, nil
		}
		if demand != ContainerPartMetadata && !p.Metadata.Consumed {
			route.NeedsMetadata = true
			return route, nil
		}
		groups, err := rawContainerGroups(field, p.LazyJSON, ctr, []dagql.PartKey{demand})
		if err != nil {
			return route, err
		}
		if len(groups) != 1 {
			return route, fmt.Errorf("Container lazy: part maps to %d groups", len(groups))
		}
		route.Group.Group = groups[0]
		if demand == ContainerPartMetadata {
			add(demand)
			return route, nil
		}
		for _, part := range parts {
			groups, err := rawContainerGroups(field, p.LazyJSON, ctr, []dagql.PartKey{part})
			if err != nil {
				return route, err
			}
			if slices.Contains(groups, route.Group.Group) {
				add(part)
			}
		}
	default:
		return route, fmt.Errorf("no part operation for %s", family)
	}
	return route, nil
}

// Only scalar topology is materialized; handles and services remain encoded.
func containerRoutingMetadata(p persistedContainerMetadataValue) (*Container, error) {
	ctr := &Container{Config: p.Config, Platform: p.Platform}
	for _, m := range p.Mounts {
		mount := ContainerMount{Target: m.Target, Readonly: m.Readonly}
		switch m.Kind {
		case persistedContainerMountKindDirectory:
			mount.DirectorySource = new(LazyAccessor[*Directory, *Container])
		case persistedContainerMountKindFile:
			mount.FileSource = new(LazyAccessor[*File, *Container])
		case persistedContainerMountKindCache:
			mount.CacheSource = new(CacheMountSource)
		case persistedContainerMountKindVolume:
			mount.VolumeSource = new(VolumeMountSource)
		case persistedContainerMountKindTmpfs:
			mount.TmpfsSource = new(TmpfsMountSource)
		default:
			return nil, fmt.Errorf("unknown mount kind %q", m.Kind)
		}
		ctr.Mounts = append(ctr.Mounts, mount)
	}
	return ctr, nil
}

func rawContainerGroups(field string, raw json.RawMessage, ctr *Container, parts []dagql.PartKey) ([]dagql.LazyGroupKey, error) {
	// These existing mapping helpers are pure and require no lazy latch or inputs.
	switch field {
	case "withExec":
		var p persistedContainerExecLazy
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		if p.VolatileCacheHitParentResultID != 0 {
			return templateAContainerGroups(ctr, parts)
		}
		return (&ContainerExecLazy{}).ContainerLazyGroups(context.Background(), ctr, parts)
	case "from", "withRootfs":
		return (&ContainerFromImageRefLazy{}).ContainerLazyGroups(context.Background(), ctr, parts)
	case "withMountedDirectory", "withMountedFile", "__withMountedPathDockerfileCompat":
		var p struct {
			Target string `json:"target"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return containerMountWriterGroups(ctr, p.Target, parts)
	case "withDirectory", "withFile", "withNewFile", "withoutDirectory", "withoutFile", "withoutFiles":
		var p struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return containerPathWriterGroups(ctr, []string{p.Path}, parts)
	case "withSymlink":
		var p persistedContainerWithSymlinkLazy
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		return containerPathWriterGroups(ctr, []string{p.LinkPath}, parts)
	default:
		return templateAContainerGroups(ctr, parts)
	}
}
