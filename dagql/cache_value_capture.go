package dagql

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
)

type capturedTransferRow struct {
	shared                                  *sharedResult
	version                                 capturedRowRevision
	imported                                bool
	frame                                   *ResultCall
	deps                                    []uint64
	offers                                  []PersistedPartOffer
	transferRev, dependencyRev, requiredRev uint64
	expiry                                  int64
	record                                  PersistedRecord
	outputs                                 []CapturedCodecOutput
	ordinal                                 TransferOrdinal
}

// HeldCapturedClosure protects every exact source row and offer while storage
// opens and provider consumption run outside graph and value locks.
type HeldCapturedClosure struct {
	cache       *Cache
	rows        map[sharedResultID]*capturedTransferRow
	order       []*capturedTransferRow
	owners      map[offerOwnerID]*offerOwner
	roots       []TransferredRoot
	releaseOnce sync.Once
	releaseErr  error
}

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
		releases, err := c.collectUnownedResultsLocked(ctx, queue)
		c.egraphMu.Unlock()
		capture.releaseErr = errors.Join(capture.releaseErr, err, runOnReleaseFuncs(ctx, releases))
	})
	return capture.releaseErr
}
func (c *Cache) holdTransferClosure(ctx context.Context, selection ValueSelection) (*HeldCapturedClosure, error) {
	capture := &HeldCapturedClosure{cache: c, rows: map[sharedResultID]*capturedTransferRow{}, owners: map[offerOwnerID]*offerOwner{}}
	c.egraphMu.Lock()
	seen := map[sharedResultID]uint8{}
	var walk func(*sharedResult) error
	walk = func(res *sharedResult) error {
		if res == nil || res.id == 0 || c.resultsByID[res.id] != res {
			return fmt.Errorf("transfer capture: unregistered row")
		}
		if seen[res.id] == 1 {
			return fmt.Errorf("transfer capture: ownership cycle at %d", res.id)
		}
		if seen[res.id] == 2 {
			return nil
		}
		if res.attachmentState() != resultAttachmentClean {
			return fmt.Errorf("%w: result %d attachment", ErrPersistStateNotReady, res.id)
		}
		frame := res.loadResultCall()
		if frame == nil || frame.Type == nil || frame.Type.NamedType == "Query" {
			return fmt.Errorf("transfer capture: row %d has no transferable frame", res.id)
		}
		seen[res.id] = 1
		c.incrementIncomingOwnershipLocked(ctx, res)
		row := &capturedTransferRow{shared: res, imported: res.imported, frame: frame, offers: res.pendingOffersLocked(), expiry: res.expiresAtUnix, transferRev: res.transferRevision, dependencyRev: res.dependencyOwnershipRevision, requiredRev: res.requiredSessionResourcesGen.Load()}
		capture.rows[res.id] = row
		for id := range res.deps {
			row.deps = append(row.deps, uint64(id))
		}
		slices.Sort(row.deps)
		for child := range c.resultOwnershipChildrenLocked(res) {
			if child.owner == nil {
				if err := walk(child.result); err != nil {
					return err
				}
				continue
			}
			if _, held := capture.owners[child.owner.id]; !held {
				c.retainOfferOwnerLocked(child.owner)
				capture.owners[child.owner.id] = child.owner
			}
			for dep := range offerOwnershipChildrenLocked(child.owner) {
				if err := walk(dep); err != nil {
					return err
				}
			}
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
		if res.expiresAtUnix != 0 && res.expiresAtUnix <= now {
			err = fmt.Errorf("transfer capture: expired root %d", res.id)
			break
		}
		if err = walk(res); err != nil {
			break
		}
		expiry := res.expiresAtUnix
		if edge, ok := c.persistedEdgesByResult[res.id]; ok {
			expiry = edge.expiresAtUnix
		}
		if expiry != 0 && expiry <= now {
			err = fmt.Errorf("expired retention edge for root %d", res.id)
			break
		}
		capture.roots = append(capture.roots, TransferredRoot{Ordinal: capture.rows[res.id].ordinal, ExpiresAtUnix: expiry})
	}
	for _, output := range selection.Outputs {
		if err == nil && (output.Result == nil || output.Result.cacheSharedResult() == nil || capture.rows[output.Result.cacheSharedResult().id] == nil || capture.rows[output.Result.cacheSharedResult().id].shared != output.Result.cacheSharedResult()) {
			err = fmt.Errorf("selected output lies outside captured closure")
		}
	}
	c.egraphMu.Unlock()
	if err != nil {
		return nil, errors.Join(err, capture.Release(ctx))
	}
	return capture, nil
}
func (capture *HeldCapturedClosure) copy(ctx context.Context) error {
	for _, row := range capture.order {
		rec, err := capture.cache.captureHeldPersistedRecord(ctx, row.shared, row.imported, row.offers, &row.version)
		if err != nil {
			return err
		}
		row.record = rec
		if hook := capture.cache.testTransferCopied; hook != nil {
			hook(uint64(row.shared.id))
		}
		row.outputs, err = mapTransferredOutputs(rec)
		if err != nil {
			return err
		}
		for _, offer := range row.offers {
			key, err := partAddressKey(offer.Address)
			if err != nil {
				return err
			}
			for _, out := range row.outputs {
				outKey, _ := partAddressKey(out.Address)
				if key == outKey && out.State != "pending" {
					return fmt.Errorf("%w: unretired offer on completed output %d:%s", ErrPersistStateNotReady, row.shared.id, key)
				}
			}
		}
	}
	// Release each row's copy guards before comparing the next row.
	for _, row := range capture.order {
		if err := row.version.check(row.shared); err != nil {
			return err
		}
	}

	c := capture.cache
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	for _, row := range capture.order {
		res := row.shared
		if c.resultsByID[res.id] != res || res.attachmentState() != resultAttachmentClean || res.loadResultCall() != row.frame || res.expiresAtUnix != row.expiry || res.transferRevision != row.transferRev || res.dependencyOwnershipRevision != row.dependencyRev || res.requiredSessionResourcesGen.Load() != row.requiredRev {
			return fmt.Errorf("%w: row %d ownership changed", ErrPersistStateNotReady, res.id)
		}
	}
	now := time.Now().Unix()
	for _, root := range capture.roots {
		row := capture.order[int(root.Ordinal)-1]
		expiry := row.shared.expiresAtUnix
		if edge, ok := c.persistedEdgesByResult[row.shared.id]; ok {
			expiry = edge.expiresAtUnix
		}
		if expiry != root.ExpiresAtUnix || expiry != 0 && expiry <= now || row.expiry != 0 && row.expiry <= now {
			return fmt.Errorf("%w: root %d expiry changed", ErrPersistStateNotReady, row.shared.id)
		}
	}
	return context.Cause(ctx)
}

type SelectedChain struct {
	Ordinal  TransferOrdinal
	Address  PersistedPartAddress
	Layers   []snapshots.ExportLayer
	Provider content.InfoReaderProvider
}
type SelectedChains struct {
	Entries []SelectedChain
	refs    []snapshots.ImmutableRef
	chains  []*snapshots.ExportChain
	once    sync.Once
	err     error
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
		chain, err := ref.ExportChain(ctx, cfg)
		if err != nil {
			return nil, err
		}
		chains.chains = append(chains.chains, chain)
		chains.Entries = append(chains.Entries, SelectedChain{Ordinal: row.ordinal, Address: out.Address, Layers: chain.Layers, Provider: chain.Provider})
	}
	return chains, nil
}

type ExportedValues struct {
	Bundle ValueBundle
	Chains *SelectedChains
	// Sources maps bundle ordinals to held source rows; it is not transported.
	Sources []ImportedValue
}

func (c *Cache) WithExportedValues(ctx context.Context, selection ValueSelection, cfg config.RefConfig, consume func(context.Context, *ExportedValues) error) (rerr error) {
	op, err := c.beginCacheOperation()
	if err != nil {
		return err
	}
	defer op.finish(false)
	if consume == nil {
		return fmt.Errorf("nil transfer consumer")
	}
	capture, err := c.holdTransferClosure(ctx, selection)
	if err != nil {
		return err
	}
	defer func() { rerr = errors.Join(rerr, capture.Release(ctx)) }()
	if err := capture.copy(ctx); err != nil {
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
		local := row.record
		local.Envelope.PendingOffers = nil // Owner references have their own scope.
		if _, err := VisitEncodedReferences(local, func(ref *PersistedRef) error {
			if ref.RecipeID == nil && (ref.Kind == PersistedRefChild || ref.Kind == PersistedRefCall) && !slices.Contains(row.deps, ref.ResultID) {
				return fmt.Errorf("reference %s to %d is not a direct dependency", ref.Path, ref.ResultID)
			}
			return nil
		}); err != nil {
			return err
		}
		normalized, err := normalizeTransferRecord(row.record)
		if err != nil {
			return err
		}
		if err := validateTransferRecord(normalized, row.deps); err != nil {
			return fmt.Errorf("row %d: %w", row.shared.id, err)
		}
		rec, err := VisitEncodedReferences(normalized, relocate)
		if err != nil {
			return err
		}
		value := TransferredValue{Ordinal: row.ordinal, Record: rec, ExpiresAtUnix: row.expiry}
		for _, id := range row.deps {
			value.DependencyIDs = append(value.DependencyIDs, uint64(capture.rows[sharedResultID(id)].ordinal))
		}
		bundle.Values = append(bundle.Values, value)
	}
	if _, err := validateValueBundle(bundle); err != nil {
		return err
	}
	chains, err := OpenSelectedChains(ctx, capture, selection.Outputs, cfg)
	if err != nil {
		return err
	}
	defer func() { rerr = errors.Join(rerr, chains.Release(ctx)) }()
	for _, selected := range selection.Outputs {
		row, out, err := selectedCapturedOutput(capture, selected)
		if err != nil {
			return err
		}
		key, _ := partAddressKey(out.Address)
		hasOffer := false
		for _, offer := range row.offers {
			offerKey, _ := partAddressKey(offer.Address)
			hasOffer = hasOffer || key == offerKey
		}
		if hasOffer {
			continue
		}
		output := TransferredOutput{Ordinal: row.ordinal, Address: out.Address, State: out.State, Value: out.Value}
		for _, entry := range chains.Entries {
			entryKey, _ := partAddressKey(entry.Address)
			if entry.Ordinal == row.ordinal && entryKey == key {
				output.Chain = &OfferedChain{Layers: entry.Layers}
				break
			}
		}
		if output.Chain != nil {
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
			offer := PersistedPartOffer{Address: out.Address, Value: *output.Value, Owner: owner}
			if err := visitPersistedPartOffer(&offer, nil, relocate); err != nil {
				return err
			}
			output.Value, output.Owner = &offer.Value, &offer.Owner
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
	if _, err := validateValueBundle(bundle); err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	sources := make([]ImportedValue, len(capture.order))
	for i, row := range capture.order {
		sources[i] = ImportedValue{Ordinal: row.ordinal, ResultID: uint64(row.shared.id)}
	}
	return consume(ctx, &ExportedValues{Bundle: bundle, Chains: chains, Sources: sources})
}
