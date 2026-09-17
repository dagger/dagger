package dagql

import (
	"context"
	"errors"
	"reflect"
	"slices"
)

type OfferOutcome uint8

const (
	OfferUnavailable OfferOutcome = iota
	OfferAccepted
	OfferAlreadyComplete
	OfferExecutionStarted
	OfferInvalid
)

type OfferDisposition struct {
	Address  PersistedPartAddress
	Outcome  OfferOutcome
	Replaced bool
	OfferRev uint64
	Err      error
}

func unavailableOffers(offers []PersistedPartOffer, err error) []OfferDisposition {
	out := make([]OfferDisposition, len(offers))
	for i, offer := range offers {
		out[i] = OfferDisposition{Address: clonePartAddress(offer.Address), Outcome: OfferUnavailable, Err: err}
	}
	return out
}

// OfferParts is an engine control operation. It publishes descriptions without
// evaluating outputs or checking the control caller's session resources.
func (c *Cache) OfferParts(ctx context.Context, receiver AnyResult, offers []PersistedPartOffer) (out []OfferDisposition, rerr error) {
	out = unavailableOffers(offers, nil)
	op, err := c.beginCacheOperation()
	if err != nil {
		return unavailableOffers(offers, err), err
	}
	defer op.finish(false)
	if receiver == nil || receiver.cacheSharedResult() == nil {
		err := errors.New("offer: detached receiver")
		return unavailableOffers(offers, err), err
	}
	root := receiver.cacheSharedResult()
	c.egraphMu.Lock()
	if c.resultsByID[root.id] != root || root.attachmentState() != resultAttachmentClean {
		c.egraphMu.Unlock()
		err := errors.New("offer: receiver is unavailable")
		return unavailableOffers(offers, err), err
	}
	c.incrementIncomingOwnershipLocked(ctx, root)
	c.egraphMu.Unlock()
	defer func() { rerr = errors.Join(rerr, c.releasePartRow(context.WithoutCancel(ctx), root)) }()
	for i, offer := range offers {
		if err := context.Cause(ctx); err != nil {
			for j := i; j < len(out); j++ {
				out[j].Err = err
			}
			return out, errors.Join(rerr, err)
		}
		out[i] = c.offerPart(ctx, root, offer)
		rerr = errors.Join(rerr, out[i].Err)
	}
	return out, rerr
}

// E and G are held. Whole-result groups conservatively cover the entire inline
// scope, including outputs whose topology is not known until metadata exists.
func offerGateOutcomeLocked(gate *PartWriterGate, address PersistedPartAddress) OfferOutcome {
	key, _ := partAddressKey(address)
	if gate.outputs[key].phase != PartPending {
		return OfferAlreadyComplete
	}
	for key, group := range gate.groups {
		overlaps := containsPart(group.writeSet, address)
		if !overlaps {
			// LazyGroupAddress keys are canonical; this also covers empty write sets.
			overlaps = key == lazyGroupAddressKey(LazyGroupAddress{OutputPath: address.OutputPath, Group: LazyGroupWhole})
		}
		if overlaps && (group.phase == LazyEvaluationRunning || group.phase == LazyEvaluationEvaluated) {
			return OfferExecutionStarted
		}
	}
	return OfferAccepted
}

type offerRowCapture struct {
	row          *sharedResult
	record       PersistedRecord
	version      capturedRowRevision
	gateRevision uint64
}

func (c *Cache) offerPart(ctx context.Context, root *sharedResult, input PersistedPartOffer) (out OfferDisposition) {
	out.Address = clonePartAddress(input.Address)
	if _, err := partAddressKey(input.Address); err != nil {
		out.Outcome, out.Err = OfferInvalid, err
		return
	}
	copied, err := clonePartOffers([]PersistedPartOffer{input})
	if err != nil {
		out.Outcome, out.Err = OfferInvalid, err
		return
	}
	offer := copied[0]
	slices.Sort(offer.Owner.DependencyIDs)
	offer.Owner.DependencyIDs = slices.Compact(offer.Owner.DependencyIDs)
	captures := map[uint64]*offerRowCapture{}
	defer func() {
		for _, capture := range captures {
			out.Err = errors.Join(out.Err, c.releasePartRow(context.WithoutCancel(ctx), capture.row))
		}
	}()
	var captureErr error
	id, address, err := resolveSelectedRecord(uint64(root.id), offer.Address, func(id uint64) (PersistedRecord, bool) {
		if capture := captures[id]; capture != nil {
			return capture.record, true
		}
		c.egraphMu.Lock()
		row := c.resultsByID[sharedResultID(id)]
		if row == nil || row.attachmentState() != resultAttachmentClean {
			c.egraphMu.Unlock()
			captureErr = errors.New("offer: selected row is unavailable")
			return PersistedRecord{}, false
		}
		c.incrementIncomingOwnershipLocked(ctx, row)
		capture := &offerRowCapture{row: row}
		captures[id] = capture
		gate := row.partGate.loadOrCreate()
		gate.mu.Lock()
		capture.gateRevision = gate.revision
		if row == root {
			out.Outcome = offerGateOutcomeLocked(gate, offer.Address)
		}
		gate.mu.Unlock()
		c.egraphMu.Unlock()
		if out.Outcome == OfferAlreadyComplete || out.Outcome == OfferExecutionStarted {
			return PersistedRecord{}, false
		}
		capture.record, captureErr = c.capturePartRecord(ctx, row, row.imported, nil, &capture.version)
		return capture.record, captureErr == nil
	})
	if out.Outcome == OfferAlreadyComplete || out.Outcome == OfferExecutionStarted {
		return
	}
	if captureErr != nil {
		out.Outcome, out.Err = OfferUnavailable, captureErr
		return
	}
	if err != nil {
		out.Outcome, out.Err = OfferInvalid, err
		return
	}
	offer.Address = address
	capture := captures[id]
	row := capture.row
	probes, err := describePartRecord(capture.record)
	if err != nil {
		out.Outcome, out.Err = OfferUnavailable, err
		return
	}
	var probe *PartProbe
	for i := range probes {
		if containsPart([]PersistedPartAddress{probes[i].Descriptor.Address}, address) {
			probe = &probes[i]
			break
		}
	}
	if probe == nil {
		out.Outcome, out.Err = OfferInvalid, errors.New("offer: undeclared output")
		return
	}
	outputs, err := mapTransferredOutputs(capture.record)
	if err != nil {
		out.Outcome, out.Err = OfferUnavailable, err
		return
	}
	var described *CapturedCodecOutput
	for i := range outputs {
		if containsPart([]PersistedPartAddress{outputs[i].Address}, address) {
			described = &outputs[i]
			break
		}
	}
	if described == nil {
		out.Outcome, out.Err = OfferInvalid, errors.New("offer: output has no descriptor")
		return
	}
	if err := validateTransferOffer(offer, *described); err != nil {
		out.Outcome, out.Err = OfferInvalid, err
		return
	}
	key, _ := partAddressKey(address)
	// A preparation hold protects references across the two E sections.
	c.egraphMu.Lock()
	var owner *offerOwner
	old := row.partOffers[key]
	if old != nil && sameOfferMeaning(old.record, offer) {
		owner = old.owner
		c.retainOfferOwnerLocked(owner)
	} else {
		owner, err = c.newOfferOwnerLocked(ctx, offer.Owner)
	}
	c.egraphMu.Unlock()
	if err != nil {
		out.Outcome, out.Err = OfferInvalid, err
		return
	}
	offer.Owner.DependencyIDs = slices.Clone(owner.record.DependencyIDs)
	transferred := false
	defer func() {
		if transferred {
			return
		}
		cleanupCtx := context.WithoutCancel(ctx)
		c.egraphMu.Lock()
		queue, err := c.releaseOfferOwnerLocked(cleanupCtx, owner)
		callbacks, collectErr := c.collectUnownedResultsLocked(cleanupCtx, queue)
		c.egraphMu.Unlock()
		out.Err = errors.Join(out.Err, err, collectErr, runOnReleaseFuncs(cleanupCtx, callbacks))
	}()
	for _, captured := range captures {
		if err := captured.version.check(captured.row); err != nil {
			out.Outcome, out.Err = OfferUnavailable, err
			return
		}
	}
	if err := context.Cause(ctx); err != nil {
		out.Outcome, out.Err = OfferUnavailable, err
		return
	}
	c.egraphMu.Lock()
	gate := row.partGate.loadOrCreate()
	gate.mu.Lock()
	out.Outcome = offerGateOutcomeLocked(gate, address)
	if out.Outcome == OfferAccepted {
		if c.resultsByID[row.id] != row || row.attachmentState() != resultAttachmentClean || gate.revision != capture.gateRevision {
			out.Outcome, out.Err = OfferUnavailable, ErrPersistStateNotReady
		} else {
			row.payloadMu.RLock()
			current := row.payloadRevision == capture.version.payload.payloadRevision && row.hasValue == capture.version.payload.hasValue && row.persistedEnvelope == capture.version.payload.persistedEnvelope
			row.payloadMu.RUnlock()
			if !current {
				out.Outcome, out.Err = OfferUnavailable, ErrPersistStateNotReady
			} else if probe.LocalComplete {
				out.Outcome = OfferAlreadyComplete
			}
		}
	}
	var queue collectionQueue
	if out.Outcome == OfferAccepted {
		current := row.partOffers[key]
		if current != old {
			out.Outcome, out.Err = OfferUnavailable, ErrPersistStateNotReady
		} else if current == nil || !reflect.DeepEqual(current.record, offer) {
			if _, err := c.validateOfferAttachmentLocked(row, address, &partOffer{record: offer, owner: owner}); err != nil {
				out.Outcome, out.Err = OfferInvalid, err
			} else {
				next := &partOffer{record: offer, owner: owner}
				if current == nil {
					out.Err = c.attachPartOfferLocked(row, address, next)
				} else {
					queue, out.Err = c.replacePartOfferLocked(ctx, row, address, next)
				}
				transferred = row.partOffers[key] == next
				out.Replaced = transferred && current != nil
				if !transferred {
					out.Outcome = OfferInvalid
				}
			}
		}
	}
	out.OfferRev = row.transferRevision
	if out.Outcome == OfferAccepted {
		// A native caller that selected its callback before this publication must
		// return to the acquisition dispatcher before entering the body.
		gate.managed = true
		row.partGate.active.Store(true)
	}
	gate.mu.Unlock()
	callbacks, collectErr := c.collectUnownedResultsLocked(context.WithoutCancel(ctx), queue)
	c.egraphMu.Unlock()
	out.Err = errors.Join(out.Err, collectErr, runOnReleaseFuncs(context.WithoutCancel(ctx), callbacks))
	return
}

func sameOfferMeaning(a, b PersistedPartOffer) bool {
	a.Chain.Addresses, b.Chain.Addresses = nil, nil
	return reflect.DeepEqual(a, b)
}
