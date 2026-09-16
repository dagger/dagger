package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

func (family foreignFamilyCodec) PreparePartRecord(receiver, source dagql.PersistedRecord, d dagql.PartDescriptor, target dagql.PersistedPartAddress) (dagql.PersistedRecord, error) {
	if len(target.OutputPath) != 0 {
		return receiver, fmt.Errorf("codec part store requires a local output address")
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
		p.OperationState = ""
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
		p.OperationState = ""
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
		if p.OperationState == "" {
			p.OperationState = transferOperationState(false, len(p.LazyJSON) != 0)
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
	receiver.SnapshotLinks = dagql.ClonePersistedSnapshotLinks(receiver.SnapshotLinks)
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
	path     string
	ref      bkcache.ImmutableRef
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
	unlockAccessors, ok := tryPartAccessorLocks([]*sync.RWMutex{&s.receiver.File.mu, &s.receiver.Snapshot.mu})
	if !ok {
		u()
		return false
	}
	s.unlock = func() { unlockAccessors(); u() }
	return true
}
func (s *filePartStore) Unlock() { s.unlock() }
func (s *filePartStore) Publish() {
	r, n := s.receiver, s.next
	r.File.value = s.path
	r.File.isSet = true
	r.Snapshot.value = s.ref
	r.Snapshot.isSet = true
	r.Platform = n.Platform
	r.Services = n.Services
	r.stored = n.stored
	r.storedDiagnostics = n.storedDiagnostics
	r.lazyKind = n.lazyKind
	r.lazyJSON = n.lazyJSON
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
	path, _ := n.File.Peek()
	return &filePartStore{receiver: file, next: n, expected: revision, path: path, ref: ref}, nil
}

type directoryPartStore struct {
	receiver *Directory
	next     *Directory
	expected dagql.OutputRevision
	unlock   func()
	path     string
	ref      bkcache.ImmutableRef
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
	unlockAccessors, ok := tryPartAccessorLocks([]*sync.RWMutex{&s.receiver.Dir.mu, &s.receiver.Snapshot.mu})
	if !ok {
		u()
		return false
	}
	s.unlock = func() { unlockAccessors(); u() }
	return true
}
func (s *directoryPartStore) Unlock() { s.unlock() }
func (s *directoryPartStore) Publish() {
	r, n := s.receiver, s.next
	r.Dir.value = s.path
	r.Dir.isSet = true
	r.Snapshot.value = s.ref
	r.Snapshot.isSet = true
	r.Platform = n.Platform
	r.Services = n.Services
	r.stored = n.stored
	r.storedDiagnostics = n.storedDiagnostics
	r.lazyKind = n.lazyKind
	r.lazyJSON = n.lazyJSON
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
	path, _ := n.Dir.Peek()
	return &directoryPartStore{receiver: dir, next: n, expected: revision, path: path, ref: ref}, nil
}

// An adapter-owned Container publishes payload, operation and all desired roles
// as one immutable value, without borrowing a native operation latch.
type containerAcquiredOutput struct {
	Payload  persistedContainerPayload
	Links    []dagql.PersistedSnapshotRefLink
	Revision dagql.OutputRevision
}

func (ctr *Container) PersistedOutputRevision() (dagql.OutputRevision, error) {
	if view := ctr.acquiredOutput.Load(); view != nil {
		return view.Revision, nil
	}
	unlock, err := ctr.tryPartPublicationGuard()
	if err != nil {
		return 0, err
	}
	defer unlock()
	lazy := ctr.Lazy
	if provider, ok := lazy.(interface{ ContainerLazyState() *LazyState }); ok {
		return dagql.OutputRevision(provider.ContainerLazyState().outputRevision.Load()), nil
	}
	return 0, nil
}

func (ctr *Container) PersistedSnapshotRefLinksChecked() ([]dagql.PersistedSnapshotRefLink, error) {
	if view := ctr.acquiredOutput.Load(); view != nil {
		return dagql.ClonePersistedSnapshotLinks(view.Links), nil
	}
	unlock, err := ctr.tryPartPublicationGuard()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return ctr.PersistedSnapshotRefLinks(), nil
}

func (ctr *Container) ReadSnapshotOwner() (dagql.OutputRevision, []dagql.PersistedSnapshotRefLink, error) {
	if view := ctr.acquiredOutput.Load(); view != nil {
		return view.Revision, dagql.ClonePersistedSnapshotLinks(view.Links), nil
	}
	unlock, err := ctr.lockSnapshotOwnerRead()
	if err != nil {
		return 0, nil, err
	}
	defer unlock()
	if view := ctr.acquiredOutput.Load(); view != nil {
		return view.Revision, dagql.ClonePersistedSnapshotLinks(view.Links), nil
	}
	var revision dagql.OutputRevision
	if provider, ok := ctr.Lazy.(interface{ ContainerLazyState() *LazyState }); ok {
		revision = dagql.OutputRevision(provider.ContainerLazyState().outputRevision.Load())
	}
	return revision, ctr.PersistedSnapshotRefLinks(), nil
}

// Owner synchronization holds no graph lock. Readers may wait on one another,
// but must drop the pointer/state latches before waiting for a group body: the
// body can consult either latch. Each restart follows an actual body-latch wait.
func (ctr *Container) lockSnapshotOwnerRead() (func(), error) {
	for {
		ctr.lazyOpMu.Lock()
		if ctr.acquiredOutput.Load() != nil {
			return ctr.lazyOpMu.Unlock, nil
		}
		lazy := ctr.Lazy
		if lazy == nil {
			return ctr.lazyOpMu.Unlock, nil
		}
		provider, ok := lazy.(interface{ ContainerLazyState() *LazyState })
		if !ok || provider.ContainerLazyState() == nil || provider.ContainerLazyState().LazyMu == nil {
			ctr.lazyOpMu.Unlock()
			return nil, fmt.Errorf("Container ownership read: missing native latch")
		}
		state := provider.ContainerLazyState()
		if !state.LazyMu.TryLock() {
			// A whole body may need the graph lock while a diagnostic or
			// encoder needs lazyOpMu. Wait without retaining that pointer hold.
			ctr.lazyOpMu.Unlock()
			state.LazyMu.Lock()
			state.LazyMu.Unlock()
			continue
		}
		var held []*lazyGroupOnce
		unlock := func() {
			for _, group := range held {
				group.mu.Unlock()
			}
			state.LazyMu.Unlock()
			ctr.lazyOpMu.Unlock()
		}
		if state.IsEvaluated() {
			return unlock, nil
		}
		var running *lazyGroupOnce
		for _, group := range state.groups {
			if group.done.Load() {
				continue
			}
			if !group.mu.TryLock() {
				running = group
				break
			}
			held = append(held, group)
		}
		if running == nil {
			return unlock, nil
		}
		unlock()
		running.mu.Lock()
		running.mu.Unlock()
	}
}

type containerPartStore struct {
	receiver, next *Container
	view, previous *containerAcquiredOutput
	part           dagql.PartKey
	parts          []dagql.PartKey
	unlock         func()
	assignments    []containerPartAssignment
	locks          []*sync.RWMutex
}

func (s *containerPartStore) TryLock() bool {
	if s.receiver.acquiredOutput.Load() != s.previous {
		return false
	}
	if s.previous == nil {
		u, err := s.receiver.tryPartPublicationGuard()
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
	unlockAccessors, ok := tryPartAccessorLocks(s.locks)
	if !ok {
		s.unlock()
		return false
	}
	priorUnlock := s.unlock
	s.unlock = func() { unlockAccessors(); priorUnlock() }
	return true
}
func (s *containerPartStore) Unlock() { s.unlock() }
func (s *containerPartStore) Publish() {
	r, n := s.receiver, s.next
	if slices.Contains(s.parts, ContainerPartMetadata) || s.part == ContainerPartMetadata {
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
	}
	for _, a := range s.assignments {
		if a.directory != nil {
			a.directory.value = a.dir
			a.directory.isSet = true
		}
		if a.file != nil {
			a.file.value = a.fileValue
			a.file.isSet = true
		}
		if a.snapshot != nil {
			a.snapshot.value = a.ref
			a.snapshot.isSet = true
		}
	}
	r.acquiredOutput.Store(s.view)
}
func (ctr *Container) PreparePartStore(ctx context.Context, dec *dagql.PersistDecodeContext, record dagql.PersistedRecord, d dagql.PartDescriptor, ref bkcache.ImmutableRef) (dagql.PreparedPartStore, error) {
	previous := ctr.acquiredOutput.Load()
	baseRevision, err := ctr.PersistedOutputRevision()
	if err != nil {
		return nil, err
	}
	typed, err := (*Container)(nil).DecodePersistedObject(ctx, dec, record.Envelope.ObjectJSON)
	if err != nil {
		return nil, err
	}
	next := typed.(*Container)
	view := next.acquiredOutput.Load()
	if view == nil {
		return nil, fmt.Errorf("Container store requires raw representation")
	}
	view.Revision = baseRevision + 1
	if d.Address.Part != ContainerPartMetadata && !d.Absent {
		if err := next.assignAcquiredRef(ctx, dec, d.Address.Part, ref); err != nil {
			return nil, err
		}
	}
	store := &containerPartStore{receiver: ctr, next: next, view: view, previous: previous, part: d.Address.Part}
	store.prepareAssignments()
	return store, nil
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

func (ctr *Container) PreparePartStores(ctx context.Context, dec *dagql.PersistDecodeContext, record dagql.PersistedRecord, descriptors []dagql.PartDescriptor, refs []bkcache.ImmutableRef) (dagql.PreparedPartStore, error) {
	if len(descriptors) == 0 {
		return nil, fmt.Errorf("empty Container publication")
	}
	prepared, err := ctr.PreparePartStore(ctx, dec, record, descriptors[0], refs[0])
	if err != nil {
		return nil, err
	}
	store := prepared.(*containerPartStore)
	store.parts = []dagql.PartKey{descriptors[0].Address.Part}
	for i := 1; i < len(descriptors); i++ {
		d := descriptors[i]
		store.parts = append(store.parts, d.Address.Part)
		if d.Address.Part != ContainerPartMetadata && !d.Absent {
			if err := store.next.assignAcquiredRef(ctx, dec, d.Address.Part, refs[i]); err != nil {
				return nil, err
			}
		}
	}
	store.prepareAssignments()
	return store, nil
}

func tryPartAccessorLocks(locks []*sync.RWMutex) (func(), bool) {
	for i, mu := range locks {
		if !mu.TryLock() {
			for _, held := range locks[:i] {
				held.Unlock()
			}
			return nil, false
		}
	}
	return func() {
		for _, mu := range locks {
			mu.Unlock()
		}
	}, true
}

type containerPartAssignment struct {
	directory *LazyAccessor[*Directory, *Container]
	file      *LazyAccessor[*File, *Container]
	snapshot  *LazyAccessor[bkcache.ImmutableRef, *Container]
	dir       *Directory
	fileValue *File
	ref       bkcache.ImmutableRef
}

func (s *containerPartStore) prepareAssignments() {
	s.assignments = nil
	s.locks = nil
	parts := s.parts
	if len(parts) == 0 {
		parts = []dagql.PartKey{s.part}
	}
	metadata := slices.Contains(parts, ContainerPartMetadata)
	for _, part := range parts {
		var a containerPartAssignment
		switch part {
		case ContainerPartMetadata:
			continue
		case ContainerPartFS:
			a.directory = s.receiver.FS
			a.dir, _ = s.next.FS.Peek()
		case ContainerPartExecMeta:
			a.snapshot = s.receiver.MetaSnapshot
			a.ref, _ = s.next.MetaSnapshot.Peek()
		default:
			if metadata {
				continue
			} // metadata installs the fully prepared new mount list
			for i, m := range s.receiver.Mounts {
				if dagql.PartKey("mount:"+m.Target) == part {
					a.directory = m.DirectorySource
					a.file = m.FileSource
					if a.directory != nil {
						a.dir, _ = s.next.Mounts[i].DirectorySource.Peek()
					}
					if a.file != nil {
						a.fileValue, _ = s.next.Mounts[i].FileSource.Peek()
					}
					break
				}
			}
		}
		if a.directory != nil {
			s.locks = append(s.locks, &a.directory.mu)
		}
		if a.file != nil {
			s.locks = append(s.locks, &a.file.mu)
		}
		if a.snapshot != nil {
			s.locks = append(s.locks, &a.snapshot.mu)
		}
		s.assignments = append(s.assignments, a)
	}
}

// Commit must never wait for native pointer, whole-body or group locks while
// holding the cache gate. Native activation takes all of them by try-lock.
func (ctr *Container) tryPartPublicationGuard() (func(), error) {
	if !ctr.lazyOpMu.TryLock() {
		return nil, fmt.Errorf("%w: Container operation pointer busy", dagql.ErrPersistStateNotReady)
	}
	lazy := ctr.Lazy
	if lazy == nil {
		return ctr.lazyOpMu.Unlock, nil
	}
	provider, ok := lazy.(interface{ ContainerLazyState() *LazyState })
	if !ok {
		ctr.lazyOpMu.Unlock()
		return nil, fmt.Errorf("Container publication: missing native guard")
	}
	state := provider.ContainerLazyState()
	if state == nil || state.LazyMu == nil {
		ctr.lazyOpMu.Unlock()
		return nil, fmt.Errorf("Container publication: missing native latch")
	}
	if !state.LazyMu.TryLock() {
		ctr.lazyOpMu.Unlock()
		return nil, fmt.Errorf("%w: Container %T state busy (evaluated=%t)", dagql.ErrPersistStateNotReady, lazy, state.IsEvaluated())
	}
	var held []*lazyGroupOnce
	unlock := func() {
		for _, group := range held {
			group.mu.Unlock()
		}
		state.LazyMu.Unlock()
		ctr.lazyOpMu.Unlock()
	}
	for key, group := range state.groups {
		if group.done.Load() {
			continue
		}
		if !group.mu.TryLock() {
			unlock()
			return nil, fmt.Errorf("%w: Container %T group %q busy (consumed=%t)", dagql.ErrPersistStateNotReady, lazy, key, group.done.Load())
		}
		held = append(held, group)
	}
	return unlock, nil
}
