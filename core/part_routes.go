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
	router, err := family.newPartRouter(v)
	if err != nil {
		return dagql.LazyOperationRoute{Group: dagql.LazyGroupAddress{OutputPath: slices.Clone(v.Path)}}, err
	}
	return router.route(demand, true)
}

// partRouter routes demands on one decoded payload. A probe routes every
// output of its payload, so the payload is decoded once, and the Container
// routing metadata and each part's groups are derived once, not per demand.
// Every derived value is a pure function of the payload, so a route is the
// same whether or not other demands were routed before it.
type partRouter struct {
	family  foreignFamilyCodec
	v       dagql.PersistedPayloadVisit
	payload *partPayload

	routing *containerRouting
	groups  map[dagql.PartKey]containerPartGroups
	// writeSetErr is the first error mapping any part to its groups, in part
	// order: the error a write-set enumeration returns.
	writeSetErr     error
	writeSetChecked bool
}

type containerRouting struct {
	ctr   *Container
	parts []dagql.PartKey
	err   error
}

type containerPartGroups struct {
	groups []dagql.LazyGroupKey
	err    error
}

func (family foreignFamilyCodec) newPartRouter(v dagql.PersistedPayloadVisit) (*partRouter, error) {
	payload := &partPayload{}
	var err error
	switch family {
	case "Directory":
		err = json.Unmarshal(v.Payload, &payload.directory)
	case "File":
		err = json.Unmarshal(v.Payload, &payload.file)
	case "Container":
		err = json.Unmarshal(v.Payload, &payload.container)
	default:
		err = fmt.Errorf("no part operation for %s", family)
	}
	if err != nil {
		return nil, err
	}
	return &partRouter{family: family, v: v, payload: payload}, nil
}

// route routes demand. Without withWriteSet the route's write set is left
// empty, but every error enumerating it is still returned.
func (router *partRouter) route(demand dagql.PartKey, withWriteSet bool) (dagql.LazyOperationRoute, error) {
	v := router.v
	route := dagql.LazyOperationRoute{Group: dagql.LazyGroupAddress{OutputPath: slices.Clone(v.Path)}}
	add := func(part dagql.PartKey) {
		if withWriteSet {
			route.WriteSet = append(route.WriteSet, dagql.PersistedPartAddress{OutputPath: slices.Clone(v.Path), Part: part})
		}
	}
	switch router.family {
	case "Directory":
		p := &router.payload.directory
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
		p := &router.payload.file
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
		p := &router.payload.container
		if len(p.LazyJSON) == 0 {
			delegation, err := containerPartDelegation(v, *p, demand)
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
		routing := router.containerRouting()
		if routing.err != nil {
			return route, routing.err
		}
		if field == "_builtinContainer" || field == "import" {
			route.Group.Group = dagql.LazyGroupWhole
			for _, part := range routing.parts {
				add(part)
			}
			return route, nil
		}
		if demand != ContainerPartMetadata && !p.Metadata.Consumed {
			route.NeedsMetadata = true
			return route, nil
		}
		demanded := router.partGroups(demand)
		if demanded.err != nil {
			return route, demanded.err
		}
		if len(demanded.groups) != 1 {
			return route, fmt.Errorf("Container lazy: part maps to %d groups", len(demanded.groups))
		}
		route.Group.Group = demanded.groups[0]
		if demand == ContainerPartMetadata {
			add(demand)
			return route, nil
		}
		if !withWriteSet {
			return route, router.writeSetError()
		}
		for _, part := range routing.parts {
			mapped := router.partGroups(part)
			if mapped.err != nil {
				return route, mapped.err
			}
			if slices.Contains(mapped.groups, route.Group.Group) {
				add(part)
			}
		}
	default:
		return route, fmt.Errorf("no part operation for %s", router.family)
	}
	return route, nil
}

// containerRouting returns the Container's routing metadata and its parts,
// metadata first.
func (router *partRouter) containerRouting() *containerRouting {
	if router.routing == nil {
		routing := &containerRouting{}
		routing.ctr, routing.err = containerRoutingMetadata(router.payload.container.Metadata.Value)
		if routing.err == nil {
			routing.parts = append([]dagql.PartKey{ContainerPartMetadata}, containerSnapshotParts(routing.ctr)...)
		}
		router.routing = routing
	}
	return router.routing
}

// partGroups returns the groups part maps to under the Container's recorded
// operation. The returned groups are shared and must not be modified.
func (router *partRouter) partGroups(part dagql.PartKey) containerPartGroups {
	if mapped, ok := router.groups[part]; ok {
		return mapped
	}
	if router.groups == nil {
		router.groups = make(map[dagql.PartKey]containerPartGroups)
	}
	var mapped containerPartGroups
	mapped.groups, mapped.err = rawContainerGroups(router.v.Call.Field, router.payload.container.LazyJSON, router.containerRouting().ctr, []dagql.PartKey{part})
	router.groups[part] = mapped
	return mapped
}

// writeSetError returns the first error mapping a Container part to its
// groups, in part order: what enumerating any demand's write set returns.
func (router *partRouter) writeSetError() error {
	if !router.writeSetChecked {
		for _, part := range router.containerRouting().parts {
			if mapped := router.partGroups(part); mapped.err != nil {
				router.writeSetErr = mapped.err
				break
			}
		}
		router.writeSetChecked = true
	}
	return router.writeSetErr
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
