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
	"golang.org/x/sync/errgroup"
)

type capturedTransferRow struct {
	shared                                  *sharedResult
	version                                 capturedRowRevision
	imported                                bool
	frame                                   *ResultCall
	deps                                    []uint64
	transferRev, dependencyRev, requiredRev uint64
	// replacements is the entry's replacement count, read with the rest of
	// the row; a replacement also moves transferRev.
	replacements uint64
	expiry       int64
	record       PersistedRecord
	outputs      []CapturedCodecOutput
	ordinal      TransferOrdinal
	// children are the rows the walk reached from this one: its
	// dependencies.
	children []sharedResultID
	// transfer is the record as the bundle carries it, naming rows by the
	// engine's entry numbers.
	transfer PersistedRecord
	// held makes the row a held row, of recipe: the selection's recipient
	// knows it, so the bundle carries no record for it and nothing below it.
	held   bool
	recipe digest.Digest
	// knownHeld marks a row of an export that selects parts held after its
	// walk (holdKnownRows), whose links to the rows below it go with it.
	knownHeld bool
	// copied is set once record, outputs and version hold the row's copy. A
	// held row's record is copied only where the bundle reads it: its parts
	// (copy, copyLate).
	copied bool
}

// HeldCapturedClosure protects every exact source row while storage opens and
// provider consumption run outside graph and value locks. It holds no offer:
// a transferred record carries none, so an offer landing on a row of the
// closure, or retired from it, changes nothing the capture copies.
type HeldCapturedClosure struct {
	cache *Cache
	// rows and order are the rows of the closure, in dependency order; once
	// the capture has left out the roots it can't send (settle), only the
	// kept roots' rows.
	rows  map[sharedResultID]*capturedTransferRow
	order []*capturedTransferRow
	roots []TransferredRoot
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
	// unwalked is set when a root was left out before its walk, its own
	// evaluation in flight (holdTransferClosure): an output below it lies
	// outside what the capture walked.
	unwalked bool
	// early are the held rows whose records the copy copies though nothing
	// may read them, as it copies every row it doesn't hold: those an
	// earlier pass found changed when it copied them late (copyLate).
	early map[sharedResultID]bool
	// copiedRows counts the records copied, and unpredicted those of them
	// that name a snapshot their row's owner links don't
	// (countUnpredictedLinks).
	copiedRows, unpredicted int
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
		c.lockGraphForCtx(ctx, workExport)
		var queue collectionQueue
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
// names it, as a held row, with nothing below it. A selection of outputs
// walks and copies every row, since its parts and their owners are read
// from the records below the rows it holds (holdKnownRows).
//
//nolint:gocyclo // one classification per held result kind; splitting hides the order of the checks
func (c *Cache) holdTransferClosure(ctx context.Context, selection ValueSelection, carried map[sharedResultID]bool) (*HeldCapturedClosure, error) {
	capture := &HeldCapturedClosure{cache: c, rows: map[sharedResultID]*capturedTransferRow{}, blocked: map[sharedResultID]blockedRow{}, unheld: map[sharedResultID][]sharedResultID{}}
	known := selection.Known
	if len(selection.Outputs) > 0 {
		known = nil
	}
	// A root whose own evaluation is in flight can't be captured now,
	// whatever its closure holds, unless it goes as a held row: its record's
	// copy is refused
	// (lazyEvaluationInFlightLocked). It's left out before the walk, so
	// nothing below it is walked or copied for it: a remote cache service
	// asks for a running root every few seconds until it isn't busy, and
	// each capture walked and copied its whole closure only to leave it
	// out. Its lazy lock is taken here, before egraphMu, as the copy takes
	// it outside the walk. An evaluation that ends meanwhile leaves the root
	// out once more, as one that ends during a walk does.
	running := map[*sharedResult]blockedRow{}
	for _, root := range selection.Roots {
		if root == nil {
			continue
		}
		res := root.cacheSharedResult()
		if res == nil {
			continue
		}
		res.lazyMu.Lock()
		err := lazyEvaluationInFlightLocked(res)
		res.lazyMu.Unlock()
		if err != nil {
			running[res] = blockedRow{row: res.id, kind: res.busyKind(), err: err}
		}
	}
	lockStart := time.Now()
	c.lockGraphForCtx(ctx, workExport)
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
			for id := range res.deps {
				children = append(children, id)
				_ = walk(id, c.resultsByID[id])
			}
			capture.unheld[res.id] = children
			return blocked(res.id, blockedRow{row: res.id, kind: BusyAttaching, err: fmt.Errorf("%w: result %d attachment", ErrPersistStateNotReady, res.id)})
		}
		frame := res.loadResultCall()
		if frame == nil || frame.Type == nil || frame.Type.NamedType == "Query" {
			return blocked(res.id, blockedRow{row: res.id, err: fmt.Errorf("transfer capture: row %d has no transferable frame", res.id)})
		}
		// A value bound to its session (BindToSession) is no use to another
		// cache, where its session doesn't exist, and what is built on it
		// shares its binding: the roots that reach it stay here.
		if res.boundSession != "" {
			return blocked(res.id, blockedRow{row: res.id, err: fmt.Errorf("transfer capture: row %d is bound to its session", res.id)})
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
		c.incrementIncomingOwnershipLocked(ctx, res)
		row := &capturedTransferRow{shared: res, imported: res.imported, frame: frame, expiry: res.expiresAtUnix, replacements: res.replacements, transferRev: res.transferRevision, dependencyRev: res.dependencyOwnershipRevision, requiredRev: res.requiredSessionResourcesGen.Load()}
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
		// A row's children here are its dependencies alone: a transferred
		// record carries no offers, so neither their owners nor what those
		// own go with it.
		for id := range res.deps {
			below(id, c.resultsByID[id])
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
		// A root the selection's Known knows goes as a held row, whose
		// record isn't copied (walk): its evaluation keeps it from nothing.
		if b, ok := running[res]; ok && res.id != 0 && c.resultsByID[res.id] == res && (carried[res.id] || !known.knowsLocked(res)) {
			capture.blocked[res.id] = b
			capture.unwalked = true
			selected.err = &b
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
		wcprof.RecordOpAt(ctx, wcprof.OpKindRemoteCache, "remotecache.export.walk", wcprof.OpOpts{}, walkStart, walkStart.Add(capture.walk), profErrOutcome(err))
	}
	if err != nil {
		return nil, errors.Join(err, capture.Release(ctx))
	}
	return capture, nil
}

// copy copies the record of each walked row the bundle reads, without its
// offers, and checks that it still describes its row. It reads every row it
// carries in full, and of a held row only its parts: the rows on a selected
// output's way down (resolveSelectedRecord) are copied here, and the rows
// holding a snapshot a selected chain walks once the chains are open
// (copyLate). Every other held row is checked as a copied one is, but for
// its record: its evaluation isn't in flight, and checkCopied finds it as
// the walk did. A row it can't copy now, or refuses, or that changed
// meanwhile, is blocked, and copy goes on with the others; only the
// capture's own cancellation fails it.
func (capture *HeldCapturedClosure) copy(ctx context.Context, selection ValueSelection) error {
	cp := &closureCopy{capture: capture, ctx: ctx, copied: make([]bool, len(capture.order)), index: make(map[sharedResultID]int, len(capture.order))}
	for i, row := range capture.order {
		cp.index[row.shared.id] = i
	}
	selectedRows := make(map[sharedResultID]bool, len(selection.Outputs))
	for _, output := range selection.Outputs {
		if output.Result != nil && output.Result.cacheSharedResult() != nil {
			selectedRows[output.Result.cacheSharedResult().id] = true
		}
	}
	for i, row := range capture.order {
		switch {
		case row.held && !row.knownHeld:
			// Held at the walk: no record to copy; checkCopied still checks
			// that it hasn't changed since the walk.
			cp.copied[i] = true
		case row.knownHeld && !selectedRows[row.shared.id] && !capture.early[row.shared.id]:
			if err := row.shared.evaluationInFlight(); err != nil {
				cp.block(row, err)
				continue
			}
			cp.copied[i] = true
		default:
			if !cp.row(i) && ctx.Err() != nil {
				return context.Cause(ctx)
			}
		}
	}
	// A selected output's way down from its row goes through the records
	// below it: those are read for it, as its row's is.
	for _, output := range selection.Outputs {
		if output.Result == nil || output.Result.cacheSharedResult() == nil {
			continue
		}
		_, _, _ = resolveSelectedRecord(uint64(output.Result.cacheSharedResult().id), output.Address, cp.record)
		if err := context.Cause(ctx); err != nil {
			return err
		}
	}
	// Release each row's copy guards before comparing the next row.
	for i, row := range capture.order {
		if !cp.copied[i] || !row.copied {
			continue
		}
		if err := row.version.check(row.shared); err != nil {
			cp.block(row, err)
		}
	}
	capture.checkCopied(cp.copied, cp.block)
	// What keeps each busy row busy is read once no capture lock is held:
	// it takes the row's lazy lock.
	for _, row := range cp.busy {
		if b := capture.blocked[row.shared.id]; b.kind == "" && b.err != nil && errors.Is(b.err, ErrPersistStateNotReady) {
			b.kind = row.shared.busyKind()
			capture.blocked[row.shared.id] = b
		}
	}
	return context.Cause(ctx)
}

// closureCopy is one copy of a capture's closure: the rows of its order it
// copied or checked as copied, by position, and the busy rows it blocked.
type closureCopy struct {
	capture *HeldCapturedClosure
	ctx     context.Context
	copied  []bool
	index   map[sharedResultID]int
	busy    []*capturedTransferRow
}

// block blocks a row the copy can't capture now, or refuses. A blocked row
// isn't held: it stays blocked, as holdKnownRows leaves it.
func (cp *closureCopy) block(row *capturedTransferRow, err error) {
	cp.capture.block(row.shared.id, "", err)
	if row.knownHeld {
		row.held, row.knownHeld = false, false
	}
	if errors.Is(err, ErrPersistStateNotReady) {
		cp.busy = append(cp.busy, row)
	}
}

// row copies the record of the row at i, and reports whether it did. One it
// can't copy is blocked, unless the capture was cancelled.
func (cp *closureCopy) row(i int) bool {
	capture := cp.capture
	row := capture.order[i]
	rec, err := capture.cache.captureHeldPersistedRecord(cp.ctx, row.shared, row.imported, nil, &row.version)
	if err != nil {
		cp.copied[i] = false
		if cp.ctx.Err() == nil {
			cp.block(row, err)
		}
		return false
	}
	row.record = rec
	if hook := capture.cache.testTransferCopied; hook != nil {
		hook(uint64(row.shared.id))
	}
	row.outputs, err = mapTransferredOutputs(rec)
	if err != nil {
		cp.block(row, err)
		cp.copied[i] = false
		return false
	}
	row.copied = true
	capture.copiedRows++
	capture.countUnpredictedLinks(row)
	cp.copied[i] = true
	return true
}

// record is the record of the row numbered id, copied now if the copy held
// it without reading it, for a selected output's way down
// (resolveSelectedRecord).
func (cp *closureCopy) record(id uint64) (PersistedRecord, bool) {
	row := cp.capture.rows[sharedResultID(id)]
	if row == nil {
		return PersistedRecord{}, false
	}
	if !row.copied {
		i, ok := cp.index[row.shared.id]
		if !ok || !cp.copied[i] || !cp.row(i) {
			return PersistedRecord{}, false
		}
	}
	return row.record, true
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
		if c.rowMovedLocked(row) {
			blockRow(row, fmt.Errorf("%w: row %d ownership changed", ErrPersistStateNotReady, row.shared.id))
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

// evaluationInFlight refuses a capture of res while an evaluation of it or
// of a part of it runs, as its copy does (captureHeldPersistedRecord), for a
// held row whose record isn't read.
func (res *sharedResult) evaluationInFlight() error {
	res.lazyMu.Lock()
	defer res.lazyMu.Unlock()
	return lazyEvaluationInFlightLocked(res)
}

// ownedSnapshotLinks are the snapshots res holds, as its copy names them
// without encoding it (capturePartRecord): its owner links, which hold
// every snapshot it holds, borrowed ones too (syncResultSnapshotLeasesLocked),
// or the links its pending intent sets. visit stops at its first true.
func (res *sharedResult) ownedSnapshotLinks(visit func(PersistedSnapshotRefLink) bool) bool {
	res.payloadMu.RLock()
	defer res.payloadMu.RUnlock()
	links := res.snapshotOwnerLinks
	if res.snapshotLinkIntent != nil {
		links = res.snapshotLinkIntent.Links
	}
	for _, link := range links {
		if visit(link) {
			return true
		}
	}
	return false
}

// countUnpredictedLinks counts a copied row whose record names a snapshot its
// owner links don't: a held row like it would hold that snapshot's parts
// unread, since the capture reads a held row's record for its prefixes only
// when its owner links name a walked snapshot (copyLate). It should never
// count.
func (capture *HeldCapturedClosure) countUnpredictedLinks(row *capturedTransferRow) {
	for _, link := range row.record.SnapshotLinks {
		if link.RefKey == "" {
			continue
		}
		if !row.shared.ownedSnapshotLinks(func(owned PersistedSnapshotRefLink) bool { return owned.RefKey == link.RefKey }) {
			capture.unpredicted++
			return
		}
	}
}

// copyLate copies, once the selected chains are open, the record of a held
// row whose parts the bundle reads because it holds a snapshot a selected
// chain walked, and checks that the row is still as the walk found it, as
// copy and checkCopied check a row they copy. A row that changed, or can't
// be copied now, returns its error; the capture then runs again with the
// row copied in its copy (early), where a change blocks the roots that
// reach it.
func (capture *HeldCapturedClosure) copyLate(ctx context.Context, row *capturedTransferRow) error {
	c := capture.cache
	rec, err := c.captureHeldPersistedRecord(ctx, row.shared, row.imported, nil, &row.version)
	if err != nil {
		return err
	}
	if hook := c.testTransferCopied; hook != nil {
		hook(uint64(row.shared.id))
	}
	if hook := c.testCopyLate; hook != nil && hook(row.shared) {
		return nil
	}
	outputs, err := mapTransferredOutputs(rec)
	if err != nil {
		return err
	}
	if err := row.version.check(row.shared); err != nil {
		return err
	}
	c.egraphMu.RLock()
	moved := c.rowMovedLocked(row)
	c.egraphMu.RUnlock()
	if moved {
		return fmt.Errorf("%w: row %d ownership changed", ErrPersistStateNotReady, row.shared.id)
	}
	row.record, row.outputs, row.copied = rec, outputs, true
	capture.copiedRows++
	capture.countUnpredictedLinks(row)
	return nil
}

// rowMovedLocked reports whether a walked row no longer has the
// registration, attachment, frame, expiry or revisions the walk saw.
// Requires egraphMu.
func (c *Cache) rowMovedLocked(row *capturedTransferRow) bool {
	res := row.shared
	return c.resultsByID[res.id] != res || res.attachmentState() != resultAttachmentClean || res.loadResultCall() != row.frame || res.expiresAtUnix != row.expiry || res.transferRevision != row.transferRev || res.dependencyOwnershipRevision != row.dependencyRev || res.requiredSessionResourcesGen.Load() != row.requiredRev
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
		if row == nil || !row.copied {
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
	// opened holds each entry's chain. files are the entries of Files,
	// each of which travels as its own content in a chain of its own;
	// batched indexes the others, which open as one export.
	var opened []*snapshots.ExportChain
	type fileEntry struct {
		entry int
		ref   snapshots.ImmutableRef
		path  string
	}
	var files []fileEntry
	// sameFile is each File entry's that is the same path in the same
	// snapshot as an earlier one, by entry: it shares that one's chain.
	sameFile := map[int]int{}
	firstFile := map[[2]string]int{}
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
		opened = append(opened, nil)
		// A File is its content: it travels as a chain of its own, not as
		// the filesystem it was taken from.
		if out.ValueKind == "file" && out.Value != nil {
			entry := len(chains.Entries) - 1
			if first, ok := firstFile[[2]string{out.SnapshotID, out.Value.Path}]; ok {
				sameFile[entry] = first
				continue
			}
			firstFile[[2]string{out.SnapshotID, out.Value.Path}] = entry
			files = append(files, fileEntry{entry: entry, ref: ref, path: out.Value.Path})
			continue
		}
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
	// The Files' chains build at once with that export and with each
	// other, up to the layer builds at once a chain may run: each is a
	// snapshot of its own, whose one layer builds under the same gate as
	// the export's. Every chain opened is the entry's from here, so a
	// failure releases it with the others.
	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		batch, err := snapshots.ExportChains(egCtx, batchRefs, work, cfg)
		if err != nil {
			return err
		}
		for i, entry := range batched {
			opened[entry] = batch[i]
		}
		return nil
	})
	eg.Go(func() error {
		fileGroup, fileCtx := errgroup.WithContext(egCtx)
		fileGroup.SetLimit(int(max(int64(cfg.Builds.Workers), 1)))
		for _, file := range files {
			fileGroup.Go(func() error {
				_, unlabel := wcprof.WithCPUKind(fileCtx, wcprof.CPUKindExportLayers)
				defer unlabel()
				// One still waiting for a slot when another fails doesn't
				// begin.
				if err := fileCtx.Err(); err != nil {
					return err
				}
				chainCtx, chainOp := profBeginChain(fileCtx, chains.Entries[file.entry].Address)
				chain, err := file.ref.ExportFileChain(chainCtx, cfg, file.path)
				var blobBytes int64
				if chain != nil {
					blobBytes = chain.Stats.BlobBytes
				}
				profEndChain(chainOp, blobBytes, chainResults[file.entry], err)
				opened[file.entry] = chain
				return err
			})
		}
		return fileGroup.Wait()
	})
	err := eg.Wait()
	// From here the chains are the entries', in their order, as the
	// outputs read them. A chain two entries share releases once, and
	// counts once in the stats.
	for entry, first := range sameFile {
		opened[entry] = opened[first]
	}
	chains.chains = opened
	if err != nil {
		return nil, err
	}
	for i, chain := range opened {
		if _, shared := sameFile[i]; !shared {
			chains.Stats.Add(chain.Stats)
		}
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
	// LeftOutHeld counts the known rows the bundle leaves out, nothing in it
	// standing on them (keepNeededRows), and KeptEveryRow is why it left out
	// none though it could have: a bug, empty but for one.
	LeftOutHeld  int
	KeptEveryRow string
	// CopiedRows counts the records the capture copied over its passes:
	// those of the rows it carries in full, and of the held rows whose parts
	// it reads. UnpredictedLinks counts those of them that name a snapshot
	// their row's owner links don't, which should never happen (copyLate),
	// and LateMoved the held rows a late copy found changed since their
	// walk, each of which cost the export a pass.
	CopiedRows, UnpredictedLinks, LateMoved int
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

// BusyRow is a row a capture found busy, what keeps it busy, and the
// capture's own error for it: which check found it busy, such as a change to
// the row during the capture.
type BusyRow struct {
	Row  uint64
	Kind BusyKind
	Err  error
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
	// BusyOther: anything else, such as a guard its value holds or a change
	// to the row during the capture.
	BusyOther BusyKind = "other"
)

// RootsLeftOutError is a capture's error when it left out every root of
// its selection, or any root of a selection of EveryRoot: none was
// consumed.
type RootsLeftOutError struct {
	Roots []LeftOutRoot
}

func (err *RootsLeftOutError) Error() string {
	if len(err.Roots) == 1 {
		return err.Roots[0].Err.Error()
	}
	return fmt.Sprintf("%d roots left out: %v", len(err.Roots), err.Roots[0].Err)
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
	// the closure under it, which blocks lookups and installs meanwhile,
	// each summed over the captures. Every phase sums every pass, those an
	// export ran again included (copyLate).
	WalkWait time.Duration
	Walk     time.Duration
	// Copy is copying the closure's records, and Bundle checking them and
	// building the bundle of the kept roots' rows.
	Copy   time.Duration
	Bundle time.Duration
	// The bundle phase's steps, summed over its passes: holding the known
	// rows, checking the copied records, settling the roots, deriving the
	// held rows' recipes, and building the bundle's values. BundlePasses
	// counts the captures: each one after the first carries in full the
	// held rows whose recipe a row of the bundle shares, and copies the
	// closure again.
	BundleKnown   time.Duration
	BundleCheck   time.Duration
	BundleSettle  time.Duration
	BundleRecipes time.Duration
	BundleValues  time.Duration
	BundlePasses  int
	// BundleKeep is leaving out of the built bundle the known rows nothing
	// in it stands on (keepNeededRows), after its outputs and chains.
	BundleKeep time.Duration
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
			busy = append(busy, BusyRow{Row: uint64(id), Kind: b.kind, Err: b.err})
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
		busy = append(busy, BusyRow{Row: uint64(root.err.row), Kind: root.err.kind, Err: root.err.err})
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
		// One below a root left out before its walk lies outside the
		// walk, and is left out with it.
		if !selection.LeaveOutOutputsOutsideClosure && !capture.unwalked {
			return fmt.Errorf("selected output lies outside captured closure")
		}
	}
	return nil
}

// captureCopied holds the selected closure, with the rows carried names in
// full, holds the rows its recipient knows (holdKnownRows), and copies the
// records it reads, those of early too, adding the copy's time to phases. A
// capture that fails is released.
func (c *Cache) captureCopied(ctx context.Context, selection ValueSelection, carried, early map[sharedResultID]bool, phases *ExportPhases) (*HeldCapturedClosure, error) {
	capture, err := c.holdTransferClosure(ctx, selection, carried)
	if err != nil {
		return nil, err
	}
	capture.early = early
	knownStart := time.Now()
	capture.holdKnownRows(selection, carried)
	copyStart := time.Now()
	phases.BundleKnown += copyStart.Sub(knownStart)
	err = capture.copy(ctx, selection)
	copied := time.Now()
	phases.Copy += copied.Sub(copyStart)
	profCopy(ctx, copyStart, copied, err)
	phases.WalkWait += capture.walkWait
	phases.Walk += capture.walk
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

// holdKnownRows makes each walked row of an export that selects outputs a
// held row when the selection's Known knows it, as the walk found it, and
// carried doesn't name it. A held row carries no record in the bundle, but
// keeps its links to the rows below it, which go with it, and its parts: a
// selected part, a chain prefix and an owner sit on it or name it as they
// would on the row in full, and the Cloud checks its parts against its own
// copy of the row. So the copy reads a held row's record only for its parts
// (copy). A blocked row isn't held: it stays blocked.
func (capture *HeldCapturedClosure) holdKnownRows(selection ValueSelection, carried map[sharedResultID]bool) {
	known := selection.Known
	if known == nil || len(selection.Outputs) == 0 {
		return
	}
	for _, row := range capture.order {
		id := row.shared.id
		if row.held || carried[id] {
			continue
		}
		if _, blocked := capture.blocked[id]; blocked {
			continue
		}
		if !known.Knows(uint64(id), RowRevision{Replacements: row.replacements, Transfer: row.transferRev, Dependencies: row.dependencyRev}) {
			continue
		}
		row.held, row.knownHeld = true, true
	}
}

// checkRecords checks each copied record as the bundle will carry it: its
// references, its normalization and its payload, the normalized payloads'
// references walked once (normalizeCheckedTransferRecord). One that fails
// refuses the roots that reach its row.
func (capture *HeldCapturedClosure) checkRecords() {
	for _, row := range capture.order {
		if _, blocked := capture.blocked[row.shared.id]; blocked || row.held {
			continue
		}
		if _, err := VisitEncodedReferences(row.record, func(ref *PersistedRef) error {
			if ref.RecipeID == nil && (ref.Kind == PersistedRefChild || ref.Kind == PersistedRefCall) && !slices.Contains(row.deps, ref.ResultID) {
				return fmt.Errorf("reference %s to %d is not a direct dependency", ref.Path, ref.ResultID)
			}
			return nil
		}); err != nil {
			capture.block(row.shared.id, "", fmt.Errorf("row %d: %w", row.shared.id, err))
			continue
		}
		normalized, err := normalizeCheckedTransferRecord(row.record, row.deps)
		if err != nil {
			capture.block(row.shared.id, BusyOther, fmt.Errorf("row %d: %w", row.shared.id, err))
			continue
		}
		row.transfer = normalized
	}
}

// WithExportedValues captures the selected closure and lends it to consume.
// It is a capture and never waits. It leaves out each root whose closure
// reaches a row it can't capture now, one with a task in flight (an
// evaluation or a part installation), or a row whose record it
// refuses, and lends consume the bundle of the others, naming the roots it
// left out (ExportedValues.LeftOut): the caller asks for those again later.
// With every root left out, or any root of a selection of EveryRoot, it
// consumes nothing and returns a RootsLeftOutError. It sends each row its
// Known knows as a held row, unless a row
// the bundle carries in full has its recipe; in a selection of outputs, a
// held row keeps its links to the rows below it and its parts
// (holdKnownRows), and its record is read only for its parts (copy).
func (c *Cache) WithExportedValues(ctx context.Context, selection ValueSelection, cfg config.RefConfig, consume func(context.Context, *ExportedValues) error) error {
	ctx = withCacheWork(ctx, workExport)
	op, err := c.beginCacheOperation()
	if err != nil {
		return err
	}
	defer op.finish(false)
	if consume == nil {
		return fmt.Errorf("nil transfer consumer")
	}
	// A held row whose parts the bundle reads once the chains are open, and
	// that changed since the walk, is copied in the copy of the export's
	// next pass, as every row it reads otherwise is: a change found there
	// blocks the roots that reach it (copyLate). Each pass copies more rows
	// there, so this ends.
	var passes exportPasses
	for {
		moved, err := c.exportPass(ctx, selection, cfg, &passes, consume)
		if err != nil || len(moved) == 0 {
			return err
		}
		if passes.early == nil {
			passes.early = map[sharedResultID]bool{}
		}
		for _, id := range moved {
			passes.early[id] = true
		}
		passes.lateMoved += len(moved)
	}
}

// exportPasses is what an export's passes share: where their time went, the
// rows they copy in their copy whatever reads them, and their counts.
type exportPasses struct {
	phases    ExportPhases
	early     map[sharedResultID]bool
	chainTime time.Duration
	// copiedRows, unpredicted and lateMoved sum the passes' captures'
	// (HeldCapturedClosure), and the rows found changed by a late copy.
	copiedRows, unpredicted, lateMoved int
}

// count adds a capture's counts to the passes'.
func (passes *exportPasses) count(capture *HeldCapturedClosure) {
	passes.copiedRows += capture.copiedRows
	passes.unpredicted += capture.unpredicted
}

// exportPass is one pass of WithExportedValues: it captures, builds the
// bundle and its chains, and lends them to consume, unless a held row it
// copies late, once the chains are open, changed since its walk: it then
// consumes nothing and returns those rows, for the next pass to copy early.
//
//nolint:gocyclo // one step per export phase over the held closure; splitting hides the order of the steps
func (c *Cache) exportPass(ctx context.Context, selection ValueSelection, cfg config.RefConfig, passes *exportPasses, consume func(context.Context, *ExportedValues) error) (moved []sharedResultID, rerr error) {
	phases := &passes.phases
	var err error
	// A held row whose recipe a row the bundle carries in full shares is
	// carried in full too, in a capture once more: a merge refuses a held
	// row beside a record of its recipe. Only the kept roots' rows count,
	// so it's decided once the capture has left out the roots it can't
	// send. Each pass carries more rows in full, so this ends.
	var (
		capture   *HeldCapturedClosure
		carried   map[sharedResultID]bool
		leftOut   []LeftOutRoot
		bundleOp  *wcprof.Op
		bundleNS  time.Duration
		passStart time.Time
	)
	for {
		knownBefore := phases.BundleKnown
		if capture, err = c.captureCopied(ctx, selection, carried, passes.early, phases); err != nil {
			return nil, err
		}
		// Holding the known rows is the bundle phase's first step, though it
		// runs before the copy, which it tells what to read.
		bundleNS += phases.BundleKnown - knownBefore
		phases.BundlePasses++
		passStart = time.Now()
		_, bundleOp = wcprof.BeginOp(ctx, wcprof.OpKindRemoteCache, "remotecache.export.bundle", wcprof.OpOpts{})
		capture.checkRecords()
		checked := time.Now()
		phases.BundleCheck += checked.Sub(passStart)
		leftOut, err = capture.settle(selection)
		settled := time.Now()
		phases.BundleSettle += settled.Sub(checked)
		var shared []sharedResultID
		if err == nil {
			shared, err = capture.deriveHeldRecipes()
			phases.BundleRecipes += time.Since(settled)
		}
		if err == nil && len(shared) == 0 {
			break
		}
		bundleOp.EndErr(err)
		bundleNS += time.Since(passStart)
		passes.count(capture)
		if err != nil {
			return nil, errors.Join(err, capture.Release(ctx))
		}
		if carried == nil {
			carried = map[sharedResultID]bool{}
		}
		for _, id := range shared {
			carried[id] = true
		}
		if err := capture.Release(ctx); err != nil {
			return nil, err
		}
	}
	defer func() { rerr = errors.Join(rerr, capture.Release(ctx)) }()
	valuesStart := time.Now()
	if len(capture.roots) == 0 || (selection.EveryRoot && len(leftOut) > 0) {
		err = &RootsLeftOutError{Roots: leftOut}
		bundleOp.EndErr(err)
		return nil, err
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
	bundle := ValueBundle{Version: ValueBundleVersion, Roots: capture.roots}
	for _, row := range capture.order {
		if row.held {
			value := TransferredValue{Ordinal: row.ordinal, SenderNumber: uint64(row.shared.id), SenderReplacements: row.replacements, Record: PersistedRecord{ResultID: uint64(row.shared.id)}, ExpiresAtUnix: row.expiry, Held: row.recipe}
			// A row held at the walk has no dependencies here: it stands for
			// its closure. One held after its copy keeps its links to the
			// rows below it.
			for _, id := range row.deps {
				value.DependencyIDs = append(value.DependencyIDs, uint64(capture.rows[sharedResultID(id)].ordinal))
			}
			bundle.Values = append(bundle.Values, value)
			continue
		}
		// The record goes as the engine holds it, without its offers,
		// naming itself and its references by the engine's numbers, which
		// the receiver maps (transferSenderNumbers); its references were
		// checked against its dependencies when it was normalized
		// (checkRecords), and every dependency is a row of the bundle.
		value := TransferredValue{Ordinal: row.ordinal, SenderNumber: uint64(row.shared.id), SenderReplacements: row.replacements, Record: row.transfer, ExpiresAtUnix: row.expiry}
		for _, id := range row.deps {
			value.DependencyIDs = append(value.DependencyIDs, uint64(capture.rows[sharedResultID(id)].ordinal))
		}
		bundle.Values = append(bundle.Values, value)
	}
	bundleOp.End(wcprof.OutcomeOK)
	// An export that runs again keeps the time its earlier passes spent.
	phases.BundleValues += time.Since(valuesStart)
	phases.Bundle += bundleNS + time.Since(passStart)
	chainStart := time.Now()
	chainsCtx, chainsOp := wcprof.BeginOp(ctx, wcprof.OpKindRemoteCache, "remotecache.export.chains", wcprof.OpOpts{})
	chains, err := OpenSelectedChains(chainsCtx, capture, capture.outputs, cfg)
	chainsOp.EndErr(err)
	passes.chainTime += time.Since(chainStart)
	if err != nil {
		passes.count(capture)
		return nil, err
	}
	defer func() { rerr = errors.Join(rerr, chains.Release(ctx)) }()
	// sessionRows are the rows below each row, itself included, that carry
	// a session resource handle, sorted: each row's once, from its
	// dependencies'.
	sessionRows := map[sharedResultID][]uint64{}
	var rowsNeedingSessions func(*capturedTransferRow) []uint64
	rowsNeedingSessions = func(row *capturedTransferRow) []uint64 {
		if ids, ok := sessionRows[row.shared.id]; ok {
			return ids
		}
		if hook := capture.cache.testOwnerWalked; hook != nil {
			hook(uint64(row.shared.id))
		}
		var ids []uint64
		if row.shared.sessionResourceHandle != "" {
			ids = append(ids, uint64(row.shared.id))
		}
		for _, dep := range row.deps {
			ids = append(ids, rowsNeedingSessions(capture.rows[sharedResultID(dep)])...)
		}
		slices.Sort(ids)
		ids = slices.Compact(ids)
		sessionRows[row.shared.id] = ids
		return ids
	}
	// withOwner gives an output with a chain the owner of its offer: the
	// services it binds and the session resources its row's closure needs,
	// relocated to ordinals.
	withOwner := func(row *capturedTransferRow, output *TransferredOutput) error {
		owner := PersistedOfferOwner{}
		for _, service := range output.Value.Services {
			owner.DependencyIDs = append(owner.DependencyIDs, service.ServiceResultID)
		}
		owner.DependencyIDs = append(owner.DependencyIDs, rowsNeedingSessions(row)...)
		slices.Sort(owner.DependencyIDs)
		owner.DependencyIDs = slices.Compact(owner.DependencyIDs)
		offer := PersistedPartOffer{Address: output.Address, Value: *output.Value, Owner: owner}
		if err := visitPersistedPartOffer(&offer, nil, relocate); err != nil {
			return err
		}
		output.Value, output.Owner = &offer.Value, &offer.Owner
		return nil
	}
	selected := map[string]bool{}
	// carriedBy names, for each of the chains, the bundle's output that
	// carries it, if one does: a prefix of the chain names it. entryOf is
	// the first chain of each selected output, by ordinal and part, and
	// prefixesOf the chains that walked each snapshot, in their order.
	carriedBy := make([]*ChainPrefix, len(chains.Entries))
	entryOf := map[string]int{}
	prefixesOf := map[string][]int{}
	for i, entry := range chains.Entries {
		entryKey, _ := partAddressKey(entry.Address)
		if _, ok := entryOf[fmt.Sprintf("%d:%s", entry.Ordinal, entryKey)]; !ok {
			entryOf[fmt.Sprintf("%d:%s", entry.Ordinal, entryKey)] = i
		}
		for snapshotID, n := range chains.chains[i].Prefixes {
			if n > 0 {
				prefixesOf[snapshotID] = append(prefixesOf[snapshotID], i)
			}
		}
	}
	// A held row holding a snapshot a selected chain walked has parts the
	// bundle offers as the chain's prefixes: its record is read for them
	// now. One that changed since its walk ends this pass.
	lateStart := time.Now()
	for _, row := range capture.order {
		if !row.held || row.copied || !row.shared.ownedSnapshotLinks(func(link PersistedSnapshotRefLink) bool { return len(prefixesOf[link.RefKey]) > 0 }) {
			continue
		}
		if err := capture.copyLate(ctx, row); err != nil {
			if cause := context.Cause(ctx); cause != nil {
				passes.count(capture)
				return nil, cause
			}
			moved = append(moved, row.shared.id)
		}
	}
	phases.Copy += time.Since(lateStart)
	passes.count(capture)
	if len(moved) > 0 {
		return moved, nil
	}
	for _, sel := range capture.outputs {
		row, out, err := selectedCapturedOutput(capture, sel)
		if err != nil {
			return nil, err
		}
		key, _ := partAddressKey(out.Address)
		selected[fmt.Sprintf("%d:%s", row.ordinal, key)] = true
		output := TransferredOutput{Ordinal: row.ordinal, Address: out.Address, State: out.State, Value: out.Value}
		if i, ok := entryOf[fmt.Sprintf("%d:%s", row.ordinal, key)]; ok {
			output.Chain = &OfferedChain{Layers: chains.Entries[i].Layers}
			carriedBy[i] = &ChainPrefix{Ordinal: row.ordinal, Address: out.Address}
		}
		if output.Chain != nil {
			if err := withOwner(row, &output); err != nil {
				return nil, err
			}
		} else if output.Value != nil {
			value := *output.Value
			value.Services = slices.Clone(value.Services)
			for i := range value.Services {
				id := value.Services[i].ServiceResultID
				ref := PersistedRef{ResultID: id}
				if err := relocate(&ref); err != nil {
					return nil, err
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
			// Only a snapshot a selected chain walked is a prefix: most of
			// the closure's parts are no chain's.
			walked := prefixesOf[out.SnapshotID]
			if out.State != "completed" || out.SnapshotID == "" || out.Value == nil || len(walked) == 0 {
				continue
			}
			key, _ := partAddressKey(out.Address)
			if selected[fmt.Sprintf("%d:%s", row.ordinal, key)] {
				continue
			}
			if hook := capture.cache.testPartRouted; hook != nil {
				hook(uint64(row.shared.id))
			}
			if route, err := routePartRecord(row.record, out.Address); err == nil && prefixServedByDelegation(route, capture.rows, offered) {
				continue
			}
			var layers []snapshots.ExportLayer
			var prefix *ChainPrefix
			for _, i := range walked {
				n := chains.chains[i].Prefixes[out.SnapshotID]
				if carriedBy[i] != nil {
					prefix = &ChainPrefix{Ordinal: carriedBy[i].Ordinal, Address: carriedBy[i].Address, Layers: n}
					break
				}
				if layers == nil {
					layers = chains.Entries[i].Layers[:n]
				}
			}
			output := TransferredOutput{Ordinal: row.ordinal, Address: out.Address, State: out.State, Value: out.Value, Prefix: prefix}
			if prefix == nil {
				output.Chain = &OfferedChain{Layers: layers}
			}
			if err := withOwner(row, &output); err != nil {
				return nil, err
			}
			bundle.Outputs = append(bundle.Outputs, output)
			offered[fmt.Sprintf("%d:%s", row.ordinal, key)] = true
		}
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	// Of the known rows, the bundle carries only those something in it
	// stands on, and the held rows on the way down to them.
	keepStart := time.Now()
	order, leftOutHeld, keptAll := capture.order, 0, ""
	if !c.testKeepEveryKnownRow {
		order, leftOutHeld, keptAll = keepNeededRows(&bundle, chains, capture.order)
	}
	phases.BundleKeep = time.Since(keepStart)
	sources := make([]ImportedValue, len(order))
	rows := ExportedRows{Full: make(map[uint64]RowRevision, len(order))}
	for i, row := range order {
		sources[i] = ImportedValue{Ordinal: row.ordinal, ResultID: uint64(row.shared.id)}
		if row.held {
			rows.Held = append(rows.Held, uint64(row.shared.id))
			continue
		}
		rows.Full[uint64(row.shared.id)] = RowRevision{Replacements: row.replacements, Transfer: row.transferRev, Dependencies: row.dependencyRev}
	}
	profExportedParts(ctx, bundle, sources)
	return nil, consume(ctx, &ExportedValues{Bundle: bundle, Chains: chains, ChainTime: passes.chainTime, Phases: *phases, CopiedRows: passes.copiedRows, UnpredictedLinks: passes.unpredicted, LateMoved: passes.lateMoved, Sources: sources, LeftOut: leftOut, Rows: rows, LeftOutHeld: leftOutHeld, KeptEveryRow: keptAll})
}

// keepNeededRows leaves out of bundle the known rows nothing in it stands
// on, and returns the captured rows it keeps, in order, renumbered to their
// new ordinals, how many held rows it left out, and, when it sends the
// bundle as it is for a reason, the reason.
//
// A row is needed when it is a root, a row the bundle carries in full, a
// row an output sits on, or one named by an output's owner or services, or
// by a full row's dependencies or its offers' owners and services. A held
// row keeps its links only to the children something is needed at or
// below: those on the way down to a part or a new row. The bundle then
// carries what its roots reach through what it keeps, as a closure of its
// roots, and a held row with nothing needed below it goes with no links,
// standing for its closure, as a held row does in an export that selects no
// parts. A held row's links name no dependencies of the recipient's entry:
// the recipient keeps its own, and checks each held row the bundle carries
// against its own copy. The rows a held row stands for this way aren't
// checked there, so a copy of one that has expired on the recipient stays
// so until a bundle carries the row as needed or in full.
//
// Ordinals are dense, so the rows kept are renumbered, with every ordinal
// the bundle and chains name: dependencies, roots, outputs, their owners,
// services and prefixes, and offers. A row left out carries no output, so
// no chain names one.
//
// It sends the bundle as it is, with a reason, when the bundle's values
// aren't its captured rows, one each at its ordinal (keptAllRows,
// keptAllOrdinals), or when a needed row isn't reached through the kept
// links (keptAllUnreached). None happens in a capture: settle renumbers the
// kept roots' rows densely in its order, the bundle's values are built from
// it, one each, and a bundle the merge would accept reaches every row it
// carries from its roots, every needed row through links that keep it. A
// reason on an export is a bug.
func keepNeededRows(bundle *ValueBundle, chains *SelectedChains, order []*capturedTransferRow) ([]*capturedTransferRow, int, string) {
	n := len(bundle.Values)
	if n == 0 {
		return order, 0, ""
	}
	if len(order) != n {
		return order, 0, keptAllRows
	}
	for i, v := range bundle.Values {
		if uint64(v.Ordinal) != uint64(i+1) || order[i].ordinal != v.Ordinal {
			return order, 0, keptAllOrdinals
		}
	}
	needed, outputDeps := neededBundleRows(*bundle)
	links := heldRowLinks(*bundle, needed)
	kept := reachedBundleRows(*bundle, links, outputDeps)
	for i := range needed {
		if needed[i] && !kept[i] {
			return order, 0, keptAllUnreached
		}
	}
	rows, leftOut := renumberKeptRows(bundle, chains, order, kept, links)
	return rows, leftOut, ""
}

// Why keepNeededRows sent a bundle as it was (ExportedValues.KeptEveryRow).
const (
	// keptAllRows: the bundle's values and its captured rows differ in
	// number.
	keptAllRows = "rows"
	// keptAllOrdinals: a value isn't at its ordinal, or isn't its captured
	// row's.
	keptAllOrdinals = "ordinals"
	// keptAllUnreached: a needed row isn't reached through the kept links.
	keptAllUnreached = "unreached"
)

// bundleRowIndex is the index in a bundle of n values of the row of
// ordinal o, or -1 for none.
func bundleRowIndex(n int, o uint64) int {
	if o == 0 || o > uint64(n) {
		return -1
	}
	return int(o - 1)
}

// neededBundleRows marks, by index, the rows of bundle something in it
// stands on (keepNeededRows), and returns, by index, the rows each row's
// outputs' owners name, which the bundle's check walks with its
// dependencies.
func neededBundleRows(bundle ValueBundle) ([]bool, map[int][]uint64) {
	n := len(bundle.Values)
	needed := make([]bool, n)
	need := func(ordinals ...uint64) {
		for _, o := range ordinals {
			if i := bundleRowIndex(n, o); i >= 0 {
				needed[i] = true
			}
		}
	}
	for _, root := range bundle.Roots {
		need(uint64(root.Ordinal))
	}
	for _, v := range bundle.Values {
		if v.Held != "" {
			continue
		}
		need(uint64(v.Ordinal))
		need(v.DependencyIDs...)
		for _, offer := range v.Record.Envelope.PendingOffers {
			need(offer.Owner.DependencyIDs...)
			need(serviceOrdinals(offer.Value.Services)...)
		}
	}
	outputDeps := map[int][]uint64{}
	for _, out := range bundle.Outputs {
		need(uint64(out.Ordinal))
		if out.Prefix != nil {
			need(uint64(out.Prefix.Ordinal))
		}
		if out.Owner != nil {
			need(out.Owner.DependencyIDs...)
			if i := bundleRowIndex(n, uint64(out.Ordinal)); i >= 0 {
				outputDeps[i] = append(outputDeps[i], out.Owner.DependencyIDs...)
			}
		}
		if out.Value != nil {
			need(serviceOrdinals(out.Value.Services)...)
		}
	}
	return needed, outputDeps
}

// serviceOrdinals are the rows service bindings name.
func serviceOrdinals(services []TransferredServiceBinding) []uint64 {
	out := make([]uint64, 0, len(services))
	for _, service := range services {
		out = append(out, service.ServiceResultID)
	}
	return out
}

// heldRowLinks returns, by index, the links each row of bundle keeps: a row
// in full all of its dependencies, a held row those of its children that
// are needed or have something needed below them.
func heldRowLinks(bundle ValueBundle, needed []bool) [][]uint64 {
	n := len(bundle.Values)
	// below is 1 while a row's walk is under way, 2 when nothing is needed
	// below it, and 3 when something is.
	below := make([]uint8, n)
	var neededBelow func(int) bool
	neededBelow = func(i int) bool {
		if below[i] != 0 {
			return below[i] == 3
		}
		below[i] = 1
		found := false
		for _, dep := range bundle.Values[i].DependencyIDs {
			if j := bundleRowIndex(n, dep); j >= 0 && (needed[j] || neededBelow(j)) {
				found = true
			}
		}
		below[i] = 2
		if found {
			below[i] = 3
		}
		return found
	}
	links := make([][]uint64, n)
	for i, v := range bundle.Values {
		if v.Held == "" {
			links[i] = v.DependencyIDs
			continue
		}
		for _, dep := range v.DependencyIDs {
			if j := bundleRowIndex(n, dep); j >= 0 && (needed[j] || neededBelow(j)) {
				links[i] = append(links[i], dep)
			}
		}
	}
	return links
}

// reachedBundleRows marks, by index, the rows of bundle its roots reach
// through links, their offers' owners and their outputs' owners, as the
// bundle's check walks it (checkValueBundle).
func reachedBundleRows(bundle ValueBundle, links [][]uint64, outputDeps map[int][]uint64) []bool {
	n := len(bundle.Values)
	kept := make([]bool, n)
	var next []int
	for _, root := range bundle.Roots {
		if i := bundleRowIndex(n, uint64(root.Ordinal)); i >= 0 {
			next = append(next, i)
		}
	}
	for len(next) > 0 {
		i := next[len(next)-1]
		next = next[:len(next)-1]
		if kept[i] {
			continue
		}
		kept[i] = true
		deps := slices.Clone(links[i])
		for _, offer := range bundle.Values[i].Record.Envelope.PendingOffers {
			deps = append(deps, offer.Owner.DependencyIDs...)
		}
		deps = append(deps, outputDeps[i]...)
		for _, dep := range deps {
			if j := bundleRowIndex(n, dep); j >= 0 && !kept[j] {
				next = append(next, j)
			}
		}
	}
	return kept
}

// renumberKeptRows leaves out of bundle the rows not kept, gives each kept
// row its links, and renumbers the kept rows, and every ordinal the bundle
// and chains name, densely in their order. It returns the captured rows
// kept, renumbered, and how many held rows it left out; with none left
// out, it changes nothing.
func renumberKeptRows(bundle *ValueBundle, chains *SelectedChains, order []*capturedTransferRow, kept []bool, links [][]uint64) ([]*capturedTransferRow, int) {
	n := len(bundle.Values)
	renumber := make([]uint64, n)
	keptRows := make([]*capturedTransferRow, 0, n)
	values := make([]TransferredValue, 0, n)
	leftOut := 0
	for i, v := range bundle.Values {
		if !kept[i] {
			if v.Held != "" {
				leftOut++
			}
			continue
		}
		renumber[i] = uint64(len(values) + 1)
		v.DependencyIDs = links[i]
		values = append(values, v)
		keptRows = append(keptRows, order[i])
	}
	if leftOut == 0 {
		return order, 0
	}
	to := func(o uint64) uint64 {
		if i := bundleRowIndex(n, o); i >= 0 {
			return renumber[i]
		}
		return o
	}
	for i := range values {
		renumberValue(&values[i], to)
	}
	for _, row := range keptRows {
		row.ordinal = TransferOrdinal(to(uint64(row.ordinal)))
	}
	bundle.Values = values
	for i := range bundle.Roots {
		bundle.Roots[i].Ordinal = TransferOrdinal(to(uint64(bundle.Roots[i].Ordinal)))
	}
	for i := range bundle.Outputs {
		renumberOutput(&bundle.Outputs[i], to)
	}
	for i := range bundle.Calls {
		bundle.Calls[i].Value = TransferOrdinal(to(uint64(bundle.Calls[i].Value)))
	}
	if chains != nil {
		for i := range chains.Entries {
			chains.Entries[i].Ordinal = TransferOrdinal(to(uint64(chains.Entries[i].Ordinal)))
		}
	}
	return keptRows, leftOut
}

// renumberValue renumbers a value's ordinal, dependencies, and its offers'
// owners and services by to, copying what it changes.
func renumberValue(v *TransferredValue, to func(uint64) uint64) {
	v.Ordinal = TransferOrdinal(to(uint64(v.Ordinal)))
	v.DependencyIDs = mapOrdinals(v.DependencyIDs, to)
	if len(v.Record.Envelope.PendingOffers) == 0 {
		return
	}
	offers := slices.Clone(v.Record.Envelope.PendingOffers)
	for j := range offers {
		offers[j].Owner.DependencyIDs = mapOrdinals(offers[j].Owner.DependencyIDs, to)
		offers[j].Value.Services = mapServices(offers[j].Value.Services, to)
	}
	v.Record.Envelope.PendingOffers = offers
}

// renumberOutput renumbers an output's row, its prefix's, its owner's
// dependencies and its services by to, copying what it changes.
func renumberOutput(out *TransferredOutput, to func(uint64) uint64) {
	out.Ordinal = TransferOrdinal(to(uint64(out.Ordinal)))
	if out.Prefix != nil {
		prefix := *out.Prefix
		prefix.Ordinal = TransferOrdinal(to(uint64(prefix.Ordinal)))
		out.Prefix = &prefix
	}
	if out.Owner != nil {
		owner := *out.Owner
		owner.DependencyIDs = mapOrdinals(owner.DependencyIDs, to)
		out.Owner = &owner
	}
	if out.Value != nil {
		value := *out.Value
		value.Services = mapServices(value.Services, to)
		out.Value = &value
	}
}

// mapOrdinals returns ordinals renumbered by to, in a new slice.
func mapOrdinals(ordinals []uint64, to func(uint64) uint64) []uint64 {
	if ordinals == nil {
		return nil
	}
	out := make([]uint64, len(ordinals))
	for i, o := range ordinals {
		out[i] = to(o)
	}
	return out
}

// mapServices returns service bindings whose rows are renumbered by to, in a
// new slice.
func mapServices(services []TransferredServiceBinding, to func(uint64) uint64) []TransferredServiceBinding {
	if services == nil {
		return nil
	}
	out := slices.Clone(services)
	for i := range out {
		out[i].ServiceResultID = to(out[i].ServiceResultID)
	}
	return out
}
