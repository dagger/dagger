package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
)

type PartReadiness uint8

const (
	PartRunnable PartReadiness = iota
	PartDownloadable
	PartReady
)

type PartDescriptor struct {
	Family        string
	Address       PersistedPartAddress
	Value         *SnapshotValue
	Absent        bool
	SnapshotID    string
	DependencyIDs []uint64
}
type PartProbe struct {
	Descriptor                                         PartDescriptor
	LocalComplete, RestoreOnly, HasLazyOperation, Busy bool
	DescriptorRev                                      uint64
	OutputRev                                          OutputRevision
	OfferRev                                           uint64
	captured                                           *partProbeCapture
}

type partProbeCapture struct {
	row     *sharedResult
	record  PersistedRecord
	version capturedRowRevision
}

// PersistedPartDescriber is pure: it neither loads references nor opens storage.
type PersistedPartDescriber interface {
	DescribeParts(PersistedPayloadVisit) ([]PartProbe, error)
}
type PartSourceLease struct {
	cache            *Cache
	source           *sharedResult
	sourceID         uint64
	offerOwner       *offerOwner
	descriptor       PartDescriptor
	target           PersistedPartAddress
	offer            *PersistedPartOffer
	readiness        PartReadiness
	route            CacheHitRoute
	offerRev         uint64
	sessionlessShare bool
	delegation       *partDelegationProof
	record           PersistedRecord
	version          capturedRowRevision
	facts            partSourceFacts
	lookup           partLookup
	sessionID        string
	once             sync.Once
	releaseErr       error
}

func (s *PartSourceLease) Descriptor() PartDescriptor {
	if s == nil {
		return PartDescriptor{}
	}
	raw, _ := json.Marshal(s.descriptor)
	var out PartDescriptor
	_ = json.Unmarshal(raw, &out)
	return out
}
func (s *PartSourceLease) Readiness() PartReadiness { return s.readiness }
func (s *PartSourceLease) Release(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		ctx = context.WithoutCancel(ctx)
		c := s.cache
		c.egraphMu.Lock()
		var queue collectionQueue
		if s.source != nil {
			queue, s.releaseErr = c.decrementIncomingOwnershipLocked(ctx, s.source, queue)
		}
		if s.offerOwner != nil {
			q, err := c.releaseOfferOwnerLocked(ctx, s.offerOwner)
			queue = append(queue, q...)
			s.releaseErr = errors.Join(s.releaseErr, err)
		}
		callbacks, err := c.collectUnownedResultsLocked(ctx, queue)
		c.egraphMu.Unlock()
		s.releaseErr = errors.Join(s.releaseErr, err, runOnReleaseFuncs(ctx, callbacks))
	})
	return s.releaseErr
}

type partLookup struct {
	frame        *ResultCall
	recipe, self digest.Digest
	inputs       []digest.Digest
	extras       []call.ExtraDigest
}

func (c *Cache) partLookupFor(row *sharedResult) (partLookup, error) {
	l := partLookup{frame: row.loadResultCall()}
	if l.frame == nil {
		return l, nil
	}
	var err error
	l.recipe, err = l.frame.deriveRecipeDigest(c)
	if err != nil {
		return l, err
	}
	var refs []ResultCallStructuralInputRef
	l.self, refs, err = l.frame.selfDigestAndInputRefs(c)
	if err != nil {
		return l, err
	}
	for _, ref := range refs {
		d, err := ref.inputDigest(c)
		if err != nil {
			return l, err
		}
		l.inputs = append(l.inputs, d)
	}
	l.extras = slices.Clone(l.frame.ExtraDigests)
	return l, nil
}

type partCandidate struct {
	row     *sharedResult
	route   CacheHitRoute
	facts   partSourceFacts
	record  PersistedRecord
	version capturedRowRevision
	probe   *PartProbe
	offer   *PersistedPartOffer
	owner   *offerOwner
	// unready is set when the row could not be captured in this scan. It has
	// no validated record, so nothing about it may be selected, its offer
	// included.
	unready bool
}
type partSourceFacts struct {
	payload, gate, offers, resources, ownership uint64
	expires                                     int64
}

func (c *Cache) partFactsLocked(row *sharedResult) partSourceFacts {
	f := partSourceFacts{offers: row.transferRevision, resources: row.requiredSessionResourcesGen.Load(), ownership: row.dependencyOwnershipRevision, expires: row.expiresAtUnix}
	if gate := row.partGate.gate.Load(); gate != nil {
		gate.mu.Lock()
		f.gate = gate.revision
		gate.mu.Unlock()
	}
	row.payloadMu.RLock()
	f.payload = row.payloadRevision
	row.payloadMu.RUnlock()
	return f
}

// This collector deliberately does not call or change ordinary early-return
// lookup helpers. Every route is collected before availability is ranked.
func (c *Cache) collectPartCandidatesLocked(receiver *sharedResult, l partLookup, sessionID string) []partCandidate {
	return c.collectPartCandidatesWithAdmissionLocked(receiver, l, func(row *sharedResult) bool { return c.sessionSatisfiesResourceRequirementsLocked(sessionID, row) })
}

func (c *Cache) collectPartCandidatesWithAdmissionLocked(receiver *sharedResult, l partLookup, allowed func(*sharedResult) bool) []partCandidate {
	now := time.Now().Unix()
	seen := map[sharedResultID]bool{}
	out := []partCandidate{}
	add := func(row *sharedResult, route CacheHitRoute) {
		if row == nil || seen[row.id] || c.resultsByID[row.id] != row {
			return
		}
		if row != receiver && row.expiresAtUnix != 0 && row.expiresAtUnix <= now {
			return
		}
		if !allowed(row) {
			return
		}
		seen[row.id] = true
		out = append(out, partCandidate{row: row, route: route})
	}
	add(receiver, CacheHitRouteRecipe)
	exact := newSharedResultSet()
	c.appendDigestResultsLocked(exact, l.recipe, now, nil)
	for row := range exact.Items() {
		add(row, CacheHitRouteRecipe)
	}
	extra := newSharedResultSet()
	for _, d := range l.extras {
		c.appendDigestResultsLocked(extra, d.Digest, now, nil)
	}
	for row := range extra.Items() {
		add(row, CacheHitRouteDigest)
	}
	if l.frame != nil {
		ids := make([]eqClassID, len(l.inputs))
		possible := true
		for i, d := range l.inputs {
			ids[i] = c.findEqClassLocked(c.egraphDigestToClass[d.String()])
			if ids[i] == 0 {
				possible = false
				break
			}
		}
		if possible {
			structural := newSharedResultSet()
			c.appendTermSetResultsLocked(structural, c.egraphTermsByTermDigest[calcEgraphTermDigest(l.self, ids)], now, nil)
			for row := range structural.Items() {
				add(row, CacheHitRouteStructural)
			}
		}
	}
	return out
}
func partSession(ctx context.Context) (string, error) {
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return "", err
	}
	if md.SessionID == "" {
		return "", fmt.Errorf("part acquisition requires a requesting session")
	}
	return md.SessionID, nil
}
func (c *Cache) offerAllowedLocked(session string, owner *offerOwner) bool {
	if session == "" || owner == nil || c.offerOwners[owner.id] != owner {
		return false
	}
	for _, dep := range owner.deps {
		if c.resultsByID[dep.id] != dep || !c.sessionSatisfiesResourceRequirementsLocked(session, dep) {
			return false
		}
	}
	return true
}
func (c *Cache) partResourceLeavesLocked(row *sharedResult) []uint64 {
	seen := map[sharedResultID]bool{}
	var ids []uint64
	var walk func(*sharedResult)
	walk = func(r *sharedResult) {
		if r == nil || seen[r.id] {
			return
		}
		seen[r.id] = true
		if r.sessionResourceHandle != "" {
			ids = append(ids, uint64(r.id))
		}
		for id := range r.deps {
			walk(c.resultsByID[id])
		}
	}
	walk(row)
	slices.Sort(ids)
	return ids
}
func chainAddressesUsable(offer *PersistedPartOffer, now int64) bool {
	for _, layer := range offer.Chain.Layers {
		a, ok := offer.Chain.Addresses[layer.Descriptor.Digest]
		if !ok || a.URL == "" || (a.ExpiresAtUnix != 0 && a.ExpiresAtUnix <= now) {
			return false
		}
	}
	return true
}
func (c *Cache) probePart(ctx context.Context, row *sharedResult, address PersistedPartAddress) (PersistedRecord, capturedRowRevision, *PartProbe, error) {
	var version capturedRowRevision
	record, err := c.capturePartRecord(ctx, row, row.imported, nil, &version)
	if err != nil {
		return record, version, nil, err
	}
	var probe *PartProbe
	key, _ := partAddressKey(address)
	err = walkTransferPayloads(&record.Envelope, record.Call, record.SnapshotLinks, nil, func(f PersistedObjectFamily, v PersistedPayloadVisit) (json.RawMessage, error) {
		d, ok := f.Transfer.(PersistedPartDescriber)
		if !ok {
			return v.Payload, nil
		}
		probes, err := d.DescribeParts(v)
		if err != nil {
			return nil, err
		}
		for _, p := range probes {
			k, _ := partAddressKey(p.Descriptor.Address)
			if k == key {
				p.Descriptor.Family = f.Name
				p.DescriptorRev = version.payload.payloadRevision
				probe = &p
				break
			}
		}
		return v.Payload, nil
	})
	if err != nil {
		return record, version, nil, err
	}
	if err = version.check(row); err != nil {
		return record, version, nil, err
	}
	if gate := row.partGate.gate.Load(); gate != nil {
		gate.mu.Lock()
		if probe != nil {
			state := gate.outputs[key]
			probe.Busy = state.phase == PartOutputInstalled
			for _, group := range gate.groups {
				if group.phase == LazyEvaluationRunning && containsPart(group.writeSet, address) {
					probe.Busy = true
				}
			}
			for _, writer := range gate.writers {
				if containsPart([]PersistedPartAddress{writer.address}, address) {
					probe.Busy = true
				}
			}
		}
		gate.mu.Unlock()
	}
	if probe != nil {
		probe.captured = &partProbeCapture{row: row, record: record, version: version}
	}
	return record, version, probe, nil
}
func (c *Cache) AcquireEquivalentPartSource(ctx context.Context, receiver AnyResult, address PersistedPartAddress) (*PartSourceLease, error) {
	source, _, err := c.scanPartSources(ctx, receiver, address, partDemandFromContext(ctx))
	return source, err
}

// scanPartSources holds every candidate only for the metadata scan. A Ready
// selection retains its donor; an admitted chain retains only its offer owner.
func (c *Cache) scanPartSources(ctx context.Context, receiver AnyResult, address PersistedPartAddress, demand *PartDemandState) (source *PartSourceLease, _ []partCandidate, rerr error) {
	if _, err := partAddressKey(address); err != nil {
		return nil, nil, err
	}
	if receiver == nil || receiver.cacheSharedResult() == nil {
		return nil, nil, fmt.Errorf("part source: detached receiver")
	}
	session, err := partSession(ctx)
	if err != nil {
		return nil, nil, err
	}
	row := receiver.cacheSharedResult()
	lookup, err := c.partLookupFor(row)
	if err != nil {
		return nil, nil, err
	}
	key, _ := partAddressKey(address)
	c.egraphMu.Lock()
	candidates := c.collectPartCandidatesLocked(row, lookup, session)
	held := 0
	defer func() {
		for i := range candidates[:held] {
			candidate := &candidates[i]
			s := &PartSourceLease{cache: c, source: candidate.row, offerOwner: candidate.owner}
			rerr = errors.Join(rerr, s.Release(ctx))
		}
		// On error no ownership escapes, including a winner selected before
		// an unselected candidate's final-release callback failed.
		if rerr != nil {
			rerr = errors.Join(rerr, source.Release(ctx))
			source = nil
		}
	}()
	for i := range candidates {
		candidate := &candidates[i]
		candidate.facts = c.partFactsLocked(candidate.row)
		c.incrementIncomingOwnershipLocked(ctx, candidate.row)
		held++
		if offer := candidate.row.partOffers[key]; offer != nil && c.offerAllowedLocked(session, offer.owner) {
			copies, err := clonePartOffers([]PersistedPartOffer{offer.record})
			if err != nil {
				c.egraphMu.Unlock()
				return nil, nil, err
			}
			candidate.offer = &copies[0]
			candidate.owner = offer.owner
			c.retainOfferOwnerLocked(offer.owner)
		}
	}
	c.egraphMu.Unlock()
	for i := range candidates {
		candidate := &candidates[i]
		candidate.record, candidate.version, candidate.probe, err = c.probePart(ctx, candidate.row, address)
		if errors.Is(err, ErrPersistStateNotReady) {
			// What holds the receiver is another capture or a sibling part's
			// publication, which concurrent demands of one row make ordinary
			// and which ends soon: scan again. Another row can stay unready
			// for as long as its own evaluation runs, so it is passed over.
			if candidate.row == row {
				return nil, nil, partRefusedBy("scan: receiver not ready", err)
			}
			candidate.record, candidate.version, candidate.probe, candidate.unready = PersistedRecord{}, capturedRowRevision{}, nil, true
			continue
		}
		if err != nil {
			return nil, nil, err
		}
	}
	// An own completed descriptor is final even if its storage has disappeared.
	best := -1
	rank := PartRunnable
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.unready {
			continue
		}
		p := candidate.probe
		r := PartRunnable
		if p != nil && p.LocalComplete && !p.Busy {
			r = PartReady
		} else if candidate.offer != nil && (candidate.row == row || c.partOfferAvailable(*candidate.offer, time.Now().Unix())) && (demand == nil || !demand.exhausted(candidate.row.id, address, candidate.offer, candidate.facts.offers)) {
			r = PartDownloadable
		}
		if p != nil && candidate.row == row && p.LocalComplete {
			if p.Busy {
				return nil, candidates, partRefused("scan: own part complete but busy")
			}
			best = i
			rank = PartReady
			break
		}
		if r > rank {
			best = i
			rank = r
		}
	}
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.probe != nil {
			if err := candidate.version.check(candidate.row); err != nil {
				return nil, candidates, partRefused("scan: candidate version")
			}
		}
	}
	c.egraphMu.Lock()
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.facts != c.partFactsLocked(candidate.row) || !c.sessionSatisfiesResourceRequirementsLocked(session, candidate.row) {
			c.egraphMu.Unlock()
			return nil, candidates, partRefused("scan: candidate facts or session")
		}
	}
	if best < 0 {
		c.egraphMu.Unlock()
		return nil, candidates, nil
	}
	selected := &candidates[best]
	eligible := false
	for _, current := range c.collectPartCandidatesLocked(row, lookup, session) {
		if current.row == selected.row {
			eligible = true
			break
		}
	}
	if !eligible {
		c.egraphMu.Unlock()
		return nil, candidates, partRefused("scan: selected row no longer a candidate")
	}
	source = &PartSourceLease{cache: c, sourceID: uint64(selected.row.id), target: clonePartAddress(address), readiness: rank, route: selected.route, record: selected.record, version: selected.version, facts: selected.facts, lookup: lookup, sessionID: session, offerRev: selected.facts.offers}
	if rank == PartReady {
		source.source = selected.row
		selected.row = nil
		source.descriptor = selected.probe.Descriptor
		source.descriptor.DependencyIDs = append(source.descriptor.DependencyIDs, c.partResourceLeavesLocked(source.source)...)
		slices.Sort(source.descriptor.DependencyIDs)
		source.descriptor.DependencyIDs = slices.Compact(source.descriptor.DependencyIDs)
	} else {
		if !c.offerAllowedLocked(session, selected.owner) {
			c.egraphMu.Unlock()
			return nil, candidates, partRefused("scan: offer owner not allowed")
		}
		source.offerOwner = selected.owner
		selected.owner = nil
		source.offer = selected.offer
		source.descriptor = PartDescriptor{Address: clonePartAddress(selected.offer.Address), Value: &selected.offer.Value, DependencyIDs: nil}
		if selected.probe != nil {
			source.descriptor.Family = selected.probe.Descriptor.Family
		}
		for _, svc := range selected.offer.Value.Services {
			source.descriptor.DependencyIDs = append(source.descriptor.DependencyIDs, svc.ServiceResultID)
		}
		for _, dep := range source.offerOwner.deps {
			source.descriptor.DependencyIDs = append(source.descriptor.DependencyIDs, c.partResourceLeavesLocked(dep)...)
		}
		slices.Sort(source.descriptor.DependencyIDs)
		source.descriptor.DependencyIDs = slices.Compact(source.descriptor.DependencyIDs)
	}
	c.egraphMu.Unlock()
	return source, candidates, nil
}

var ErrPartReselect = errors.New("part sources changed; reselect")

// PartDemandState remains open for batch 5's independently keyed renewal set.
type PartDemandState struct {
	mu               sync.Mutex
	exhaustedContent map[string]struct{}
	failures         []partContentFailure
	revision         uint64
}

func partContentKey(id sharedResultID, address PersistedPartAddress, offer *PersistedPartOffer, revision uint64) string {
	raw, _ := json.Marshal(struct {
		ID       sharedResultID
		Address  PersistedPartAddress
		Value    SnapshotValue
		Layers   any
		Revision uint64
	}{id, address, offer.Value, offer.Chain.Layers, revision})
	return string(raw)
}
func (s *PartDemandState) exhausted(id sharedResultID, address PersistedPartAddress, offer *PersistedPartOffer, revision uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.exhaustedContent[partContentKey(id, address, offer, revision)]
	return ok
}

func (c *Cache) partSourceDependenciesLocked(source *PartSourceLease) []uint64 {
	ids := slices.Clone(source.descriptor.DependencyIDs)
	if source.offerOwner != nil {
		for _, dep := range source.offerOwner.deps {
			ids = append(ids, c.partResourceLeavesLocked(dep)...)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// Retry only a pure stale-observation result. A cleanup failure joined to a
// stale observation must reach the caller instead of being lost in a retry.
func partCanReselect(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !partCanReselect(cause) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return partCanReselect(wrapped.Unwrap())
	}
	return err == ErrPartReselect || err == ErrPersistStateNotReady
}

// ownPartRequirementsFitLocked deliberately excludes offer-owner resources.
func (c *Cache) ownPartRequirementsFitLocked(receiver, dependency *sharedResult) bool {
	if receiver == nil || dependency == nil || c.resultsByID[dependency.id] != dependency {
		return false
	}
	if dependency.requiredSessionResources == nil || dependency.requiredSessionResources.Empty() {
		return true
	}
	return receiver.requiredSessionResources != nil && receiver.requiredSessionResources.Subset(dependency.requiredSessionResources)
}

// The caller holds the rows and probes outside E. Lookup preparation can
// resolve persisted numeric references, so it must precede the E section.
func (c *Cache) newSessionlessPartSourceLease(ctx context.Context, receiver, donor *sharedResult, target, address PersistedPartAddress, probe PartProbe) (*PartSourceLease, error) {
	if receiver == nil || donor == nil {
		return nil, fmt.Errorf("sessionless part source requires receiver and donor")
	}
	lookup, err := c.partLookupFor(receiver)
	if err != nil {
		return nil, err
	}
	if probe.captured == nil {
		return nil, partRefused("sessionless source: probe without capture")
	}
	if err := probe.captured.version.check(donor); err != nil {
		return nil, err
	}
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	return c.newSessionlessPartSourceLeaseLocked(ctx, receiver, donor, target, address, probe, lookup)
}

// E held; lookup was prepared outside E. This admission boundary revalidates
// the frame and current own requirements before taking its own donor hold.
func (c *Cache) newSessionlessPartSourceLeaseLocked(ctx context.Context, receiver, donor *sharedResult, target, address PersistedPartAddress, probe PartProbe, lookup partLookup) (*PartSourceLease, error) {
	if receiver == nil || donor == nil || !receiver.imported || c.resultsByID[receiver.id] != receiver || c.resultsByID[donor.id] != donor {
		return nil, fmt.Errorf("sessionless part source requires registered rows and an imported receiver")
	}
	if _, err := partAddressKey(target); err != nil {
		return nil, err
	}
	key, err := partAddressKey(address)
	if err != nil {
		return nil, err
	}
	probedKey, err := partAddressKey(probe.Descriptor.Address)
	if err != nil || key != probedKey || !probe.LocalComplete || probe.Busy || probe.captured == nil || probe.captured.row != donor {
		return nil, partRefused("sessionless source: probe not usable")
	}

	// Resource filtering is performed below using only the receiver's current
	// own set. The normal collector and foreground session filter stay intact.
	route, eligible := c.sessionlessPartEquivalentLocked(receiver, donor, lookup)
	if !eligible || !c.ownPartRequirementsFitLocked(receiver, donor) {
		return nil, partRefused("sessionless source: donor not eligible")
	}
	source := &PartSourceLease{cache: c, source: donor, sourceID: uint64(donor.id), descriptor: probe.Descriptor, target: clonePartAddress(target), readiness: PartReady, route: route, sessionlessShare: true, record: probe.captured.record, version: probe.captured.version, lookup: lookup, facts: c.partFactsLocked(donor)}
	source.descriptor = source.Descriptor()
	source.descriptor.DependencyIDs = append(source.descriptor.DependencyIDs, c.partResourceLeavesLocked(donor)...)
	slices.Sort(source.descriptor.DependencyIDs)
	source.descriptor.DependencyIDs = slices.Compact(source.descriptor.DependencyIDs)
	for _, id := range source.descriptor.DependencyIDs {
		if !c.ownPartRequirementsFitLocked(receiver, c.resultsByID[sharedResultID(id)]) {
			return nil, partRefused("sessionless source: dependency exceeds receiver requirements")
		}
	}
	source.offerRev = source.facts.offers
	c.incrementIncomingOwnershipLocked(ctx, donor)
	return source, nil
}

func (c *Cache) sessionlessPartEquivalentLocked(receiver, donor *sharedResult, lookup partLookup) (CacheHitRoute, bool) {
	if receiver.loadResultCall() != lookup.frame {
		return "", false
	}
	for _, candidate := range c.collectPartCandidatesWithAdmissionLocked(receiver, lookup, func(row *sharedResult) bool { return c.ownPartRequirementsFitLocked(receiver, row) }) {
		if candidate.row == donor {
			return candidate.route, true
		}
	}
	return "", false
}

func (c *Cache) partSourceReferenceAllowedLocked(receiver *sharedResult, source *PartSourceLease, dep *sharedResult) bool {
	if source.sessionlessShare {
		return receiver.imported && c.ownPartRequirementsFitLocked(receiver, dep)
	}
	return source.sessionID != "" && c.sessionSatisfiesResourceRequirementsLocked(source.sessionID, dep)
}

func (c *Cache) partOfferAvailable(offer PersistedPartOffer, now int64) bool {
	if binding := c.partContentSource.Load(); binding != nil {
		return binding.source.Available(offer, now)
	}
	return (fixedPartContentSource{}).Available(offer, now)
}
