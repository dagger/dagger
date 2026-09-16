package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/dagger/dagger/dagql"
)

const transferPending = "transfer_pending"
const foreignUninitialized = "foreign_uninitialized"
const nativeBacking = "native"

// foreignFamilyCodec reads only persisted data. Producer JSON stays raw and
// reference validation uses the same registered visitors as local persistence.
type foreignFamilyCodec string

func readForeignPayload(raw json.RawMessage, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing payload data")
	}
	return nil
}
func (family foreignFamilyCodec) NormalizeForeign(v dagql.PersistedPayloadVisit) (dagql.ForeignPayload, error) {
	var payload any
	switch family {
	case "File":
		var p persistedFilePayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return dagql.ForeignPayload{}, err
		}
		if p.Form != transferPending {
			p.ProducerState = transferProducerState(p.Form == persistedFileFormSnapshot, p.LazyKind != "")
			p.ValueKnown = p.ValueKnown || p.Form == persistedFileFormSnapshot
			p.Form = transferPending
		}
		payload = p
	case "Directory":
		var p persistedDirectoryPayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return dagql.ForeignPayload{}, err
		}
		if p.Form != transferPending {
			p.ProducerState = transferProducerState(p.Form == persistedDirectoryFormSnapshot, p.LazyKind != "")
			p.ValueKnown = p.ValueKnown || p.Form == persistedDirectoryFormSnapshot
			p.Form = transferPending
		}
		payload = p
	case "Container":
		var p persistedContainerPayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return dagql.ForeignPayload{}, err
		}
		if p.ProducerState == "" {
			completed := p.Metadata.Consumed
			for _, part := range p.Parts {
				completed = completed && part.Kind != containerPartPending
			}
			p.ProducerState = transferProducerState(completed, len(p.LazyJSON) != 0)
		}
		for key, part := range p.Parts {
			switch part.Kind {
			case containerPartDirectory, containerPartFile, containerPartSnapshot:
				part.ValueKind, part.Kind = part.Kind, containerPartPending
			}
			p.Parts[key] = part
		}
		payload = p
	case "HTTPState":
		var p persistedHTTPStatePayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return dagql.ForeignPayload{}, err
		}
		p.Form, p.ETag, p.LastModified, p.ContentDigest = foreignUninitialized, "", "", ""
		payload = p
	case "CacheVolume":
		var p persistedCacheVolumePayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return dagql.ForeignPayload{}, err
		}
		p.Form = foreignUninitialized
		payload = p
	case "ClientFilesyncMirror":
		var p persistedClientFilesyncMirrorPayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return dagql.ForeignPayload{}, err
		}
		p.Form = foreignUninitialized
		payload = p
	case "RemoteGitMirror":
		var p persistedRemoteGitMirrorPayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return dagql.ForeignPayload{}, err
		}
		p.Form = foreignUninitialized
		payload = p
	case "ModuleSource":
		var p persistedModuleSourcePayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return dagql.ForeignPayload{}, err
		}
		if p.Kind == ModuleSourceKindLocal {
			if p.Local == nil {
				return dagql.ForeignPayload{}, fmt.Errorf("local module source has no local payload")
			}
			p.Local.Foreign = true
		}
		payload = p
	default:
		return dagql.ForeignPayload{}, fmt.Errorf("unknown foreign family %q", family)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return dagql.ForeignPayload{}, err
	}
	normalized := v
	normalized.Payload, normalized.SnapshotLinks = raw, nil
	if err := family.ValidateForeign(normalized); err != nil {
		return dagql.ForeignPayload{}, err
	}
	return dagql.ForeignPayload{JSON: raw}, nil
}
func transferProducerState(completed, hasProducer bool) string {
	if !hasProducer {
		return "none"
	}
	if completed {
		return "completed"
	}
	return "pending"
}
func validateTransferProducer(state, kind string, raw json.RawMessage) error {
	switch state {
	case "none":
		if kind != "" || len(raw) != 0 {
			return fmt.Errorf("producer state none carries a producer")
		}
	case "pending", "completed":
		if kind == "" || len(raw) == 0 || !json.Valid(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("producer state %s requires a valid kind and payload", state)
		}
	default:
		return fmt.Errorf("invalid producer state %q", state)
	}
	return nil
}

//nolint:gocyclo // one check per foreign family and reference kind; splitting hides the order of the checks
func (family foreignFamilyCodec) ValidateForeign(v dagql.PersistedPayloadVisit) error {
	if len(v.SnapshotLinks) != 0 {
		return fmt.Errorf("foreign %s carries local storage links", family)
	}
	switch family {
	case "File":
		var p persistedFilePayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return err
		}
		if p.Form != transferPending {
			return fmt.Errorf("foreign file requires transfer_pending")
		}
		if err := validateTransferProducer(p.ProducerState, p.LazyKind, p.LazyJSON); err != nil {
			return err
		}
	case "Directory":
		var p persistedDirectoryPayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return err
		}
		if p.Form != transferPending {
			return fmt.Errorf("foreign directory requires transfer_pending")
		}
		if err := validateTransferProducer(p.ProducerState, p.LazyKind, p.LazyJSON); err != nil {
			return err
		}
	case "Container":
		var p persistedContainerPayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return err
		}
		kind := ""
		if len(p.LazyJSON) > 0 && v.Call != nil {
			kind = v.Call.Field
		}
		if err := validateTransferProducer(p.ProducerState, kind, p.LazyJSON); err != nil {
			return err
		}
		if !p.Metadata.Consumed && p.ProducerState == "none" {
			return fmt.Errorf("pending metadata requires producer")
		}
		for key, part := range p.Parts {
			if part.Kind != containerPartPending && part.Kind != containerPartAbsent {
				return fmt.Errorf("foreign container part %s has native kind %q", key, part.Kind)
			}
			if part.Kind == containerPartAbsent {
				if part.Role != "" || part.ValueKind != "" || part.Path != "" || part.Platform != nil || len(part.Services) != 0 {
					return fmt.Errorf("absent part %s has a descriptor", key)
				}
			} else if part.ValueKind != "" && part.ValueKind != containerPartDirectory && part.ValueKind != containerPartFile && part.ValueKind != containerPartSnapshot {
				return fmt.Errorf("invalid part value kind %q", part.ValueKind)
			}
		}
		if _, err := mapContainerTransferParts(v, p); err != nil {
			return err
		}
	case "HTTPState":
		var p persistedHTTPStatePayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return err
		}
		if p.Form != foreignUninitialized || p.ETag != "" || p.LastModified != "" || p.ContentDigest != "" {
			return fmt.Errorf("foreign HTTP state must be uninitialized without validators or digest")
		}
	case "CacheVolume":
		var p persistedCacheVolumePayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return err
		}
		if p.Form != foreignUninitialized {
			return fmt.Errorf("foreign cache volume requires foreign_uninitialized")
		}
	case "ClientFilesyncMirror":
		var p persistedClientFilesyncMirrorPayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return err
		}
		if p.Form != foreignUninitialized {
			return fmt.Errorf("foreign filesync mirror requires foreign_uninitialized")
		}
	case "RemoteGitMirror":
		var p persistedRemoteGitMirrorPayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return err
		}
		if p.Form != foreignUninitialized {
			return fmt.Errorf("foreign Git mirror requires foreign_uninitialized")
		}
	case "ModuleSource":
		var p persistedModuleSourcePayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return err
		}
		switch p.Kind {
		case ModuleSourceKindLocal:
			if p.Local == nil || !p.Local.Foreign {
				return fmt.Errorf("foreign local module source requires Foreign")
			}
		case ModuleSourceKindGit, ModuleSourceKindDir: // Existing non-host source forms.
		default:
			return fmt.Errorf("unknown module source kind %q", p.Kind)
		}
	default:
		return fmt.Errorf("unknown foreign family %q", family)
	}
	registered, ok := dagql.PersistedObjectFamilyByName("core." + string(family))
	if !ok {
		return fmt.Errorf("unknown family %s", family)
	}
	_, err := registered.Visitor.VisitPersistedReferences(v, func(*dagql.PersistedRef) error { return nil })
	return err
}

func transferSnapshotValue(kind, path string, platform *Platform, services []persistedServiceBinding) *dagql.SnapshotValue {
	value := &dagql.SnapshotValue{Kind: kind, Path: path}
	if platform != nil {
		p := platform.Spec()
		value.Platform = &p
	}
	for _, service := range services {
		value.Services = append(value.Services, dagql.TransferredServiceBinding{ServiceResultID: service.ServiceResultID, Hostname: service.Hostname, Aliases: slices.Clone(service.Aliases)})
	}
	return value
}
func capturedSnapshotID(v dagql.PersistedPayloadVisit, role string) (string, error) {
	for _, link := range v.SnapshotLinks {
		if link.Role == role {
			if link.RefKey == "" {
				break
			}
			return link.RefKey, nil
		}
	}
	return "", fmt.Errorf("completed output missing snapshot role %q", role)
}
func (family foreignFamilyCodec) MapSnapshotParts(v dagql.PersistedPayloadVisit) ([]dagql.CapturedCodecOutput, error) {
	out := dagql.CapturedCodecOutput{Address: dagql.PersistedPartAddress{OutputPath: v.Path, Part: "snapshot"}, State: "pending", Role: "snapshot"}
	switch family {
	case "File":
		out.ValueKind = "file"
		var p persistedFilePayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return nil, err
		}
		if p.Form == persistedFileFormSnapshot || p.ValueKnown {
			out.Value = transferSnapshotValue("file", p.File, &p.Platform, p.Services)
		}
		if p.Form == persistedFileFormSnapshot {
			out.State = "completed"
		}
	case "Directory":
		out.ValueKind = "directory"
		var p persistedDirectoryPayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return nil, err
		}
		if p.Form == persistedDirectoryFormSnapshot || p.ValueKnown {
			out.Value = transferSnapshotValue("directory", p.Dir, &p.Platform, p.Services)
		}
		if p.Form == persistedDirectoryFormSnapshot {
			out.State = "completed"
		}
	case "Container":
		var p persistedContainerPayload
		if err := readForeignPayload(v.Payload, &p); err != nil {
			return nil, err
		}
		return mapContainerTransferParts(v, p)
	default:
		return nil, nil
	}
	if out.State == "completed" {
		var err error
		out.SnapshotID, err = capturedSnapshotID(v, out.Role)
		if err != nil {
			return nil, err
		}
	}
	return []dagql.CapturedCodecOutput{out}, nil
}
func mapContainerTransferParts(v dagql.PersistedPayloadVisit, p persistedContainerPayload) ([]dagql.CapturedCodecOutput, error) {
	metadata := "pending"
	if p.Metadata.Consumed {
		metadata = "metadata"
	}
	outputs := []dagql.CapturedCodecOutput{{Address: dagql.PersistedPartAddress{OutputPath: v.Path, Part: ContainerPartMetadata}, State: metadata}}
	expected := map[dagql.PartKey]struct{ kind, role string }{ContainerPartFS: {containerPartDirectory, "fs"}, ContainerPartExecMeta: {containerPartSnapshot, "meta"}}
	targets := map[string]bool{}
	for i, mount := range p.Metadata.Value.Mounts {
		if targets[mount.Target] {
			return nil, fmt.Errorf("duplicate mount target %q", mount.Target)
		}
		targets[mount.Target] = true
		key := dagql.PartKey("mount:" + mount.Target)
		switch mount.Kind {
		case persistedContainerMountKindDirectory:
			expected[key] = struct{ kind, role string }{containerPartDirectory, fmt.Sprintf("mount_dir:%d", i)}
		case persistedContainerMountKindFile:
			expected[key] = struct{ kind, role string }{containerPartFile, fmt.Sprintf("mount_file:%d", i)}
		}
	}
	for key, part := range p.Parts {
		want, ok := expected[key]
		if !ok {
			return nil, fmt.Errorf("unknown container part %q", key)
		}
		out := dagql.CapturedCodecOutput{Address: dagql.PersistedPartAddress{OutputPath: v.Path, Part: key}, State: part.Kind, ValueKind: want.kind, Role: part.Role}
		kind := part.Kind
		if kind == containerPartPending {
			kind = part.ValueKind
		}
		if kind != "" && kind != containerPartAbsent {
			if kind != want.kind || part.Role != want.role {
				return nil, fmt.Errorf("container part %s descriptor does not match its recorded mount/role", key)
			}
			out.Value = transferSnapshotValue(kind, part.Path, part.Platform, part.Services)
		}
		switch part.Kind {
		case containerPartPending, containerPartAbsent:
		case containerPartDirectory, containerPartFile, containerPartSnapshot:
			out.State = "completed"
			var err error
			out.SnapshotID, err = capturedSnapshotID(v, part.Role)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("invalid container part kind %q", part.Kind)
		}
		outputs = append(outputs, out)
	}
	// Mount descriptors are meaningful only with their captured ordered metadata.
	for key, want := range expected {
		if _, ok := p.Parts[key]; !ok {
			if p.Metadata.Consumed {
				return nil, fmt.Errorf("missing container part %s", key)
			}
			outputs = append(outputs, dagql.CapturedCodecOutput{Address: dagql.PersistedPartAddress{OutputPath: v.Path, Part: key}, State: "pending", ValueKind: want.kind, Role: want.role})
		}
	}
	slices.SortFunc(outputs, func(a, b dagql.CapturedCodecOutput) int {
		return strings.Compare(string(a.Address.Part), string(b.Address.Part))
	})
	return outputs, nil
}

func persistedBackingForm(foreign, initialized bool) string {
	if foreign && !initialized {
		return foreignUninitialized
	}
	return nativeBacking
}
func (container *Container) resolveTransferParts(parts []dagql.PartKey) ([]dagql.LazyGroupKey, error) {
	view := container.acquiredOutput.Load()
	if view == nil {
		return nil, fmt.Errorf("missing raw Container view")
	}
	payload := &view.Payload

	if parts == nil {
		parts = []dagql.PartKey{ContainerPartMetadata}
		for part := range payload.Parts {
			parts = append(parts, part)
		}
	}
	for _, part := range parts {
		if part == ContainerPartMetadata && payload.Metadata.Consumed {
			continue
		}
		if descriptor, ok := payload.Parts[part]; ok && descriptor.Kind != containerPartPending {
			continue
		}
		return nil, fmt.Errorf("%w: Container.%s", dagql.ErrUnavailablePart, part)
	}
	return nil, nil
}
func (container *Container) setAbsentTransferPart(part dagql.PartKey) {
	switch part {
	case ContainerPartFS:
		container.FS.setValue(nil)
	case ContainerPartExecMeta:
		container.MetaSnapshot.setValue(nil)
	default:
		target, _ := strings.CutPrefix(string(part), "mount:")
		for _, mount := range container.Mounts {
			if mount.Target == target {
				if mount.DirectorySource != nil {
					mount.DirectorySource.setValue(nil)
				}
				if mount.FileSource != nil {
					mount.FileSource.setValue(nil)
				}
			}
		}
	}
}

// ValidateSnapshotScope checks local role coverage before a generic visitor
// relocates storage keys. Foreign pending descriptors carry no local roles.
func (family foreignFamilyCodec) ValidateSnapshotScope(v dagql.PersistedPayloadVisit) error {
	switch family {
	case "File", "Directory", "Container":
	default:
		return nil
	}
	outputs, err := family.MapSnapshotParts(v)
	if err != nil {
		return err
	}
	expected := map[string]bool{}
	for _, output := range outputs {
		if output.State == "completed" {
			expected[output.Role] = true
		}
	}
	for _, link := range v.SnapshotLinks {
		if !expected[link.Role] {
			return fmt.Errorf("undeclared snapshot role %q", link.Role)
		}
		delete(expected, link.Role)
	}
	if len(expected) != 0 {
		return fmt.Errorf("completed descriptor is missing snapshot roles")
	}
	return nil
}
