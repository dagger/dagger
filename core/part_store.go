package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

func (family foreignFamilyCodec) PreparePartRecord(receiver, source dagql.PersistedRecord, d dagql.PartDescriptor, target dagql.PersistedPartAddress) (dagql.PersistedRecord, error) {
	if len(target.OutputPath) != 0 {
		return receiver, fmt.Errorf("inline part store awaits scoped snapshot links")
	}
	role := "snapshot"
	switch family {
	case "Directory":
		if d.Absent || d.Value == nil || d.Value.Kind != "directory" || d.Value.Platform == nil {
			return receiver, fmt.Errorf("incompatible Directory output")
		}
		var p persistedDirectoryPayload
		if err := json.Unmarshal(receiver.Envelope.ObjectJSON, &p); err != nil {
			return receiver, err
		}
		p.Form = persistedDirectoryFormSnapshot
		p.ProducerState = ""
		p.ValueKnown = true
		p.Dir = d.Value.Path
		p.Platform = Platform(*d.Value.Platform)
		p.Services = partServices(d.Value)
		raw, err := json.Marshal(p)
		if err != nil {
			return receiver, err
		}
		receiver.Envelope.ObjectJSON = raw
	case "File":
		if d.Absent || d.Value == nil || d.Value.Kind != "file" || d.Value.Platform == nil {
			return receiver, fmt.Errorf("incompatible File output")
		}
		var p persistedFilePayload
		if err := json.Unmarshal(receiver.Envelope.ObjectJSON, &p); err != nil {
			return receiver, err
		}
		p.Form = persistedFileFormSnapshot
		p.ProducerState = ""
		p.ValueKnown = true
		p.File = d.Value.Path
		p.Platform = Platform(*d.Value.Platform)
		p.Services = partServices(d.Value)
		raw, err := json.Marshal(p)
		if err != nil {
			return receiver, err
		}
		receiver.Envelope.ObjectJSON = raw
	case "Container":
		var p persistedContainerPayload
		if err := json.Unmarshal(receiver.Envelope.ObjectJSON, &p); err != nil {
			return receiver, err
		}
		if p.ProducerState == "" {
			p.ProducerState = transferProducerState(false, len(p.LazyJSON) != 0)
		}
		if target.Part == ContainerPartMetadata {
			var donor persistedContainerPayload
			if err := json.Unmarshal(source.Envelope.ObjectJSON, &donor); err != nil {
				return receiver, err
			}
			if !donor.Metadata.Consumed {
				return receiver, fmt.Errorf("source metadata is pending")
			}
			p.Metadata = donor.Metadata
			// Metadata fixes the positional role mapping. Existing final outputs must
			// have a matching target/kind in that layout.
			shape, err := containerRoutingMetadata(p.Metadata.Value)
			if err != nil {
				return receiver, err
			}
			if p.Parts == nil {
				p.Parts = map[dagql.PartKey]persistedContainerPart{}
			}
			expected := containerSnapshotParts(shape)
			for key, part := range p.Parts {
				if !slices.Contains(expected, key) && part.Kind != containerPartPending {
					return receiver, fmt.Errorf("metadata removes final part %s", key)
				}
			}
			for _, key := range expected {
				if _, ok := p.Parts[key]; !ok {
					p.Parts[key] = persistedContainerPart{Kind: containerPartPending}
				}
			}
		} else {
			if !p.Metadata.Consumed {
				return receiver, fmt.Errorf("snapshot publication requires settled metadata")
			}
			mapped, err := mapContainerTransferParts(dagql.PersistedPayloadVisit{SnapshotLinks: receiver.SnapshotLinks}, p)
			if err != nil {
				return receiver, err
			}
			var want *dagql.CapturedCodecOutput
			for i := range mapped {
				if mapped[i].Address.Part == target.Part {
					want = &mapped[i]
					break
				}
			}
			if want == nil {
				return receiver, fmt.Errorf("unknown Container output %s", target.Part)
			}
			role = want.Role
			if role == "" {
				switch target.Part {
				case ContainerPartFS:
					role = "fs"
				case ContainerPartExecMeta:
					role = "meta"
				default:
					for i, m := range p.Metadata.Value.Mounts {
						if dagql.PartKey("mount:"+m.Target) == target.Part {
							if m.Kind == persistedContainerMountKindDirectory {
								role = fmt.Sprintf("mount_dir:%d", i)
							} else if m.Kind == persistedContainerMountKindFile {
								role = fmt.Sprintf("mount_file:%d", i)
							}
						}
					}
				}
			}
			part := persistedContainerPart{Kind: containerPartAbsent}
			if !d.Absent {
				if d.Value == nil || d.Value.Kind != want.ValueKind {
					return receiver, fmt.Errorf("incompatible Container part %s", target.Part)
				}
				part = persistedContainerPart{Kind: d.Value.Kind, Role: role, Path: d.Value.Path, Services: partServices(d.Value)}
				if d.Value.Platform != nil {
					p := Platform(*d.Value.Platform)
					part.Platform = &p
				}
			}
			p.Parts[target.Part] = part
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return receiver, err
		}
		receiver.Envelope.ObjectJSON = raw
	default:
		return receiver, fmt.Errorf("unsupported part family %s", family)
	}
	receiver.SnapshotLinks = slices.Clone(receiver.SnapshotLinks)
	if d.SnapshotID != "" {
		receiver.SnapshotLinks = slices.DeleteFunc(receiver.SnapshotLinks, func(l dagql.PersistedSnapshotRefLink) bool { return l.Role == role })
		receiver.SnapshotLinks = append(receiver.SnapshotLinks, dagql.PersistedSnapshotRefLink{Role: role, RefKey: d.SnapshotID})
	}
	return receiver, nil
}
func partServices(value *dagql.SnapshotValue) []persistedServiceBinding {
	var out []persistedServiceBinding
	for _, s := range value.Services {
		out = append(out, persistedServiceBinding{ServiceResultID: s.ServiceResultID, Hostname: s.Hostname, Aliases: slices.Clone(s.Aliases)})
	}
	return out
}

type filePartStore struct {
	receiver *File
	next     *File
	expected dagql.OutputRevision
	unlock   func()
}

func (s *filePartStore) TryLock() bool {
	u, err := s.receiver.lockForPersistence()
	if err != nil {
		return false
	}
	if s.receiver.OutputRev != s.expected {
		u()
		return false
	}
	s.unlock = u
	return true
}
func (s *filePartStore) Unlock() { s.unlock() }
func (s *filePartStore) Publish() {
	r, n := s.receiver, s.next
	r.File = n.File
	r.Snapshot = n.Snapshot
	r.Platform = n.Platform
	r.Services = n.Services
	r.stored = n.stored
	r.storedDiagnostics = n.storedDiagnostics
	r.completedRecipeKind = n.completedRecipeKind
	r.completedRecipeJSON = n.completedRecipeJSON
	r.completedRecipe = nil
	r.Lazy = nil
	r.transferPending = nil
	r.OutputRev++
}
func (file *File) PreparePartStore(ctx context.Context, dec *dagql.PersistDecodeContext, record dagql.PersistedRecord, d dagql.PartDescriptor, ref bkcache.ImmutableRef) (dagql.PreparedPartStore, error) {
	revision, err := file.PersistedOutputRevision()
	if err != nil {
		return nil, err
	}
	n, err := decodePersistedFileWithSnapshotRole(ctx, dec, record.Envelope.ObjectJSON, "snapshot")
	if err != nil {
		return nil, err
	}
	n.SetSnapshot(ref)
	n.Lazy = nil
	return &filePartStore{receiver: file, next: n, expected: revision}, nil
}

type directoryPartStore struct {
	receiver *Directory
	next     *Directory
	expected dagql.OutputRevision
	unlock   func()
}

func (s *directoryPartStore) TryLock() bool {
	u, err := s.receiver.lockForPersistence()
	if err != nil {
		return false
	}
	if s.receiver.OutputRev != s.expected {
		u()
		return false
	}
	s.unlock = u
	return true
}
func (s *directoryPartStore) Unlock() { s.unlock() }
func (s *directoryPartStore) Publish() {
	r, n := s.receiver, s.next
	r.Dir = n.Dir
	r.Snapshot = n.Snapshot
	r.Platform = n.Platform
	r.Services = n.Services
	r.stored = n.stored
	r.storedDiagnostics = n.storedDiagnostics
	r.completedRecipeKind = n.completedRecipeKind
	r.completedRecipeJSON = n.completedRecipeJSON
	r.completedRecipe = nil
	r.Lazy = nil
	r.transferPending = nil
	r.OutputRev++
}
func (dir *Directory) PreparePartStore(ctx context.Context, dec *dagql.PersistDecodeContext, record dagql.PersistedRecord, d dagql.PartDescriptor, ref bkcache.ImmutableRef) (dagql.PreparedPartStore, error) {
	revision, err := dir.PersistedOutputRevision()
	if err != nil {
		return nil, err
	}
	n, err := decodePersistedDirectoryWithSnapshotRole(ctx, dec, record.Envelope.ObjectJSON, "snapshot")
	if err != nil {
		return nil, err
	}
	n.SetSnapshot(ref)
	n.Lazy = nil
	return &directoryPartStore{receiver: dir, next: n, expected: revision}, nil
}

// An adapter-owned Container publishes payload, producer and all desired roles
// as one immutable value, without borrowing a native producer latch.
type containerAcquiredOutput struct {
	Payload  persistedContainerPayload
	Links    []dagql.PersistedSnapshotRefLink
	Revision dagql.OutputRevision
}

func (ctr *Container) PersistedOutputRevision() (dagql.OutputRevision, error) {
	if view := ctr.acquiredOutput.Load(); view != nil {
		return view.Revision, nil
	}
	unlock, err := ctr.lockForPersistence(false)
	if err != nil {
		return 0, err
	}
	defer unlock()
	return 0, nil
}

type containerPartStore struct {
	receiver, next *Container
	view, previous *containerAcquiredOutput
	part           dagql.PartKey
	unlock         func()
}

func (s *containerPartStore) TryLock() bool {
	if s.receiver.acquiredOutput.Load() != s.previous {
		return false
	}
	if s.previous == nil {
		u, err := s.receiver.lockForPersistence(false)
		if err != nil {
			return false
		}
		s.unlock = u
	} else {
		s.unlock = func() {}
	}
	if s.receiver.acquiredOutput.Load() != s.previous {
		s.unlock()
		return false
	}
	return true
}
func (s *containerPartStore) Unlock() { s.unlock() }
func (s *containerPartStore) Publish() {
	r, n := s.receiver, s.next
	switch s.part {
	case ContainerPartMetadata:
		r.Config = n.Config
		r.EnabledGPUs = n.EnabledGPUs
		r.Platform = n.Platform
		r.Annotations = n.Annotations
		r.Secrets = n.Secrets
		r.Sockets = n.Sockets
		r.ImageRef = n.ImageRef
		r.Ports = n.Ports
		r.Services = n.Services
		r.DefaultTerminalCmd = n.DefaultTerminalCmd
		r.SystemEnvNames = n.SystemEnvNames
		r.VolatileEnv = n.VolatileEnv
		r.DefaultArgs = n.DefaultArgs
		r.Mounts = n.Mounts
	case ContainerPartFS:
		r.FS = n.FS
	case ContainerPartExecMeta:
		r.MetaSnapshot = n.MetaSnapshot
	default:
		for i := range r.Mounts {
			if dagql.PartKey("mount:"+r.Mounts[i].Target) == s.part {
				r.Mounts[i].DirectorySource = n.Mounts[i].DirectorySource
				r.Mounts[i].FileSource = n.Mounts[i].FileSource
				break
			}
		}
	}
	r.acquiredOutput.Store(s.view)
}
func (ctr *Container) PreparePartStore(ctx context.Context, dec *dagql.PersistDecodeContext, record dagql.PersistedRecord, d dagql.PartDescriptor, ref bkcache.ImmutableRef) (dagql.PreparedPartStore, error) {
	previous := ctr.acquiredOutput.Load()
	typed, err := (*Container)(nil).DecodePersistedObject(ctx, dec, record.Envelope.ObjectJSON)
	if err != nil {
		return nil, err
	}
	next := typed.(*Container)
	view := next.acquiredOutput.Load()
	if view == nil {
		return nil, fmt.Errorf("Container store requires raw representation")
	}
	if previous != nil {
		view.Revision = previous.Revision + 1
	}
	if d.Address.Part != ContainerPartMetadata && !d.Absent {
		if err := next.assignAcquiredRef(ctx, dec, d.Address.Part, ref); err != nil {
			return nil, err
		}
	}
	return &containerPartStore{receiver: ctr, next: next, view: view, previous: previous, part: d.Address.Part}, nil
}

// assignAcquiredRef operates only on an unpublished prepared value.
func (ctr *Container) assignAcquiredRef(ctx context.Context, dec *dagql.PersistDecodeContext, key dagql.PartKey, ref bkcache.ImmutableRef) error {
	p := ctr.acquiredOutput.Load().Payload.Parts[key]
	services, err := decodePersistedServiceBindings(ctx, dec, "acquired Container part", p.Services)
	if err != nil {
		return err
	}
	var dir *Directory
	var file *File
	switch p.Kind {
	case containerPartDirectory:
		dir = &Directory{Dir: new(LazyAccessor[string, *Directory]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *Directory]), Services: services}
		if p.Platform != nil {
			dir.Platform = *p.Platform
		}
		dir.SetPath(p.Path)
		dir.SetSnapshot(ref)
	case containerPartFile:
		file = &File{File: new(LazyAccessor[string, *File]), Snapshot: new(LazyAccessor[bkcache.ImmutableRef, *File]), Services: services}
		if p.Platform != nil {
			file.Platform = *p.Platform
		}
		file.SetPath(p.Path)
		file.SetSnapshot(ref)
	case containerPartSnapshot:
	default:
		return fmt.Errorf("acquired part %s has no snapshot descriptor", key)
	}
	switch key {
	case ContainerPartFS:
		ctr.FS.setValue(dir)
	case ContainerPartExecMeta:
		ctr.MetaSnapshot.setValue(ref)
	default:
		for _, m := range ctr.Mounts {
			if dagql.PartKey("mount:"+m.Target) == key {
				if m.DirectorySource != nil {
					m.DirectorySource.setValue(dir)
				} else if m.FileSource != nil {
					m.FileSource.setValue(file)
				} else {
					return fmt.Errorf("mount %s has no snapshot accessor", key)
				}
				return nil
			}
		}
		return fmt.Errorf("missing mount %s", key)
	}
	return nil
}
