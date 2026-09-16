package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

func (file *File) OpenPart(ctx context.Context, address dagql.PersistedPartAddress) error {
	if address.Part != "snapshot" {
		return fmt.Errorf("unknown File part %s", address.Part)
	}
	file.acquiredOpen.Lock()
	defer file.acquiredOpen.Unlock()
	file.outputMu.Lock()
	_, opened := file.Snapshot.Peek()
	stored := file.stored
	file.outputMu.Unlock()
	if opened {
		return nil
	}
	if stored == nil {
		return fmt.Errorf("final File snapshot has no descriptor")
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	ref, err := query.SnapshotManager().GetBySnapshotID(ctx, stored.SnapshotID, bkcache.NoUpdateLastUsed)
	if err != nil {
		return err
	}
	file.outputMu.Lock()
	file.Snapshot.setValue(ref)
	file.outputMu.Unlock()
	return nil
}
func (dir *Directory) OpenPart(ctx context.Context, address dagql.PersistedPartAddress) error {
	if address.Part != "snapshot" {
		return fmt.Errorf("unknown Directory part %s", address.Part)
	}
	dir.acquiredOpen.Lock()
	defer dir.acquiredOpen.Unlock()
	dir.outputMu.Lock()
	_, opened := dir.Snapshot.Peek()
	stored := dir.stored
	dir.outputMu.Unlock()
	if opened {
		return nil
	}
	if stored == nil {
		return fmt.Errorf("final Directory snapshot has no descriptor")
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	ref, err := query.SnapshotManager().GetBySnapshotID(ctx, stored.SnapshotID, bkcache.NoUpdateLastUsed)
	if err != nil {
		return err
	}
	dir.outputMu.Lock()
	dir.Snapshot.setValue(ref)
	dir.outputMu.Unlock()
	return nil
}
func (ctr *Container) OpenPart(ctx context.Context, address dagql.PersistedPartAddress) error {
	if address.Part == ContainerPartMetadata {
		return nil
	}
	mu, _ := ctr.acquiredOpens.LoadOrStore(address.Part, new(sync.Mutex))
	lock := mu.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	view := ctr.acquiredOutput.Load()
	if view == nil {
		// Native final values may be opened through their original restore group;
		// ordinary live completed outputs already have their accessor.
		return ctr.openNativeFinalPart(ctx, address.Part)
	}
	part, ok := view.Payload.Parts[address.Part]
	if !ok {
		return fmt.Errorf("unknown Container part %s", address.Part)
	}
	if part.Kind == containerPartAbsent {
		return nil
	}
	if ctr.acquiredPartOpened(address.Part) {
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
	if host := ctr.partHost.Load(); host != nil {
		dec = host.DecodeContext(ctx)
	}
	if err := ctr.assignAcquiredRef(ctx, dec, address.Part, ref); err != nil {
		_ = ref.Release(context.WithoutCancel(ctx))
		return err
	}
	return nil
}
func (ctr *Container) acquiredPartOpened(part dagql.PartKey) bool {
	switch part {
	case ContainerPartFS:
		_, ok := ctr.FS.Peek()
		return ok
	case ContainerPartExecMeta:
		_, ok := ctr.MetaSnapshot.Peek()
		return ok
	default:
		for _, m := range ctr.Mounts {
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
func (ctr *Container) openNativeFinalPart(ctx context.Context, part dagql.PartKey) error {
	if ctr.acquiredPartOpened(part) {
		return nil
	}
	value, ok := ctr.storedParts[part]
	if !ok {
		return fmt.Errorf("final Container part %s has no descriptor", part)
	}
	if value.Kind == containerPartAbsent {
		ctr.setAbsentTransferPart(part)
		return nil
	}
	return ctr.openStoredContainerPart(ctx, part)
}
