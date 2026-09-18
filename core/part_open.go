package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// openSnapshotPart makes a value's stored final snapshot available through
// its snapshot accessor, once. kind names the type in errors; load runs under
// outputMu and reports whether the snapshot is already open and the stored
// descriptor; publish runs under outputMu with the opened reference.
func (out *filesystemOutput) openSnapshotPart(ctx context.Context, kind string, address dagql.PersistedPartAddress, load func() (opened bool, stored *storedSnapshot), publish func(bkcache.ImmutableRef)) error {
	if address.Part != "snapshot" {
		return fmt.Errorf("unknown %s part %s", kind, address.Part)
	}
	out.acquiredOpen.Lock()
	defer out.acquiredOpen.Unlock()
	out.outputMu.Lock()
	opened, stored := load()
	out.outputMu.Unlock()
	if opened {
		return nil
	}
	if stored == nil {
		return fmt.Errorf("final %s snapshot has no descriptor", kind)
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	ref, err := query.SnapshotManager().GetBySnapshotID(ctx, stored.SnapshotID, bkcache.NoUpdateLastUsed)
	if err != nil {
		return err
	}
	out.outputMu.Lock()
	publish(ref)
	out.outputMu.Unlock()
	return nil
}

func (file *File) OpenPart(ctx context.Context, address dagql.PersistedPartAddress) error {
	return file.filesystemOutput.openSnapshotPart(ctx, "File", address, func() (bool, *storedSnapshot) {
		_, opened := file.Snapshot.Peek()
		return opened, file.stored
	}, file.Snapshot.setValue)
}
func (dir *Directory) OpenPart(ctx context.Context, address dagql.PersistedPartAddress) error {
	return dir.filesystemOutput.openSnapshotPart(ctx, "Directory", address, func() (bool, *storedSnapshot) {
		_, opened := dir.Snapshot.Peek()
		return opened, dir.stored
	}, dir.Snapshot.setValue)
}
func (container *Container) OpenPart(ctx context.Context, address dagql.PersistedPartAddress) error {
	if address.Part == ContainerPartMetadata {
		return nil
	}
	mu, _ := container.acquiredOpens.LoadOrStore(address.Part, new(sync.Mutex))
	lock := mu.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	view := container.acquiredOutput.Load()
	if view == nil {
		// Native final values may be opened through their original restore group;
		// ordinary live completed outputs already have their accessor.
		return container.openNativeFinalPart(ctx, address.Part)
	}
	part, ok := view.Payload.Parts[address.Part]
	if !ok {
		return fmt.Errorf("unknown Container part %s", address.Part)
	}
	if part.Kind == containerPartAbsent {
		return nil
	}
	if container.acquiredPartOpened(address.Part) {
		return nil
	}
	if part.Kind == containerPartPending {
		return fmt.Errorf("cannot open pending Container part %s", address.Part)
	}
	snapshotID := ""
	for _, link := range view.Links {
		if link.Role == part.Role {
			snapshotID = link.RefKey
			break
		}
	}
	if snapshotID == "" {
		return fmt.Errorf("missing Container role %s", part.Role)
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	ref, err := query.SnapshotManager().GetBySnapshotID(ctx, snapshotID, bkcache.NoUpdateLastUsed)
	if err != nil {
		return err
	}
	dec := dagql.NewPersistDecodeContext(dagql.CurrentDagqlServer(ctx), 0, nil)
	if host := container.partHost.Load(); host != nil {
		dec = host.DecodeContext(ctx)
	}
	if err := container.assignAcquiredRef(ctx, dec, address.Part, ref); err != nil {
		_ = ref.Release(context.WithoutCancel(ctx))
		return err
	}
	return nil
}
func (container *Container) acquiredPartOpened(part dagql.PartKey) bool {
	switch part {
	case ContainerPartFS:
		_, ok := container.FS.Peek()
		return ok
	case ContainerPartExecMeta:
		_, ok := container.MetaSnapshot.Peek()
		return ok
	default:
		for _, m := range container.Mounts {
			if dagql.PartKey("mount:"+m.Target) == part {
				if m.DirectorySource != nil {
					_, ok := m.DirectorySource.Peek()
					return ok
				}
				if m.FileSource != nil {
					_, ok := m.FileSource.Peek()
					return ok
				}
			}
		}
	}
	return false
}
func (container *Container) openNativeFinalPart(ctx context.Context, part dagql.PartKey) error {
	if container.acquiredPartOpened(part) {
		return nil
	}
	value, ok := container.storedParts[part]
	if !ok {
		return fmt.Errorf("final Container part %s has no descriptor", part)
	}
	if value.Kind == containerPartAbsent {
		container.setAbsentTransferPart(part)
		return nil
	}
	return container.openStoredContainerPart(ctx, part)
}
