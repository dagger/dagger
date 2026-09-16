package dagql

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// The active path is per demand, independent of exhaustion and task lifetime.
// Copying it before append prevents concurrent sibling demands sharing storage.
type partDelegationPathKey struct{}
type partDelegationStep struct {
	row     sharedResultID
	address string
}

func enterPartDemand(ctx context.Context, row *sharedResult, address PersistedPartAddress) (context.Context, error) {
	key, err := partAddressKey(address)
	if err != nil {
		return nil, err
	}
	step := partDelegationStep{row.id, key}
	path, _ := ctx.Value(partDelegationPathKey{}).([]partDelegationStep)
	if slices.Contains(path, step) {
		return nil, fmt.Errorf("part delegation cycle at row %d address %s", row.id, key)
	}
	return context.WithValue(ctx, partDelegationPathKey{}, append(slices.Clone(path), step)), nil
}

// A proof is created only by the foreground demand selector. Frozen call
// pointers and captured output revisions bind the mapping to both exact rows.
type partDelegationProof struct {
	child, parent           *sharedResult
	target, source          PersistedPartAddress
	mapping                 PartDelegation
	childFrame, parentFrame *ResultCall
	childVersion            capturedRowRevision
}

func (proof *partDelegationProof) currentLocked(c *Cache, source *PartSourceLease, row *sharedResult) bool {
	if proof == nil || row != proof.child || source.source != proof.parent ||
		c.resultsByID[row.id] != row || c.resultsByID[proof.parent.id] != proof.parent ||
		row == proof.parent || row.loadResultCall() != proof.childFrame || proof.parent.loadResultCall() != proof.parentFrame {
		return false
	}
	if _, ok := row.deps[proof.parent.id]; !ok {
		return false
	}
	if proof.mapping.ParentResultID != uint64(proof.parent.id) || !containsPart([]PersistedPartAddress{proof.mapping.Address}, proof.source) {
		return false
	}
	if len(proof.target.OutputPath) == 0 && (proof.childFrame == nil || proof.childFrame.Receiver == nil || proof.childFrame.Receiver.ResultID != proof.mapping.ParentResultID) {
		return false
	}
	return source.sessionID != "" &&
		c.sessionSatisfiesResourceRequirementsLocked(source.sessionID, row) &&
		c.sessionSatisfiesResourceRequirementsLocked(source.sessionID, proof.parent) &&
		containsPart([]PersistedPartAddress{proof.target}, source.target) &&
		containsPart([]PersistedPartAddress{proof.source}, source.descriptor.Address)
}

// Validate the target key and value kind against each settled mount table.
// Positional storage roles may differ between the parent and child.
func validatePartDelegationTopology(child, parent PersistedRecord, target, source PersistedPartAddress, ready bool) error {
	if parent.Call == nil || parent.Call.Type == nil || parent.Call.Type.NamedType != "Container" || parent.Call.Type.Elem != nil || parent.Envelope.ObjectCodec != "core.Container" {
		return fmt.Errorf("part delegation: parent is not a Container")
	}
	var targetOutput, sourceOutput *CapturedCodecOutput
	for _, item := range []struct {
		record  PersistedRecord
		address PersistedPartAddress
		out     **CapturedCodecOutput
	}{{child, target, &targetOutput}, {parent, source, &sourceOutput}} {
		outputs, err := mapTransferredOutputs(item.record)
		if err != nil {
			return err
		}
		for _, output := range outputs {
			if containsPart([]PersistedPartAddress{item.address}, output.Address) {
				*item.out = &output
			}
			if ready && output.Address.Part == "metadata" && slices.Equal(output.Address.OutputPath, item.address.OutputPath) && output.State != "metadata" {
				return fmt.Errorf("part delegation: unsettled parent metadata")
			}
		}
	}
	if targetOutput == nil || sourceOutput == nil || target.Part != source.Part || targetOutput.ValueKind == "" || targetOutput.ValueKind != sourceOutput.ValueKind || sourceOutput.State == "absent" {
		return fmt.Errorf("part delegation: incompatible parent part %s", source.Part)
	}
	return nil
}

// Keep AcquireEquivalentPartSource's public meaning unchanged. This selector
// adds the exact parent's Ready view after ordinary Ready ties, before chains.
func (c *Cache) selectDemandPartSource(ctx context.Context, receiver AnyResult, address PersistedPartAddress, route LazyOperationRoute) (_ *PartSourceLease, pending *PartSourceLease, rerr error) {
	ordinary, err := c.AcquireEquivalentPartSource(ctx, receiver, address)
	if err != nil || route.Delegation == nil || (ordinary != nil && ordinary.readiness == PartReady) {
		return ordinary, nil, err
	}
	defer func() {
		if ordinary != nil {
			rerr = errors.Join(rerr, ordinary.Release(ctx))
		}
	}()
	row := receiver.cacheSharedResult()
	session, err := partSession(ctx)
	if err != nil {
		return nil, nil, err
	}
	childFrame := row.loadResultCall()
	childRecord, childVersion, _, err := c.probePart(ctx, row, address)
	if err != nil {
		return nil, nil, err
	}
	current, err := routePartRecord(childRecord, address)
	if err != nil {
		return nil, nil, err
	}
	mapping := current.Delegation
	if mapping == nil || mapping.ParentResultID != route.Delegation.ParentResultID || !containsPart([]PersistedPartAddress{mapping.Address}, route.Delegation.Address) {
		return nil, nil, ErrPartReselect
	}
	c.egraphMu.Lock()
	parent := c.resultsByID[sharedResultID(mapping.ParentResultID)]
	_, direct := row.deps[sharedResultID(mapping.ParentResultID)]
	if c.resultsByID[row.id] != row || parent == nil || parent == row || !direct {
		c.egraphMu.Unlock()
		return nil, nil, fmt.Errorf("part delegation: receiver %d has no distinct exact direct parent %d", row.id, mapping.ParentResultID)
	}
	if !c.sessionSatisfiesResourceRequirementsLocked(session, row) || !c.sessionSatisfiesResourceRequirementsLocked(session, parent) {
		c.egraphMu.Unlock()
		return nil, nil, fmt.Errorf("part delegation: requesting session lacks parent resources")
	}
	parentFrame := parent.loadResultCall()
	facts := c.partFactsLocked(parent)
	c.incrementIncomingOwnershipLocked(ctx, parent)
	c.egraphMu.Unlock()
	hold := &PartSourceLease{cache: c, source: parent, sourceID: uint64(parent.id), sessionID: session, target: clonePartAddress(address)}
	defer func() {
		if hold != nil {
			rerr = errors.Join(rerr, hold.Release(ctx))
		}
	}()
	record, version, probe, err := c.probePart(ctx, parent, mapping.Address)
	if err != nil {
		return nil, nil, err
	}
	ready := probe != nil && probe.LocalComplete && !probe.Busy
	if err := validatePartDelegationTopology(childRecord, record, address, mapping.Address, ready); err != nil {
		return nil, nil, err
	}
	if err := childVersion.check(row); err != nil {
		return nil, nil, err
	}
	if err := version.check(parent); err != nil {
		return nil, nil, err
	}
	proof := &partDelegationProof{child: row, parent: parent, target: clonePartAddress(address), source: clonePartAddress(mapping.Address), mapping: *mapping, childFrame: childFrame, parentFrame: parentFrame, childVersion: childVersion}
	hold.delegation, hold.record, hold.version, hold.facts = proof, record, version, facts
	if probe != nil {
		hold.descriptor = probe.Descriptor
	}
	c.egraphMu.Lock()
	valid := proof.currentLocked(c, hold, row) && c.partFactsLocked(parent) == facts
	if valid && ready {
		hold.descriptor.DependencyIDs = append(hold.descriptor.DependencyIDs, c.partResourceLeavesLocked(parent)...)
		slices.Sort(hold.descriptor.DependencyIDs)
		hold.descriptor.DependencyIDs = slices.Compact(hold.descriptor.DependencyIDs)
	}
	c.egraphMu.Unlock()
	if !valid {
		return nil, nil, ErrPartReselect
	}
	if ready {
		if ordinary != nil {
			err := ordinary.Release(ctx)
			ordinary = nil
			if err != nil {
				return nil, nil, err
			}
		}
		hold.readiness = PartReady
		selected := hold
		hold = nil
		return selected, nil, nil
	}
	if ordinary != nil {
		err := hold.Release(ctx)
		hold = nil
		if err != nil {
			return nil, nil, err
		}
		selected := ordinary
		ordinary = nil
		return selected, nil, nil
	}
	pending, hold = hold, nil
	return nil, pending, nil
}

func (c *Cache) demandDelegatedParent(ctx context.Context, pending *PartSourceLease) (rerr error) {
	defer func() { rerr = errors.Join(rerr, pending.Release(ctx)) }()
	proof := pending.delegation
	server := CurrentDagqlServer(ctx)
	if server == nil {
		server = proof.child.partGate.server.Load()
	}
	// An empty loader session selects exactly, under our explicit parent hold.
	parent, err := c.loadResultByResultID(ctx, "", server, uint64(proof.parent.id))
	if err != nil {
		return err
	}
	c.egraphMu.RLock()
	allowed := proof.currentLocked(c, pending, proof.child)
	c.egraphMu.RUnlock()
	if !allowed {
		return ErrPartReselect
	}
	// demandPart creates the parent's task and exhaustion state. The copied
	// active path is retained, but no child permit or task generation is reused.
	// In particular, do not race this wait against a faster child installation.
	return c.demandPart(ctx, parent, proof.source)
}

// Restore marks only validated pending mappings for foreground acquisition.
// Boot/decoding performs no parent load, body, provider or snapshot operation.
func markRestoredPartDelegation(row *sharedResult, envelope PersistedResultEnvelope, frame *ResultCall, links []PersistedSnapshotRefLink) {
	record := PersistedRecord{Envelope: envelope, Call: frame, SnapshotLinks: links}
	outputs, err := mapTransferredOutputs(record)
	if err != nil {
		return
	}
	for _, output := range outputs {
		if output.State != "pending" {
			continue
		}
		route, err := routePartRecord(record, output.Address)
		if err == nil && route.Delegation != nil {
			row.partGate.restoredDelegation.Store(true)
			return
		}
	}
}
