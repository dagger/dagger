package dagql

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/opencontainers/go-digest"
)

type capturedTransferRow struct {
	shared                                  *sharedResult
	version                                 capturedRowRevision
	imported                                bool
	frame                                   *ResultCall
	deps                                    []uint64
	offers                                  []PersistedPartOffer
	transferRev, dependencyRev, requiredRev uint64
	// replacements is the entry's replacement count, read with the rest of
	// the row; a replacement also moves transferRev.
	replacements uint64
	expiry       int64
	record       PersistedRecord
	outputs      []CapturedCodecOutput
	ordinal      TransferOrdinal
	// children are the rows the walk reached from this one: its
	// dependencies, and those of the owners of its offers.
	children []sharedResultID
	// transfer is the record as the bundle carries it, before its
	// references are relocated to ordinals.
	transfer PersistedRecord
	// held makes the row a held row, of recipe: the selection's recipient
	// knows it, so the capture copies no record for it and walks nothing
	// below it.
	held   bool
	recipe digest.Digest
}

// HeldCapturedClosure protects every exact source row and offer while storage
// opens and provider consumption run outside graph and value locks.
type HeldCapturedClosure struct {
	cache *Cache
	// rows and order are the rows of the closure, in dependency order; once
	// the capture has left out the roots it can't send (settle), only the
	// kept roots' rows.
	rows   map[sharedResultID]*capturedTransferRow
	order  []*capturedTransferRow
	owners map[offerOwnerID]*offerOwner
	roots  []TransferredRoot
	// selected are the selection's roots, in its order.
	selected []capturedRoot
	// blocked are the rows the capture can't capture now, or refuses, by
	// row: each with the row that blocks it, itself or one below it.
	blocked map[sharedResultID]blockedRow
	// dropped are the rows held for left-out roots only, released with the
	// others.
	dropped []*capturedTransferRow
	// unheld are the dependencies of the rows the walk found attaching,
	// which it walks but doesn't hold.
	unheld map[sharedResultID][]sharedResultID
	// outputs are the selection's outputs the closure holds.
	outputs []SelectedValueOutput
	// walkWait is the wait for egraphMu before the walk, and walk the walk
	// under it.
	walkWait    time.Duration
	walk        time.Duration
	releaseOnce sync.Once
	releaseErr  error
}

// capturedRoot is one selected root: the row it holds, its retention's
// expiry, and, for a root left out whatever its closure holds, why.
type capturedRoot struct {
	row    *sharedResult
	expiry int64
	err    *blockedRow
}

// blockedRow is why a row can't go: the row whose capture failed, what keeps
// it busy if it's busy, and its error.
type blockedRow struct {
	row  sharedResultID
	kind BusyKind
	err  error
}

// rowBlockedError ends the walk below a blocked row.
type rowBlockedError struct{ blockedRow }

func (err *rowBlockedError) Error() string { return err.err.Error() }

func (capture *HeldCapturedClosure) Release(ctx context.Context) error {
	if capture == nil {
		return nil
	}
	capture.releaseOnce.Do(func() {
		ctx = context.WithoutCancel(ctx)
		c := capture.cache
		c.egraphMu.Lock()
		var queue collectionQueue
		for _, owner := range capture.owners {
			q, err := c.releaseOfferOwnerLocked(ctx, owner)
			queue = append(queue, q...)
			capture.releaseErr = errors.Join(capture.releaseErr, err)
		}
		for _, row := range capture.rows {
			q, err := c.decrementIncomingOwnershipLocked(ctx, row.shared, nil)
			queue = append(queue, q...)
			capture.releaseErr = errors.Join(capture.releaseErr, err)
		}
		for _, row := range capture.dropped {
			q, err := c.decrementIncomingOwnershipLocked(ctx, row.shared, nil)
			queue = append(queue, q...)
			capture.releaseErr = errors.Join(capture.releaseErr, err)
		}
		releases, err := c.collectUnownedResultsLocked(ctx, queue)
		c.egraphMu.Unlock()
		capture.releaseErr = errors.Join(capture.releaseErr, err, runOnReleaseFuncs(ctx, releases))
	})
	return capture.releaseErr
}

// block marks a row the capture can't capture, or refuses: err is its error,
// and kind what keeps it busy, for a busy row.
func (capture *HeldCapturedClosure) block(id sharedResultID, kind BusyKind, err error) {
	if _, done := capture.blocked[id]; done {
		return
	}
	if !errors.Is(err, ErrPersistStateNotReady) {
		kind = ""
	}
	capture.blocked[id] = blockedRow{row: id, kind: kind, err: err}
}

// holdTransferClosure walks and holds the closure of every selected root. A
// row the walk can't capture, or refuses, blocks the roots that reach it,
// not the walk: the walk goes on with the other roots, and the rows it holds
// for the blocked ones are released with the rest. Only a selection that
// isn't one (no roots, a nil, detached or repeated root) fails it. A
// selection of no outputs holds each row its Known knows, unless carried
// names it, as a held row.
//
//nolint:gocyclo // one classification per held result kind; splitting hides the order of the checks
func (c *Cache) holdTransferClosure(ctx context.Context, selection ValueSelection, carried map[sharedResultID]bool) (*HeldCapturedClosure, error) {
	capture := &HeldCapturedClosure{cache: c, rows: map[sharedResultID]*capturedTransferRow{}, owners: map[offerOwnerID]*offerOwner{}, blocked: map[sharedResultID]blockedRow{}, unheld: map[sharedResultID][]sharedResultID{}}
	known := selection.Known
	if len(selection.Outputs) > 0 {
		known = nil
	}
	lockStart := time.Now()
	c.egraphMu.Lock()
	walkStart := time.Now()
	capture.walkWait = walkStart.Sub(lockStart)
	// seen is 1 while a row's walk is under way, 2 once it's done, and 3
	// once it's blocked.
	seen := map[sharedResultID]uint8{}
	// blocked ends a row's walk: the row is blocked by the one below it
	// whose walk failed, or by itself.
	blocked := func(id sharedResultID, cause blockedRow) error {
		seen[id] = 3
		capture.blocked[id] = cause
		return &rowBlockedError{cause}
	}
	var walk func(sharedResultID, *sharedResult) error
	walk = func(id sharedResultID, res *sharedResult) error {
		if res == nil || res.id == 0 || c.resultsByID[res.id] != res {
			return blocked(id, blockedRow{row: id, err: fmt.Errorf("transfer capture: unregistered row")})
		}
		switch seen[res.id] {
		case 1:
			return blocked(res.id, blockedRow{row: res.id, err: fmt.Errorf("transfer capture: ownership cycle at %d", res.id)})
		case 2:
			return nil
		case 3:
			return &rowBlockedError{capture.blocked[res.id]}
		}
		if res.attachmentState() != resultAttachmentClean {
			// The dependencies it has so far are walked like any row's,
			// under the graph lock, but the row itself isn't held: its
			// roots are left out whatever its attachment adds later.
			seen[res.id] = 1
			var children []sharedResultID
			for child := range c.resultOwnershipChildrenLocked(res) {
				if child.owner == nil {
					children = append(children, child.resultID)
					_ = walk(child.resultID, child.result)
					continue
				}
				for dep := range offerOwnershipChildrenLocked(child.owner) {
					if dep != nil {
						children = append(children, dep.id)
						_ = walk(dep.id, dep)
					}
				}
			}
			capture.unheld[res.id] = children
			return blocked(res.id, blockedRow{row: res.id, kind: BusyAttaching, err: fmt.Errorf("%w: result %d attachment", ErrPersistStateNotReady, res.id)})
		}
		frame := res.loadResultCall()
		if frame == nil || frame.Type == nil || frame.Type.NamedType == "Query" {
			return blocked(res.id, blockedRow{row: res.id, err: fmt.Errorf("transfer capture: row %d has no transferable frame", res.id)})
		}
		if !carried[res.id] && known.knowsLocked(res) {
			c.incrementIncomingOwnershipLocked(ctx, res)
			row := &capturedTransferRow{shared: res, held: true, imported: res.imported, frame: frame, expiry: res.expiresAtUnix, replacements: res.replacements, transferRev: res.transferRevision, dependencyRev: res.dependencyOwnershipRevision, requiredRev: res.requiredSessionResourcesGen.Load()}
			capture.rows[res.id] = row
			seen[res.id] = 2
			row.ordinal = TransferOrdinal(len(capture.order) + 1)
			capture.order = append(capture.order, row)
			return nil
		}
		seen[res.id] = 1
		offers, err := res.pendingOffersLocked()
		if err != nil {
			kind := BusyOther
			if !errors.Is(err, ErrPersistStateNotReady) {
				kind = ""
			}
			return blocked(res.id, blockedRow{row: res.id, kind: kind, err: err})
		}
		c.incrementIncomingOwnershipLocked(ctx, res)
		row := &capturedTransferRow{shared: res, imported: res.imported, frame: frame, offers: offers, expiry: res.expiresAtUnix, replacements: res.replacements, transferRev: res.transferRevision, dependencyRev: res.dependencyOwnershipRevision, requiredRev: res.requiredSessionResourcesGen.Load()}
		capture.rows[res.id] = row
		for id := range res.deps {
			row.deps = append(row.deps, uint64(id))
		}
		slices.Sort(row.deps)
		// Every child is walked, even past a blocked one, so that the
		// capture knows every row below the root: each busy row it reaches,
		// and each row whose selected output goes or is left out with it.
		// The row is blocked by the lowest-numbered blocked row below it.
		var cause *blockedRow
		below := func(id sharedResultID, child *sharedResult) {
			if child != nil {
				id = child.id
			}
			row.children = append(row.children, id)
			if err := walk(id, child); err != nil {
				stop := err.(*rowBlockedError)
				if cause == nil || stop.row < cause.row {
					cause = &stop.blockedRow
				}
			}
		}
		for child := range c.resultOwnershipChildrenLocked(res) {
			if child.owner == nil {
				below(child.resultID, child.result)
				continue
			}
			if _, held := capture.owners[child.owner.id]; !held {
				c.retainOfferOwnerLocked(child.owner)
				capture.owners[child.owner.id] = child.owner
			}
			for dep := range offerOwnershipChildrenLocked(child.owner) {
				below(0, dep)
			}
		}
		if cause != nil {
			return blocked(res.id, *cause)
		}
		seen[res.id] = 2
		row.ordinal = TransferOrdinal(len(capture.order) + 1)
		capture.order = append(capture.order, row)
		return nil
	}
	roots := map[sharedResultID]bool{}
	var err error
	if len(selection.Roots) == 0 {
		err = fmt.Errorf("transfer capture: no roots")
	}
	now := time.Now().Unix()
	for _, root := range selection.Roots {
		if err != nil {
			break
		}
		if root == nil {
			err = fmt.Errorf("transfer capture: nil root")
			break
		}
		res := root.cacheSharedResult()
		if res == nil {
			err = fmt.Errorf("transfer capture: detached root")
			break
		}
		if roots[res.id] {
			err = fmt.Errorf("transfer capture: duplicate root")
			break
		}
		roots[res.id] = true
		selected := capturedRoot{row: res}
		if res.expiresAtUnix != 0 && res.expiresAtUnix <= now {
			selected.err = &blockedRow{row: res.id, err: fmt.Errorf("transfer capture: expired root %d", res.id)}
			capture.selected = append(capture.selected, selected)
			continue
		}
		if walkErr := walk(res.id, res); walkErr != nil {
			var stop *rowBlockedError
			if !errors.As(walkErr, &stop) {
				err = walkErr
				break
			}
			capture.selected = append(capture.selected, selected)
			continue
		}
		selected.expiry = res.expiresAtUnix
		if edge, ok := c.persistedEdgesByResult[res.id]; ok {
			selected.expiry = edge.expiresAtUnix
		}
		if selected.expiry != 0 && selected.expiry <= now {
			selected.err = &blockedRow{row: res.id, err: fmt.Errorf("expired retention edge for root %d", res.id)}
		}
		capture.selected = append(capture.selected, selected)
	}
	capture.walk = time.Since(walkStart)
	c.egraphMu.Unlock()
	if wcprof.Enabled(ctx) {
		wcprof.RecordWaitAt(ctx, egraphLockIdent, wcprof.WaitReasonLock, lockStart, walkStart)
		wcprof.RecordOpAt(ctx, wcprof.OpKindRemoteCache, "remotecache.export.walk", wcprof.OpOpts{}, walkStart, walkStart.Add(capture.walk), profErrOutcome(err))
	}
	if err != nil {
		return nil, errors.Join(err, capture.Release(ctx))
	}
	return capture, nil
}

// staleOffersError is a capture's refusal for offers on outputs the captured
// values already completed. Such an offer is obsolete: it offers a part the
// value has. Nothing retires it while the engine runs, unlike one on a part
// installed from it, so the capture's caller retires it (retireStaleOffers)
// and captures again.
type staleOffersError struct {
	offers []staleOffer
}

// staleOffer is an offer on a completed output, with the row's transfer
// revision when it was captured: any change to the row's offers moves it.
type staleOffer struct {
	row         *sharedResult
	address     PersistedPartAddress
	transferRev uint64
}

func (err *staleOffersError) Error() string {
	offer := err.offers[0]
	key, _ := partAddressKey(offer.address)
	return fmt.Sprintf("%s: unretired offer on completed output %d:%s", ErrPersistStateNotReady, offer.row.id, key)
}

func (err *staleOffersError) Unwrap() error { return ErrPersistStateNotReady }

// retireStaleOffers retires each stale offer whose row still holds the offers
// it was captured with, as boot retires those it finds on completed outputs
// (settleRestoredOffers). An offer whose row changed since is left for the
// next capture to judge.
func (c *Cache) retireStaleOffers(ctx context.Context, stale []staleOffer) error {
	ctx = context.WithoutCancel(ctx)
	c.egraphMu.Lock()
	var queue collectionQueue
	var rerr error
	// Each row is judged once, before any of its offers is retired: a
	// retirement moves the revision its other stale offers were captured at.
	unchanged := map[*sharedResult]bool{}
	for _, offer := range stale {
		current, judged := unchanged[offer.row]
		if !judged {
			current = c.resultsByID[offer.row.id] == offer.row && offer.row.transferRevision == offer.transferRev
			unchanged[offer.row] = current
		}
		if !current {
			continue
		}
		q, err := c.retirePartOfferLocked(ctx, offer.row, offer.address)
		queue = append(queue, q...)
		rerr = errors.Join(rerr, err)
	}
	releases, err := c.collectUnownedResultsLocked(ctx, queue)
	c.egraphMu.Unlock()
	return errors.Join(rerr, err, runOnReleaseFuncs(ctx, releases))
}

// copy copies each walked row's record and checks that it still describes
// its row. A row it can't copy now, or refuses, or that changed meanwhile,
// is blocked, and copy goes on with the others; only the capture's own
// cancellation fails it. It returns the obsolete offers it found on rows it
// copied: with retireStale they end the copy before its checks, for the
// caller to retire them and capture again; without, they block their rows.
func (capture *HeldCapturedClosure) copy(ctx context.Context, retireStale bool) ([]staleOffer, error) {
	var stale []staleOffer
	var busy []*capturedTransferRow
	blockRow := func(row *capturedTransferRow, err error) {
		capture.block(row.shared.id, "", err)
		if errors.Is(err, ErrPersistStateNotReady) {
			busy = append(busy, row)
		}
	}
	copied := make([]bool, len(capture.order))
	for i, row := range capture.order {
		if row.held {
			// No record to copy; checkCopied still checks that it hasn't
			// changed since the walk.
			copied[i] = true
			continue
		}
		rec, err := capture.cache.captureHeldPersistedRecord(ctx, row.shared, row.imported, row.offers, &row.version)
		if err != nil {
			if ctx.Err() != nil {
				return nil, context.Cause(ctx)
			}
			blockRow(row, err)
			continue
		}
		row.record = rec
		if hook := capture.cache.testTransferCopied; hook != nil {
			hook(uint64(row.shared.id))
		}
		row.outputs, err = mapTransferredOutputs(rec)
		if err != nil {
			blockRow(row, err)
			continue
		}
		copied[i] = true
		rowStale, err := row.staleOffers()
		if err != nil {
			blockRow(row, err)
		}
		stale = append(stale, rowStale...)
		if !retireStale && len(rowStale) > 0 {
			capture.block(row.shared.id, BusyStaleOffer, &staleOffersError{offers: rowStale})
		}
	}
	// Release each row's copy guards before comparing the next row.
	for i, row := range capture.order {
		if !copied[i] || row.held {
			continue
		}
		if err := row.version.check(row.shared); err != nil {
			blockRow(row, err)
		}
	}
	// The records still describe their rows, so the completed outputs are
	// the values' own.
	if retireStale && len(stale) > 0 {
		return stale, nil
	}

	capture.checkCopied(copied, blockRow)
	// What keeps each busy row busy is read once no capture lock is held:
	// it takes the row's lazy lock.
	for _, row := range busy {
		if b := capture.blocked[row.shared.id]; b.kind == "" && b.err != nil && errors.Is(b.err, ErrPersistStateNotReady) {
			b.kind = row.shared.busyKind()
			capture.blocked[row.shared.id] = b
		}
	}
	return stale, context.Cause(ctx)
}

// staleOffers are the row's offers on outputs its copied record completed.
func (row *capturedTransferRow) staleOffers() ([]staleOffer, error) {
	var stale []staleOffer
	for _, offer := range row.offers {
		key, err := partAddressKey(offer.Address)
		if err != nil {
			return stale, err
		}
		for _, out := range row.outputs {
			outKey, _ := partAddressKey(out.Address)
			if key == outKey && out.State != "pending" {
				stale = append(stale, staleOffer{row: row.shared, address: clonePartAddress(offer.Address), transferRev: row.transferRev})
			}
		}
	}
	return stale, nil
}

// checkCopied checks, under the graph lock, that each copied row still has
// the ownership, frame and expiry the walk saw, and each kept root the
// retention it had: blockRow blocks a row that changed, and a root whose
// expiry changed is left out.
func (capture *HeldCapturedClosure) checkCopied(copied []bool, blockRow func(*capturedTransferRow, error)) {
	c := capture.cache
	now := time.Now().Unix()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	for i, row := range capture.order {
		if !copied[i] {
			continue
		}
		res := row.shared
		if c.resultsByID[res.id] != res || res.attachmentState() != resultAttachmentClean || res.loadResultCall() != row.frame || res.expiresAtUnix != row.expiry || res.transferRevision != row.transferRev || res.dependencyOwnershipRevision != row.dependencyRev || res.requiredSessionResourcesGen.Load() != row.requiredRev {
			blockRow(row, fmt.Errorf("%w: row %d ownership changed", ErrPersistStateNotReady, res.id))
		}
	}
	for i := range capture.selected {
		root := &capture.selected[i]
		row := capture.rows[root.row.id]
		if root.err != nil || row == nil || row.ordinal == 0 {
			continue
		}
		expiry := row.shared.expiresAtUnix
		if edge, ok := c.persistedEdgesByResult[row.shared.id]; ok {
			expiry = edge.expiresAtUnix
		}
		if expiry != root.expiry || expiry != 0 && expiry <= now || row.expiry != 0 && row.expiry <= now {
			root.err = &blockedRow{row: row.shared.id, kind: BusyOther, err: fmt.Errorf("%w: root %d expiry changed", ErrPersistStateNotReady, row.shared.id)}
		}
	}
}

// busyKind is what keeps a row from being captured now, as its state says
// after a capture found it busy.
func (res *sharedResult) busyKind() BusyKind {
	if res.attachmentState() != resultAttachmentClean {
		return BusyAttaching
	}
	res.lazyMu.Lock()
	defer res.lazyMu.Unlock()
	groups := []*lazyGroupState{&res.lazyWhole}
	for _, group := range res.lazyPartGroups {
		groups = append(groups, group)
	}
	kind := BusyOther
	for _, group := range groups {
		switch {
		case group.attempt != nil && group.attempt.waiters > 0:
			return BusyRunning
		case group.attempt != nil:
			kind = BusyAbandoned
		case group.syncPending && kind == BusyOther:
			kind = BusyBookkeeping
		}
	}
	return kind
}

type SelectedChain struct {
	Ordinal  TransferOrdinal
	Address  PersistedPartAddress
	Layers   []snapshots.ExportLayer
	Provider content.InfoReaderProvider
}
type SelectedChains struct {
	Entries []SelectedChain
	// Stats sum what exporting the chains took.
	Stats  snapshots.ExportStats
	refs   []snapshots.ImmutableRef
	chains []*snapshots.ExportChain
	once   sync.Once
	err    error
}

func (chains *SelectedChains) Release(ctx context.Context) error {
	if chains == nil {
		return nil
	}
	chains.once.Do(func() {
		ctx = context.WithoutCancel(ctx)
		for _, chain := range chains.chains {
			chains.err = errors.Join(chains.err, chain.Release(ctx))
		}
		for _, ref := range chains.refs {
			chains.err = errors.Join(chains.err, ref.Release(ctx))
		}
	})
	return chains.err
}
func selectedCapturedOutput(capture *HeldCapturedClosure, selected SelectedValueOutput) (*capturedTransferRow, CapturedCodecOutput, error) {
	if selected.Result == nil || selected.Result.cacheSharedResult() == nil {
		return nil, CapturedCodecOutput{}, fmt.Errorf("detached selected output")
	}
	row := capture.rows[selected.Result.cacheSharedResult().id]
	if row == nil {
		return nil, CapturedCodecOutput{}, fmt.Errorf("output not captured")
	}
	id, address, err := resolveSelectedRecord(uint64(row.shared.id), selected.Address, func(id uint64) (PersistedRecord, bool) {
		row := capture.rows[sharedResultID(id)]
		if row == nil {
			return PersistedRecord{}, false
		}
		return row.record, true
	})
	if err != nil {
		return nil, CapturedCodecOutput{}, err
	}
	row = capture.rows[sharedResultID(id)]
	key, err := partAddressKey(address)
	if err != nil {
		return nil, CapturedCodecOutput{}, err
	}
	for _, out := range row.outputs {
		candidate, _ := partAddressKey(out.Address)
		if candidate == key {
			return row, out, nil
		}
	}
	return nil, CapturedCodecOutput{}, fmt.Errorf("row %d has no selected part %s", row.shared.id, key)
}
func OpenSelectedChains(ctx context.Context, capture *HeldCapturedClosure, outputs []SelectedValueOutput, cfg config.RefConfig) (_ *SelectedChains, rerr error) {
	chains := &SelectedChains{}
	defer func() {
		if rerr != nil {
			rerr = errors.Join(rerr, chains.Release(ctx))
		}
	}()
	var chainResults []uint64
	// opened holds each entry's chain: a File's, which travels as its own
	// content, as soon as it is exported; the others' once they open
	// together. batched indexes the entries opening together.
	var opened []*snapshots.ExportChain
	var batched []int
	var batchRefs []snapshots.ImmutableRef
	seen := map[string]bool{}
	for _, selected := range outputs {
		row, out, err := selectedCapturedOutput(capture, selected)
		if err != nil {
			return nil, err
		}
		key, _ := partAddressKey(out.Address)
		key = fmt.Sprintf("%d:%s", row.ordinal, key)
		if seen[key] {
			return nil, fmt.Errorf("duplicate selected output %s", key)
		}
		seen[key] = true
		if out.State != "completed" {
			continue
		}
		if capture.cache.snapshotManager == nil {
			return nil, fmt.Errorf("completed output has no snapshot manager")
		}
		ref, err := capture.cache.snapshotManager.GetBySnapshotID(ctx, out.SnapshotID, snapshots.NoUpdateLastUsed)
		if err != nil {
			return nil, err
		}
		chains.refs = append(chains.refs, ref)
		chainResults = append(chainResults, uint64(row.shared.id))
		chains.Entries = append(chains.Entries, SelectedChain{Ordinal: row.ordinal, Address: out.Address})
		// A File is its content: it travels as a chain of its own, not as
		// the filesystem it was taken from.
		if out.ValueKind == "file" && out.Value != nil {
			chainCtx, chainOp := profBeginChain(ctx, out.Address)
			chain, err := ref.ExportFileChain(chainCtx, cfg, out.Value.Path)
			var blobBytes int64
			if chain != nil {
				blobBytes = chain.Stats.BlobBytes
			}
			profEndChain(chainOp, blobBytes, uint64(row.shared.id), err)
			if err != nil {
				return nil, err
			}
			// Released with the others if a later output fails.
			chains.chains = append(chains.chains, chain)
			opened = append(opened, chain)
			continue
		}
		opened = append(opened, nil)
		batched = append(batched, len(chains.Entries)-1)
		batchRefs = append(batchRefs, ref)
	}
	// The other outputs' chains open as one export, so that their layers
	// build together and a layer they share builds once. Each chain's
	// profile op spans its own builds; the export's op spans the rest.
	var work *snapshots.ChainWork
	if wcprof.Enabled(ctx) {
		chainOps := make([]*wcprof.Op, len(batchRefs))
		work = &snapshots.ChainWork{
			Begin: func(ctx context.Context, i int) context.Context {
				chainCtx, op := profBeginChain(ctx, chains.Entries[batched[i]].Address)
				chainOps[i] = op
				return chainCtx
			},
			End: func(i int, blobBytes int64, err error) {
				profEndChain(chainOps[i], blobBytes, chainResults[batched[i]], err)
			},
		}
	}
	batch, err := snapshots.ExportChains(ctx, batchRefs, work, cfg)
	if err != nil {
		return nil, err
	}
	// From here the chains are the entries', in their order, as the
	// outputs read them.
	for i, entry := range batched {
		opened[entry] = batch[i]
	}
	chains.chains = opened
	for i, chain := range opened {
		chains.Stats.Add(chain.Stats)
		chains.Entries[i].Layers, chains.Entries[i].Provider = chain.Layers, chain.Provider
	}
	return chains, nil
}

// prefixServedByDelegation reports whether a part's route delegates to a
// parent part the bundle offers, keyed "ordinal:address" in offered: then
// delegation serves the part, and it gets no prefix output of its own. A part
// that delegates to a part the bundle doesn't offer still gets one.
func prefixServedByDelegation(route LazyOperationRoute, rows map[sharedResultID]*capturedTransferRow, offered map[string]bool) bool {
	if route.Delegation == nil {
		return false
	}
	parent := rows[sharedResultID(route.Delegation.ParentResultID)]
	if parent == nil {
		return false
	}
	key, err := partAddressKey(route.Delegation.Address)
	if err != nil {
		return false
	}
	return offered[fmt.Sprintf("%d:%s", parent.ordinal, key)]
}

type ExportedValues struct {
	Bundle ValueBundle
	Chains *SelectedChains
	// ChainTime is the wall time spent opening the selected chains, which
	// may compress a layer the first time it is exported.
	ChainTime time.Duration
	// Phases are where the capture's time went.
	Phases ExportPhases
	// Sources maps bundle ordinals to held source rows; it is not transported.
	Sources []ImportedValue
	// LeftOut are the selection's roots the bundle leaves out, in the
	// selection's order.
	LeftOut []LeftOutRoot
	// Rows are the rows the bundle carries, for KnownRows once the recipient
	// says whether it merged them.
	Rows ExportedRows
}

// LeftOutRoot is a selected root a capture left out because its closure
// reaches a row it couldn't capture: Index is the root's position in the
// selection's Roots, Row that row's number, and Err its error. A row that
// can't be captured now reports ErrPersistStateNotReady, and Kind says what
// keeps it busy; any other error refuses the root, with no Kind.
type LeftOutRoot struct {
	Index int
	Row   uint64
	Kind  BusyKind
	Err   error
	// Busy are all the busy rows the root's closure reaches, by number,
	// each with what keeps it busy: Row is one of them, when Kind is set.
	Busy []BusyRow
}

// BusyRow is a row a capture found busy, and what keeps it busy.
type BusyRow struct {
	Row  uint64
	Kind BusyKind
}

// BusyKind is what keeps a row from being captured now.
type BusyKind string

const (
	// BusyRunning: an evaluation of the row is running, with a caller
	// waiting for it.
	BusyRunning BusyKind = "running"
	// BusyAbandoned: an evaluation of the row is still running with no
	// caller waiting for it: canceled, and not yet returned.
	BusyAbandoned BusyKind = "abandoned"
	// BusyBookkeeping: the row's evaluation succeeded, but its cache-side
	// bookkeeping didn't; only a later evaluation retries it.
	BusyBookkeeping BusyKind = "bookkeeping"
	// BusyAttaching: the row's dependencies are still being attached.
	BusyAttaching BusyKind = "attaching"
	// BusyStaleOffer: the row still carries an offer for a part it
	// completed, after the capture retired such offers once.
	BusyStaleOffer BusyKind = "staleOffer"
	// BusyOther: anything else, such as a guard its value holds or a change
	// to the row during the capture.
	BusyOther BusyKind = "other"
)

// RootsLeftOutError is a capture's error when it left out every root of
// its selection: none was consumed.
type RootsLeftOutError struct {
	Roots []LeftOutRoot
}

func (err *RootsLeftOutError) Error() string {
	if len(err.Roots) == 1 {
		return err.Roots[0].Err.Error()
	}
	return fmt.Sprintf("all %d roots left out: %v", len(err.Roots), err.Roots[0].Err)
}

// Unwrap is every left-out root's error, so that errors.Is finds
// ErrPersistStateNotReady when any root was busy.
func (err *RootsLeftOutError) Unwrap() []error {
	errs := make([]error, len(err.Roots))
	for i, root := range err.Roots {
		errs[i] = root.Err
	}
	return errs
}

// ExportPhases are where a capture that succeeded spent its time.
type ExportPhases struct {
	// WalkWait is the wait for the cache's graph lock, and Walk the walk of
	// the closure under it, which blocks lookups and installs meanwhile.
	WalkWait time.Duration
	Walk     time.Duration
	// Copy is copying the closure's records, and Bundle checking them and
	// building the bundle of the kept roots' rows.
	Copy   time.Duration
	Bundle time.Duration
	// RetriedStale is 1 when obsolete offers were retired and the closure
	// captured again.
	RetriedStale int
}

// settle decides which selected roots the capture sends: those whose
// closure reaches no blocked row. Each other root is left out, with the
// blocked row it reaches; of several, the lowest-numbered. The closure then
// holds the kept roots' rows only, renumbered in dependency order, and the
// selected outputs on them. An output on a row only left-out roots reach is
// left out with them; one outside the closure is left out on request, and
// refuses the selection otherwise.
func (capture *HeldCapturedClosure) settle(selection ValueSelection) ([]LeftOutRoot, error) {
	// cause is the blocked row a row reaches, if any, memoized: rows share
	// most of their closures.
	causes := map[sharedResultID]*blockedRow{}
	var cause func(sharedResultID) *blockedRow
	cause = func(id sharedResultID) *blockedRow {
		if b, ok := capture.blocked[id]; ok {
			return &b
		}
		if c, done := causes[id]; done {
			return c
		}
		causes[id] = nil
		var found *blockedRow
		if row := capture.rows[id]; row != nil {
			for _, child := range row.children {
				if c := cause(child); c != nil && (found == nil || c.row < found.row) {
					found = c
				}
			}
		}
		causes[id] = found
		return found
	}
	var leftOut []LeftOutRoot
	keep := map[sharedResultID]bool{}
	var mark func(sharedResultID)
	mark = func(id sharedResultID) {
		if keep[id] {
			return
		}
		keep[id] = true
		for _, child := range capture.rows[id].children {
			mark(child)
		}
	}
	var kept []capturedRoot
	for i, root := range capture.selected {
		b := root.err
		if b == nil {
			b = cause(root.row.id)
		}
		if b != nil {
			leftOut = append(leftOut, LeftOutRoot{Index: i, Row: uint64(b.row), Kind: b.kind, Err: b.err, Busy: capture.busyBelow(root)})
			continue
		}
		mark(root.row.id)
		kept = append(kept, root)
	}
	order := make([]*capturedTransferRow, 0, len(keep))
	rows := make(map[sharedResultID]*capturedTransferRow, len(keep))
	for _, row := range capture.order {
		if keep[row.shared.id] {
			row.ordinal = TransferOrdinal(len(order) + 1)
			order = append(order, row)
			rows[row.shared.id] = row
		}
	}
	for id, row := range capture.rows {
		if !keep[id] {
			capture.dropped = append(capture.dropped, row)
		}
	}
	held := capture.rows
	capture.rows, capture.order = rows, order
	capture.roots = nil
	for _, root := range kept {
		capture.roots = append(capture.roots, TransferredRoot{Ordinal: rows[root.row.id].ordinal, ExpiresAtUnix: root.expiry})
	}
	return leftOut, capture.settleOutputs(selection, held)
}

// busyBelow are the busy rows a left-out root's closure reaches: the rows
// blocked by their own capture, with a busy kind, by number.
func (capture *HeldCapturedClosure) busyBelow(root capturedRoot) []BusyRow {
	var busy []BusyRow
	visited := map[sharedResultID]bool{}
	var visit func(sharedResultID)
	visit = func(id sharedResultID) {
		if visited[id] {
			return
		}
		visited[id] = true
		if b, ok := capture.blocked[id]; ok && b.row == id && b.kind != "" {
			busy = append(busy, BusyRow{Row: uint64(id), Kind: b.kind})
		}
		children := capture.unheld[id]
		if row := capture.rows[id]; row != nil {
			children = row.children
		}
		for _, child := range children {
			visit(child)
		}
	}
	if root.err != nil && root.err.kind != "" {
		busy = append(busy, BusyRow{Row: uint64(root.err.row), Kind: root.err.kind})
	}
	visit(root.row.id)
	slices.SortFunc(busy, func(x, y BusyRow) int { return cmp.Compare(x.Row, y.Row) })
	return slices.CompactFunc(busy, func(x, y BusyRow) bool { return x.Row == y.Row })
}

// settleOutputs keeps the selected outputs on the kept roots' rows. One on a
// row the walk held or blocked for left-out roots only is left out with
// them; one outside the closure is left out on request, and refuses the
// selection otherwise.
func (capture *HeldCapturedClosure) settleOutputs(selection ValueSelection, held map[sharedResultID]*capturedTransferRow) error {
	for _, output := range selection.Outputs {
		if output.Result == nil || output.Result.cacheSharedResult() == nil {
			return fmt.Errorf("selected output lies outside captured closure")
		}
		res := output.Result.cacheSharedResult()
		if row := capture.rows[res.id]; row != nil && row.shared == res {
			capture.outputs = append(capture.outputs, output)
			continue
		}
		if row := held[res.id]; row != nil && row.shared == res {
			continue
		}
		if _, blocked := capture.blocked[res.id]; blocked {
			continue
		}
		if !selection.LeaveOutOutputsOutsideClosure {
			return fmt.Errorf("selected output lies outside captured closure")
		}
	}
	return nil
}

// captureCopied holds the selected closure, with the rows carried names in
// full, and copies its records, adding the copy's time to phases. An offer it
// finds on an output the value already completed is obsolete: it retires the
// offer, as boot does, and captures once more; offers still obsolete then
// block their rows. A capture that fails is released.
func (c *Cache) captureCopied(ctx context.Context, selection ValueSelection, carried map[sharedResultID]bool, phases *ExportPhases) (*HeldCapturedClosure, error) {
	capture, err := c.holdTransferClosure(ctx, selection, carried)
	if err != nil {
		return nil, err
	}
	copyStart := time.Now()
	stale, err := capture.copy(ctx, true)
	copied := time.Now()
	phases.Copy += copied.Sub(copyStart)
	profCopy(ctx, copyStart, copied, err)
	if err == nil && len(stale) > 0 {
		// Retire the obsolete offers outside the capture's holds, then
		// capture once more.
		if err := errors.Join(capture.Release(ctx), c.retireStaleOffers(ctx, stale)); err != nil {
			return nil, err
		}
		capture, err = c.holdTransferClosure(ctx, selection, carried)
		if err != nil {
			return nil, err
		}
		phases.RetriedStale = 1
		copyStart = time.Now()
		_, err = capture.copy(ctx, false)
		copied = time.Now()
		phases.Copy += copied.Sub(copyStart)
		profCopy(ctx, copyStart, copied, err)
	}
	phases.WalkWait, phases.Walk = capture.walkWait, capture.walk
	if err != nil {
		return nil, errors.Join(err, capture.Release(ctx))
	}
	return capture, nil
}

// deriveHeldRecipes derives the recipe of each of the capture's held rows,
// and returns those whose recipe a row it carries in full shares. Deriving
// can read the cache, so it runs outside egraphMu; a frame's recipe is
// derived once.
func (capture *HeldCapturedClosure) deriveHeldRecipes() ([]sharedResultID, error) {
	held := map[digest.Digest][]sharedResultID{}
	for _, row := range capture.order {
		if !row.held {
			continue
		}
		recipe, err := row.frame.deriveRecipeDigest(capture.cache)
		if err != nil {
			return nil, fmt.Errorf("held row %d recipe: %w", row.shared.id, err)
		}
		row.recipe = recipe
		held[recipe] = append(held[recipe], row.shared.id)
	}
	if len(held) == 0 {
		return nil, nil
	}
	var shared []sharedResultID
	for _, row := range capture.order {
		if row.held {
			continue
		}
		recipe, err := row.frame.deriveRecipeDigest(capture.cache)
		if err != nil {
			return nil, fmt.Errorf("row %d recipe: %w", row.shared.id, err)
		}
		shared = append(shared, held[recipe]...)
		delete(held, recipe)
	}
	return shared, nil
}

// WithExportedValues captures the selected closure and lends it to consume.
// It is a capture and never waits. It leaves out each root whose closure
// reaches a row it can't capture now, one with a task in flight (an
// evaluation, a part installation, a sharing slot), or a row whose record it
// refuses, and lends consume the bundle of the others, naming the roots it
// left out (ExportedValues.LeftOut): the caller asks for those again later.
// With every root left out, it consumes nothing and returns a
// RootsLeftOutError. An offer it finds on an output the value already
// completed is obsolete: it retires the offer, as boot does, and captures
// once more. A selection of no outputs sends each row its Known knows as a
// held row, unless a row the bundle carries in full has its recipe.
//
//nolint:gocyclo // one step per export phase over the held closure; splitting hides the order of the steps
func (c *Cache) WithExportedValues(ctx context.Context, selection ValueSelection, cfg config.RefConfig, consume func(context.Context, *ExportedValues) error) (rerr error) {
	op, err := c.beginCacheOperation()
	if err != nil {
		return err
	}
	defer op.finish(false)
	if consume == nil {
		return fmt.Errorf("nil transfer consumer")
	}
	var phases ExportPhases
	capture, err := c.captureCopied(ctx, selection, nil, &phases)
	if err != nil {
		return err
	}
	defer func() { rerr = errors.Join(rerr, capture.Release(ctx)) }()
	// A held row whose recipe a row carried in full shares is carried in
	// full too, in a capture once more: a merge refuses a held row beside a
	// record of its recipe. Each pass carries more rows in full, so this
	// ends. It runs before the capture leaves roots out, so a row may go in
	// full beside a row that's then left out, which is only a full record
	// more.
	var carried map[sharedResultID]bool
	for {
		shared, err := capture.deriveHeldRecipes()
		if err != nil {
			return err
		}
		if len(shared) == 0 {
			break
		}
		if carried == nil {
			carried = map[sharedResultID]bool{}
		}
		for _, id := range shared {
			carried[id] = true
		}
		if err := capture.Release(ctx); err != nil {
			return err
		}
		if capture, err = c.captureCopied(ctx, selection, carried, &phases); err != nil {
			return err
		}
	}
	// Each copied record is checked as the bundle will carry it: its
	// references, its normalization and its payload. One that fails refuses
	// the roots that reach its row.
	bundleStart := time.Now()
	_, bundleOp := wcprof.BeginOp(ctx, wcprof.OpKindRemoteCache, "remotecache.export.bundle", wcprof.OpOpts{})
	for _, row := range capture.order {
		if _, blocked := capture.blocked[row.shared.id]; blocked || row.held {
			continue
		}
		local := row.record
		local.Envelope.PendingOffers = nil // Owner references have their own scope.
		if _, err := VisitEncodedReferences(local, func(ref *PersistedRef) error {
			if ref.RecipeID == nil && (ref.Kind == PersistedRefChild || ref.Kind == PersistedRefCall) && !slices.Contains(row.deps, ref.ResultID) {
				return fmt.Errorf("reference %s to %d is not a direct dependency", ref.Path, ref.ResultID)
			}
			return nil
		}); err != nil {
			capture.block(row.shared.id, "", fmt.Errorf("row %d: %w", row.shared.id, err))
			continue
		}
		normalized, err := normalizeTransferRecord(row.record)
		if err == nil {
			err = validateTransferRecord(normalized, row.deps)
		}
		if err != nil {
			capture.block(row.shared.id, BusyOther, fmt.Errorf("row %d: %w", row.shared.id, err))
			continue
		}
		row.transfer = normalized
	}
	leftOut, err := capture.settle(selection)
	if err == nil && len(capture.roots) == 0 {
		err = &RootsLeftOutError{Roots: leftOut}
	}
	if err != nil {
		bundleOp.EndErr(err)
		return err
	}
	relocate := func(ref *PersistedRef) error {
		if ref.RecipeID != nil || ref.ResultID == 0 {
			return nil
		}
		row := capture.rows[sharedResultID(ref.ResultID)]
		if row == nil {
			return fmt.Errorf("reference %d outside captured closure", ref.ResultID)
		}
		ref.ResultID = uint64(row.ordinal)
		return nil
	}
	bundle := ValueBundle{Version: valueBundleVersion, Roots: capture.roots}
	for _, row := range capture.order {
		if row.held {
			bundle.Values = append(bundle.Values, TransferredValue{Ordinal: row.ordinal, SenderNumber: uint64(row.shared.id), SenderReplacements: row.replacements, Record: PersistedRecord{ResultID: uint64(row.ordinal)}, ExpiresAtUnix: row.expiry, Held: row.recipe})
			continue
		}
		rec, err := VisitEncodedReferences(row.transfer, relocate)
		if err != nil {
			bundleOp.EndErr(err)
			return err
		}
		value := TransferredValue{Ordinal: row.ordinal, SenderNumber: uint64(row.shared.id), SenderReplacements: row.replacements, Record: rec, ExpiresAtUnix: row.expiry}
		for _, id := range row.deps {
			value.DependencyIDs = append(value.DependencyIDs, uint64(capture.rows[sharedResultID(id)].ordinal))
		}
		bundle.Values = append(bundle.Values, value)
	}
	bundleOp.End(wcprof.OutcomeOK)
	phases.Bundle = time.Since(bundleStart)
	chainStart := time.Now()
	chainsCtx, chainsOp := wcprof.BeginOp(ctx, wcprof.OpKindRemoteCache, "remotecache.export.chains", wcprof.OpOpts{})
	chains, err := OpenSelectedChains(chainsCtx, capture, capture.outputs, cfg)
	chainsOp.EndErr(err)
	chainTime := time.Since(chainStart)
	if err != nil {
		return err
	}
	defer func() { rerr = errors.Join(rerr, chains.Release(ctx)) }()
	// withOwner gives an output with a chain the owner of its offer: the
	// services it binds and the session resources its row's closure needs,
	// relocated to ordinals.
	withOwner := func(row *capturedTransferRow, output *TransferredOutput) error {
		owner := PersistedOfferOwner{}
		owned := map[uint64]bool{}
		for _, service := range output.Value.Services {
			owned[service.ServiceResultID] = true
		}
		visited := map[sharedResultID]bool{}
		var ownWalk func(*capturedTransferRow)
		ownWalk = func(row *capturedTransferRow) {
			if visited[row.shared.id] {
				return
			}
			visited[row.shared.id] = true
			if row.shared.sessionResourceHandle != "" {
				owned[uint64(row.shared.id)] = true
			}
			for _, dep := range row.deps {
				ownWalk(capture.rows[sharedResultID(dep)])
			}
		}
		ownWalk(row)
		for id := range owned {
			owner.DependencyIDs = append(owner.DependencyIDs, id)
		}
		slices.Sort(owner.DependencyIDs)
		offer := PersistedPartOffer{Address: output.Address, Value: *output.Value, Owner: owner}
		if err := visitPersistedPartOffer(&offer, nil, relocate); err != nil {
			return err
		}
		output.Value, output.Owner = &offer.Value, &offer.Owner
		return nil
	}
	hasOffer := func(row *capturedTransferRow, key string) bool {
		for _, offer := range row.offers {
			if offerKey, _ := partAddressKey(offer.Address); key == offerKey {
				return true
			}
		}
		return false
	}
	selected := map[string]bool{}
	// carriedBy names, for each of the chains, the bundle's output that
	// carries it, if one does: a prefix of the chain names it.
	carriedBy := make([]*ChainPrefix, len(chains.Entries))
	for _, sel := range capture.outputs {
		row, out, err := selectedCapturedOutput(capture, sel)
		if err != nil {
			return err
		}
		key, _ := partAddressKey(out.Address)
		selected[fmt.Sprintf("%d:%s", row.ordinal, key)] = true
		if hasOffer(row, key) {
			continue
		}
		output := TransferredOutput{Ordinal: row.ordinal, Address: out.Address, State: out.State, Value: out.Value}
		for i, entry := range chains.Entries {
			entryKey, _ := partAddressKey(entry.Address)
			if entry.Ordinal == row.ordinal && entryKey == key {
				output.Chain = &OfferedChain{Layers: entry.Layers}
				carriedBy[i] = &ChainPrefix{Ordinal: row.ordinal, Address: out.Address}
				break
			}
		}
		if output.Chain != nil {
			if err := withOwner(row, &output); err != nil {
				return err
			}
		} else if output.Value != nil {
			value := *output.Value
			value.Services = slices.Clone(value.Services)
			for i := range value.Services {
				id := value.Services[i].ServiceResultID
				ref := PersistedRef{ResultID: id}
				if err := relocate(&ref); err != nil {
					return err
				}
				value.Services[i].ServiceResultID = ref.ResultID
			}
			output.Value = &value
		}
		bundle.Outputs = append(bundle.Outputs, output)
	}
	// Every other complete part of the closure whose snapshot a selected
	// chain walked is a prefix of that chain (design 6.1): an output too,
	// whose layers the selected chain already carries, so it opens no
	// snapshot and needs no upload of its own. It names the first such chain
	// an output of the bundle carries, instead of carrying the layers again;
	// when none does, it carries the first one's layers itself. A part whose
	// route delegates to a parent part the bundle offers gets none:
	// delegation serves it. Parents come before their children in the
	// capture's order.
	offered := map[string]bool{}
	for _, output := range bundle.Outputs {
		if output.Chain != nil {
			key, _ := partAddressKey(output.Address)
			offered[fmt.Sprintf("%d:%s", output.Ordinal, key)] = true
		}
	}
	for _, row := range capture.order {
		for _, out := range row.outputs {
			key, _ := partAddressKey(out.Address)
			if out.State != "completed" || out.SnapshotID == "" || out.Value == nil || selected[fmt.Sprintf("%d:%s", row.ordinal, key)] || hasOffer(row, key) {
				continue
			}
			if route, err := routePartRecord(row.record, out.Address); err == nil && prefixServedByDelegation(route, capture.rows, offered) {
				continue
			}
			var layers []snapshots.ExportLayer
			var prefix *ChainPrefix
			for i, entry := range chains.Entries {
				n := chains.chains[i].Prefixes[out.SnapshotID]
				if n == 0 {
					continue
				}
				if carriedBy[i] != nil {
					prefix = &ChainPrefix{Ordinal: carriedBy[i].Ordinal, Address: carriedBy[i].Address, Layers: n}
					break
				}
				if layers == nil {
					layers = entry.Layers[:n]
				}
			}
			if prefix == nil && layers == nil {
				continue
			}
			output := TransferredOutput{Ordinal: row.ordinal, Address: out.Address, State: out.State, Value: out.Value, Prefix: prefix}
			if prefix == nil {
				output.Chain = &OfferedChain{Layers: layers}
			}
			if err := withOwner(row, &output); err != nil {
				return err
			}
			bundle.Outputs = append(bundle.Outputs, output)
			offered[fmt.Sprintf("%d:%s", row.ordinal, key)] = true
		}
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	sources := make([]ImportedValue, len(capture.order))
	rows := ExportedRows{Full: make(map[uint64]RowRevision, len(capture.order))}
	for i, row := range capture.order {
		sources[i] = ImportedValue{Ordinal: row.ordinal, ResultID: uint64(row.shared.id)}
		if row.held {
			rows.Held = append(rows.Held, uint64(row.shared.id))
			continue
		}
		rows.Full[uint64(row.shared.id)] = RowRevision{Replacements: row.replacements, Transfer: row.transferRev, Dependencies: row.dependencyRev}
	}
	profExportedParts(ctx, bundle, sources)
	return consume(ctx, &ExportedValues{Bundle: bundle, Chains: chains, ChainTime: chainTime, Phases: phases, Sources: sources, LeftOut: leftOut, Rows: rows})
}
