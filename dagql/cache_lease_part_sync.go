package dagql

import (
	"context"
	"slices"

	"github.com/dagger/dagger/engine/wcprof"
)

// SnapshotOwnerPartReader is a value whose parts' snapshot links can be read
// a part at a time, without waiting for the value's other evaluations.
type SnapshotOwnerPartReader interface {
	// ReadSnapshotOwnerParts returns the links parts hold now and every role
	// those parts link under, held or not. ok is false when the value can't
	// answer for parts alone; it is then read whole.
	ReadSnapshotOwnerParts(parts []PartKey) (links []PersistedSnapshotRefLink, roles []string, ok bool)
}

// syncTaskPartLeases is the owner lease sync after a task's body: the parts
// it installed and those it noted as also filled (AlsoFilled), alone, or the
// whole result when it installed none.
func (c *Cache) syncTaskPartLeases(ctx context.Context, res *sharedResult, token *PartTaskToken) error {
	if token == nil {
		return c.syncInstalledPartLeases(ctx, res, nil, nil)
	}
	return c.syncInstalledPartLeases(ctx, res, token.installed.Load(), token.alsoFilledParts())
}

// syncInstalledPartLeases is the owner lease sync after an evaluation filled
// parts of res: it reconciles the leases of those parts alone. The whole
// sync reads every part of res, and waits for the parts still being filled
// to read them coherently; after each of many parts filled one by one, that
// is a wait on the others and a read of all of them each time. The parts
// filled here are complete, and the parts not read keep the leases they
// have, so reading only these is coherent. Anything it can't scope, it
// syncs whole.
func (c *Cache) syncInstalledPartLeases(ctx context.Context, res *sharedResult, installed *InstalledOutputs, also []PartKey) error {
	if c == nil || c.snapshotManager == nil || res == nil || res.id == 0 {
		return nil
	}
	parts, ok := rootParts(installed)
	for _, part := range also {
		if !slices.Contains(parts, part) {
			parts = append(parts, part)
		}
	}
	state := res.loadPayloadState()
	reader, isReader := state.self.(SnapshotOwnerPartReader)
	if !ok || !state.hasValue || !isReader {
		return c.syncResultSnapshotLeases(ctx, res)
	}

	wcprof.Lock(ctx, &res.leaseSyncMu, "dagql.sharedResult.leaseSyncMu")
	_, readOp := wcprof.BeginOp(ctx, wcprof.OpKindInternal, "lease.partLinks", wcprof.OpOpts{})
	links, roles, ok := reader.ReadSnapshotOwnerParts(parts)
	readOp.EndErr(nil)
	if !ok {
		res.leaseSyncMu.Unlock()
		return c.syncResultSnapshotLeases(ctx, res)
	}
	defer res.leaseSyncMu.Unlock()

	scoped := make(map[string]bool, len(roles))
	for _, role := range roles {
		scoped[role] = true
	}
	// The stored links of the parts read are replaced by what they hold now;
	// every other stored link stays as it is.
	var kept []PersistedSnapshotRefLink
	oldByRole := map[string]PersistedSnapshotRefLink{}
	for _, link := range res.loadSnapshotOwnerLinks() {
		if len(link.OutputPath) == 0 && scoped[link.Role] {
			oldByRole[link.Role] = link
		} else {
			kept = append(kept, link)
		}
	}
	newByRole := make(map[string]PersistedSnapshotRefLink, len(links))
	for _, link := range links {
		if link.RefKey == "" || !scoped[link.Role] || len(link.OutputPath) != 0 {
			return c.syncResultSnapshotLeasesLocked(ctx, res)
		}
		newByRole[link.Role] = link
	}

	// As the whole sync does: a borrowed link has no lease of its own.
	for role, old := range oldByRole {
		if old.Borrowed {
			continue
		}
		if cur, ok := newByRole[role]; !ok || cur.Borrowed || cur.RefKey != old.RefKey {
			key, err := snapshotLinkKey(old)
			if err != nil {
				return err
			}
			_, removeOp := wcprof.BeginOp(ctx, wcprof.OpKindInternal, "lease.remove", wcprof.OpOpts{})
			err = c.snapshotManager.RemoveLease(ctx, resultSnapshotLeaseIDForKey(res.id, key))
			removeOp.EndErr(err)
			if err != nil {
				return err
			}
		}
	}
	for role, cur := range newByRole {
		if cur.Borrowed {
			continue
		}
		if old, ok := oldByRole[role]; ok && !old.Borrowed && old.RefKey == cur.RefKey {
			continue
		}
		key, err := snapshotLinkKey(cur)
		if err != nil {
			return err
		}
		res.payloadMu.Lock()
		if res.snapshotLeaseCleanupRoles == nil {
			res.snapshotLeaseCleanupRoles = map[snapshotOwnerKey]struct{}{}
		}
		res.snapshotLeaseCleanupRoles[key] = struct{}{}
		res.payloadMu.Unlock()
		attachCtx, attachOp := wcprof.BeginOp(ctx, wcprof.OpKindInternal, "lease.attach", wcprof.OpOpts{})
		err = c.snapshotManager.AttachLease(attachCtx, resultSnapshotLeaseIDForKey(res.id, key), cur.RefKey)
		attachOp.EndErr(err)
		if err != nil {
			return err
		}
	}
	res.storeSnapshotOwnerLinks(append(kept, links...))
	return nil
}

// rootParts is the parts installed fills on the row itself, or false when it
// fills a part below it or nothing.
func rootParts(installed *InstalledOutputs) ([]PartKey, bool) {
	if installed == nil || len(installed.outputs) == 0 {
		return nil, false
	}
	parts := make([]PartKey, 0, len(installed.outputs))
	for _, output := range installed.outputs {
		if len(output.address.OutputPath) != 0 {
			return nil, false
		}
		parts = append(parts, output.address.Part)
	}
	return parts, true
}
