package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"
)

// MergeValues merges a bundle of transferred values into the cache, and
// replaces a fresh import: every record lands on its recipe's current entry
// when the cache has one. The Cloud calls it to store an engine's export, from
// that engine's cache; an engine calls it for the Cloud's merge, from
// cloudCacheID.
//
// It derives each record's recipe outside the graph lock, in a private cache
// with provisional numbers, as an import did. An engine prepares a record its
// cache already holds a value for from its call and offers only, since merge
// keeps that value. Then, in one hold of egraphMu, it picks every record's
// target and decides it, relocates the records to the final numbers and runs
// every check that can fail, and only then changes the cache. The decision
// starts again in two cases: an entry whose dependency attachment is still
// open is waited on first, and a decision that installs a record prepared from
// its call and offers only prepares the bundle in full first.
//
// For each record, by its recipe's current entry:
//   - none: a new entry stores the record;
//   - an entry with no value, known only through holdings: it stores the
//     record, and its replacement count stays;
//   - an unexpired value: the entry keeps it, since the same recipe means an
//     interchangeable value;
//   - an expired value and an unexpired record: the Cloud replaces the value
//     in place. An engine does the same when nothing uses the entry, and
//     otherwise retires it and stores the record in a new entry;
//   - both expired: the entry keeps its value and adds only what it lacks.
//
// Every target learns the record's identity, and the sender's holding: on an
// engine its Cloud holding, on the Cloud the sending engine's holding at the
// record's replacement count. Unexpired roots are retained. An engine admits
// the bundle's parts as offers by what the target kept; the Cloud admits none
// until it has the bytes (SetStoredPart). An expired root is answered expired,
// and only what the live roots need is merged.
//
// The reply reports every target as the commit left it, read under the same
// lock, with the cache's generation and clock. Merge runs in no session and
// emits no span.
func (c *Cache) MergeValues(ctx context.Context, from CacheID, input ValueBundle) (MergeReply, error) {
	switch {
	case from == "":
		return MergeReply{}, fmt.Errorf("merge values: no sending cache")
	case c.blobBacked && from == cloudCacheID:
		return MergeReply{}, fmt.Errorf("merge values: the Cloud merges from engine caches")
	case !c.blobBacked && from != cloudCacheID:
		return MergeReply{}, fmt.Errorf("merge values: an engine merges only from the Cloud, not %q", from)
	}
	op, err := c.beginCacheOperation()
	if err != nil {
		return MergeReply{}, err
	}
	defer op.finish(false)
	prep, err := c.prepareValueMerge(ctx, input, true)
	if err != nil {
		return MergeReply{}, err
	}
	// Reading an entry's complete parts captures its record, which needs the
	// graph lock free. The reply reads them back only while that capture
	// still describes the entry's value.
	c.warmMergeTargetParts(ctx, prep)
	if c.testBeforeTransferCommit != nil {
		c.testBeforeTransferCommit()
	}
	for {
		c.egraphMu.Lock()
		locked := time.Now()
		if target, wait := c.mergeAttachmentWaitLocked(prep); wait != nil {
			c.egraphMu.Unlock()
			if c.testMergeWaitsOnAttachment != nil {
				c.testMergeWaitsOnAttachment(target)
			}
			select {
			case <-wait:
			case <-ctx.Done():
				return MergeReply{}, context.Cause(ctx)
			}
			continue
		}
		commit, err := c.planValueMergeLocked(ctx, from, prep)
		if errors.Is(err, errMergeNeedsFullPrep) {
			c.egraphMu.Unlock()
			if prep, err = c.prepareValueMerge(ctx, input, false); err != nil {
				return MergeReply{}, err
			}
			continue
		}
		if err != nil {
			c.egraphMu.Unlock()
			return MergeReply{}, err
		}
		reply, finish := c.commitValueMergeLocked(ctx, commit)
		reply.Committed, reply.CommitHold = true, time.Since(locked)
		c.egraphMu.Unlock()
		err = finish(ctx)
		if c.testAfterTransferCommit != nil {
			c.testAfterTransferCommit()
		}
		return reply, err
	}
}

// MergeReply is what a merge committed, read under the lock that committed it.
type MergeReply struct {
	// Values has one entry per merged record, in dependency order.
	Values []MergedValue
	// Roots has one entry per root of the bundle.
	Roots []MergedRoot
	// Generation and EngineTimeUnixNano are the cache's start count and clock
	// at the commit. They order the reply's retention among the cache's other
	// observations.
	Generation         uint64
	EngineTimeUnixNano int64
	// Change is, on the Cloud, what the sender's holdings changed, for the
	// service's next CollectRemoteHoldings.
	Change RemoteChange
	// Committed reports that the merge changed the cache. A committed merge
	// can still return an error, from releasing a replaced value after the
	// lock; the reply then describes what the cache holds all the same.
	Committed bool
	// CommitHold is how long the commit held egraphMu, from taking it to
	// releasing it.
	CommitHold time.Duration
}

// MergedValue is one merged record's target as the commit left it.
type MergedValue struct {
	Ordinal      TransferOrdinal
	Number       uint64
	Replacements uint64
	// ExpiresAtUnix is the target's own expiry (0: none).
	ExpiresAtUnix int64
	// Deps are the target's actual direct dependencies by number: a kept
	// value's own, which can differ from the record's.
	Deps []uint64
	// Parts are the target's complete parts, and OfferedParts those it has an
	// offer of and not the bytes.
	Parts        []PersistedPartAddress
	OfferedParts []PersistedPartAddress
}

// MergedRoot is one root of the bundle: its target and retention, or expired.
type MergedRoot struct {
	Ordinal TransferOrdinal
	// Expired marks a root that had expired when the merge decided: it was
	// skipped, with whatever only it needed.
	Expired                bool
	Number                 uint64
	Retained               bool
	RetentionExpiresAtUnix int64
}

// Imported returns the unexpired roots' numbers.
func (r MergeReply) Imported() []ImportedValue {
	out := make([]ImportedValue, 0, len(r.Roots))
	for _, root := range r.Roots {
		if !root.Expired {
			out = append(out, ImportedValue{Ordinal: root.Ordinal, ResultID: root.Number})
		}
	}
	return out
}

// valueMergePrep is what merge prepares outside the graph lock: the validated
// bundle, its records in dependency order, and each record's identity, derived
// in a private cache under provisional numbers. A record that becomes a new
// entry keeps its provisional number, firstID + ordinal - 1.
type valueMergePrep struct {
	bundle  ValueBundle
	order   []uint64
	index   map[uint64]*TransferredValue
	firstID uint64
	plans   map[uint64]transferIdentityPlan
	// records are the bundle's records at their provisional numbers, by
	// ordinal, and refs the provisional numbers each one references, itself
	// included. A light record is relocated in its call and offers only, and
	// has no refs.
	records map[uint64]PersistedRecord
	refs    map[uint64][]uint64
	// completeParts are the part keys each record but a light one proves
	// complete, by ordinal. A part key is an address and its completeness,
	// which the record's payload decides; relocation rewrites only reference
	// numbers, never to or from zero, so the keys read at provisional numbers
	// are the ones at the final numbers.
	completeParts map[uint64][]string
	// light are the records prepared from their call and offers only: the
	// cache held a value for their recipe that merge keeps.
	light map[uint64]bool
}

// errMergeNeedsFullPrep is planValueMergeLocked's answer when it installs a
// light record: the merge prepares the bundle in full and decides again.
var errMergeNeedsFullPrep = errors.New("merge values: a light record is installed")

// prepareValueMerge validates the bundle and derives each record's identity.
// On an engine, the bundle's outputs with chains become offers embedded in
// their records; the Cloud drops the bundle's parts, since the upload decides
// what it stores.
//
// A record's identity comes from its call. With skipKept, on an engine, a
// record whose recipe's current entry holds a value merge would keep, as the
// cache stands, is prepared light: from its call and offers only, since merge
// doesn't install it. Its payload is neither validated by its codec nor
// relocated; its shape, its call's references and its offers' are checked
// (validateTransferRecordShape).
func (c *Cache) prepareValueMerge(ctx context.Context, input ValueBundle, skipKept bool) (*valueMergePrep, error) {
	bundle, err := cloneValueBundle(input)
	if err != nil {
		return nil, err
	}
	// The bundle is validated once, before its outputs become offers: each
	// output with a chain is checked as the offer it becomes, against the
	// same slots, and the ownership walk takes its owner's dependencies in
	// the same order. Each payload merge uses is validated below.
	order, err := validateValueBundleShape(bundle)
	if err != nil {
		return nil, err
	}
	c.embedMergedParts(&bundle)
	if c.blobBacked {
		// Dropping the parts drops their owners' edges, so the roots'
		// closure is checked again without them.
		if order, err = validateValueBundleShape(bundle); err != nil {
			return nil, err
		}
	}
	prep := &valueMergePrep{bundle: bundle, order: order, index: map[uint64]*TransferredValue{}, plans: map[uint64]transferIdentityPlan{}, records: map[uint64]PersistedRecord{}, refs: map[uint64][]uint64{}, completeParts: map[uint64][]string{}}
	for i := range prep.bundle.Values {
		row := &prep.bundle.Values[i]
		prep.index[uint64(row.Ordinal)] = row
	}

	c.egraphMu.Lock()
	if c.nextSharedResultID == 0 {
		c.nextSharedResultID = 1
	}
	prep.firstID = uint64(c.nextSharedResultID)
	c.nextSharedResultID += sharedResultID(len(prep.bundle.Values))
	c.egraphMu.Unlock()

	relocate := func(ref *PersistedRef) error {
		if ref.RecipeID != nil || ref.ResultID == 0 {
			return nil
		}
		if prep.index[ref.ResultID] == nil {
			return fmt.Errorf("missing transfer reference %d", ref.ResultID)
		}
		ref.ResultID = prep.firstID + ref.ResultID - 1
		return nil
	}
	// Every record is first relocated in its call and offers, which is all
	// its identity and the bundle's ownership graph read. Relocation maps
	// ordinals one to one onto nonzero numbers, so what the validation
	// checked at ordinals holds at provisional numbers: neither the records
	// nor the private cache's ownership are checked again.
	private := &Cache{resultsByID: map[sharedResultID]*sharedResult{}}
	for _, id := range order {
		row := prep.index[id]
		rec := row.Record
		rec.ResultID = prep.firstID + id - 1
		rec.Envelope.ResultID = rec.ResultID
		rec.Envelope.Imported = true
		rec.Call = rec.Call.clone()
		if err := visitResultCallReferences(rec.Call, nil, relocate); err != nil {
			return nil, err
		}
		offers, err := clonePartOffers(rec.Envelope.PendingOffers)
		if err != nil {
			return nil, err
		}
		for i := range offers {
			if err := visitPersistedPartOffer(&offers[i], nil, relocate); err != nil {
				return nil, err
			}
		}
		rec.Envelope.PendingOffers = offers
		deps := make(map[sharedResultID]struct{}, len(row.DependencyIDs))
		for _, dep := range row.DependencyIDs {
			deps[sharedResultID(prep.firstID+dep-1)] = struct{}{}
		}
		prep.records[id] = rec
		res := &sharedResult{id: sharedResultID(rec.ResultID), imported: true, isObject: rec.Envelope.Kind == persistedResultKindObject, persistedEnvelope: &rec.Envelope, deps: deps, expiresAtUnix: row.ExpiresAtUnix, sessionResourceHandle: rec.Envelope.SessionResourceHandle}
		res.storeResultCall(rec.Call)
		if !c.blobBacked {
			// The private offers attach to a Cloud holding, as they will in
			// the cache.
			res.noteCloudCopyLocked(row.SenderNumber, true, row.ExpiresAtUnix)
		}
		private.resultsByID[res.id] = res
	}
	for _, res := range private.resultsByID {
		if err := walkTransferCalls(res.loadResultCall(), func(*ResultCall) error { return nil }, func(ref *ResultCallRef) error {
			ref.shared = private.resultsByID[sharedResultID(ref.ResultID)]
			return nil
		}); err != nil {
			return nil, err
		}
		for depID := range res.deps {
			dep := private.resultsByID[depID]
			private.rememberDependencyEdgeLocked(res, dep)
			private.incrementIncomingOwnershipLocked(ctx, dep)
		}
	}
	// The bundle's offers are checked in its own ownership graph here, and
	// against the cache's when the merge decides.
	if err := private.restoreOfferOwnersLocked(ctx); err != nil {
		return nil, err
	}
	for i, id := range order {
		plan, err := private.transferIdentityPlanOf(private.resultsByID[sharedResultID(prep.firstID+id-1)])
		if err != nil {
			return nil, err
		}
		prep.plans[id] = plan
		if c.testTransferPlanPrepared != nil {
			if err := c.testTransferPlanPrepared(i + 1); err != nil {
				return nil, err
			}
		}
	}

	if skipKept && !c.blobBacked {
		prep.light = c.keptMergeRecords(prep)
	}
	// Every other record is prepared with its payload.
	for _, id := range order {
		if !prep.light[id] {
			if err := prep.prepareRecordPayload(id, relocate); err != nil {
				return nil, err
			}
		}
	}
	return prep, nil
}

// prepareRecordPayload validates the record of ordinal id with its payload,
// and relocates it in full to its provisional numbers with relocate.
func (prep *valueMergePrep) prepareRecordPayload(id uint64, relocate PersistedRefVisitor) error {
	row := prep.index[id]
	if err := validateTransferRecord(row.Record, row.DependencyIDs); err != nil {
		return fmt.Errorf("row %d: %w", id, err)
	}
	if _, err := mapTransferredOutputs(row.Record); err != nil {
		return err
	}
	var refs []uint64
	rec, err := VisitEncodedReferences(row.Record, func(ref *PersistedRef) error {
		if err := relocate(ref); err != nil {
			return err
		}
		if ref.RecipeID == nil && ref.ResultID != 0 {
			refs = append(refs, ref.ResultID)
		}
		return nil
	})
	if err != nil {
		return err
	}
	slices.Sort(refs)
	prep.refs[id] = slices.Compact(refs)
	rec.Envelope.Imported = true
	// The record shares its call with the private cache's entry, whose
	// identity merge applies.
	rec.Call = prep.records[id].Call
	prep.completeParts[id] = recordCompletePartKeys(rec)
	prep.records[id] = rec
	return nil
}

// keptMergeRecords returns the records merge would keep, deciding now
// (decideMergeRowLocked): their recipe's current entry has a value, and
// either it hasn't expired or the record has. A wrong guess costs no
// correctness: planValueMergeLocked refuses to install a light record.
func (c *Cache) keptMergeRecords(prep *valueMergePrep) map[uint64]bool {
	kept := map[uint64]bool{}
	now := time.Now().Unix()
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	for _, id := range prep.order {
		cur := c.currentEntryForRecipeLocked(prep.plans[id].recipe)
		if cur == nil || cur.noValueLocked() {
			continue
		}
		expiry := prep.index[id].ExpiresAtUnix
		if !c.resultExpiredAtLocked(cur, now) || expiry != 0 && expiry <= now {
			kept[id] = true
		}
	}
	return kept
}

// embedMergedParts prepares the bundle's parts for the cache. On an engine,
// the outputs with chains become offers embedded in their records. The Cloud
// drops every part, since the upload decides what it stores.
func (c *Cache) embedMergedParts(bundle *ValueBundle) {
	if c.blobBacked {
		for i := range bundle.Values {
			bundle.Values[i].Record.Envelope.PendingOffers = nil
		}
	} else {
		index := map[uint64]*TransferredValue{}
		for i := range bundle.Values {
			index[uint64(bundle.Values[i].Ordinal)] = &bundle.Values[i]
		}
		for _, output := range bundle.Outputs {
			if output.Chain == nil {
				continue
			}
			row := index[uint64(output.Ordinal)]
			row.Record.Envelope.PendingOffers = append(row.Record.Envelope.PendingOffers, PersistedPartOffer{Address: output.Address, Value: *output.Value, Chain: *output.Chain, Owner: *output.Owner})
		}
	}
	bundle.Outputs = nil
}

// cloneValueBundle copies a bundle for merge to change, leaving the input
// untouched, without encoding its payloads: each record field by field, and
// the outputs, which carry no payload, by a JSON round trip.
func cloneValueBundle(in ValueBundle) (ValueBundle, error) {
	out := ValueBundle{Version: in.Version, Roots: slices.Clone(in.Roots)}
	if in.Values != nil {
		out.Values = make([]TransferredValue, len(in.Values))
	}
	for i, value := range in.Values {
		env, err := clonePersistedEnvelope(value.Record.Envelope)
		if err != nil {
			return ValueBundle{}, err
		}
		value.Record.Envelope = env
		value.Record.Call = value.Record.Call.clone()
		value.Record.SnapshotLinks = cloneSnapshotRefLinks(value.Record.SnapshotLinks)
		value.DependencyIDs = slices.Clone(value.DependencyIDs)
		out.Values[i] = value
	}
	if in.Outputs != nil {
		raw, err := json.Marshal(in.Outputs)
		if err != nil {
			return ValueBundle{}, err
		}
		if err := json.Unmarshal(raw, &out.Outputs); err != nil {
			return ValueBundle{}, err
		}
	}
	return out, nil
}

// transferIdentityPlanOf derives the identity of res, a record in the private
// cache c: its recipe, and its term's self digest, inputs and provenance.
func (c *Cache) transferIdentityPlanOf(res *sharedResult) (transferIdentityPlan, error) {
	frame := res.loadResultCall()
	recipe, err := frame.deriveRecipeDigest(c)
	if err != nil {
		return transferIdentityPlan{}, err
	}
	self, refs, err := frame.selfDigestAndInputRefs(c)
	if err != nil {
		return transferIdentityPlan{}, err
	}
	plan := transferIdentityPlan{row: res, recipe: recipe, self: self}
	plan.provenance, err = c.inputProvenanceForRefs(refs)
	if err != nil {
		return transferIdentityPlan{}, err
	}
	for _, ref := range refs {
		dig, err := ref.inputDigest(c)
		if err != nil {
			return transferIdentityPlan{}, err
		}
		plan.inputs = append(plan.inputs, dig)
	}
	return plan, nil
}

// warmMergeTargetParts reads the complete parts of each recipe's current
// entry, so the reply can report them without capturing under the lock.
func (c *Cache) warmMergeTargetParts(ctx context.Context, prep *valueMergePrep) {
	c.egraphMu.RLock()
	targets := make([]*sharedResult, 0, len(prep.plans))
	for _, plan := range prep.plans {
		if res := c.currentEntryForRecipeLocked(plan.recipe); res != nil && !res.noValueLocked() {
			targets = append(targets, res)
		}
	}
	c.egraphMu.RUnlock()
	for _, res := range targets {
		c.completePartKeys(ctx, res)
	}
}

// mergeAttachmentWaitLocked returns the attachment to wait on when a record's
// target is still attaching: merge changes nothing until every target is
// settled. Requires egraphMu.
func (c *Cache) mergeAttachmentWaitLocked(prep *valueMergePrep) (*sharedResult, <-chan struct{}) {
	for _, id := range prep.order {
		res := c.currentEntryForRecipeLocked(prep.plans[id].recipe)
		if res == nil || res.attachmentState() != resultAttachmentOpen {
			continue
		}
		res.attachDepsMu.Lock()
		wait := res.attachDepsWaitCh
		res.attachDepsMu.Unlock()
		return res, wait
	}
	return nil, nil
}

// mergeAction is what merge does with one record.
type mergeAction uint8

const (
	// mergeCreate: the recipe has no current entry; a new entry stores the
	// record.
	mergeCreate mergeAction = iota
	// mergeStore: the current entry has no value; it stores the record.
	mergeStore
	// mergeKeep: the current entry keeps its value.
	mergeKeep
	// mergeReplace: the current entry's value has expired, and the record
	// replaces it in place.
	mergeReplace
	// mergeRetire: the current entry's expired value is in use; the entry is
	// retired and a new entry stores the record.
	mergeRetire
)

// installs reports whether the action stores the record as a value.
func (a mergeAction) installs() bool {
	return a != mergeKeep
}

// mergeRow is one record's decision.
type mergeRow struct {
	ordinal uint64
	value   *TransferredValue
	plan    transferIdentityPlan
	action  mergeAction
	// target is the recipe's entry before the merge, if any.
	target *sharedResult
	// res is the entry that holds the record's value after the merge: the
	// target, or a new entry.
	res *sharedResult
	// expired marks an incoming record that had expired when merge decided.
	expired bool
	// keptExpired marks a kept value that had expired.
	keptExpired bool
	// rec and deps are the record relocated to the final numbers, for an
	// action that installs it.
	rec  PersistedRecord
	deps []sharedResultID
	// offers are the record's incoming offers, and admitted those this row's
	// selection admitted, in commit order: a later row can still supersede
	// one for the same part. The final set per entry and part key is
	// commit.finalOffers (selectMergedOffersLocked).
	offers   []PersistedPartOffer
	admitted []PersistedPartOffer
}

// valueMergeCommit is a merge decided and checked, ready to change the cache.
type valueMergeCommit struct {
	prep      *valueMergePrep
	from      CacheID
	rows      []*mergeRow
	byOrdinal map[uint64]*mergeRow
	// byNumber maps each row's entry number to the entry it lands on, and
	// installed each entry that takes a record to the row it takes.
	byNumber  map[sharedResultID]*sharedResult
	installed map[*sharedResult]*mergeRow
	// finalOffers holds, for each entry a row admits offers to, its offer
	// per part key as the commit leaves it, and newOffers the keys whose
	// final offer is an admitted one (selectMergedOffersLocked).
	finalOffers map[*sharedResult]map[string]PersistedPartOffer
	newOffers   map[*sharedResult]map[string]bool
	roots       []MergedRoot
	now         int64
}

// planValueMergeLocked decides every record and runs every check that can
// fail, without changing the cache. Requires egraphMu for writing.
func (c *Cache) planValueMergeLocked(ctx context.Context, from CacheID, prep *valueMergePrep) (*valueMergeCommit, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	// Every expiry is judged by the clock read under the lock that decides.
	now := time.Now().Unix()
	commit := &valueMergeCommit{prep: prep, from: from, byOrdinal: map[uint64]*mergeRow{}, now: now}

	// An expired root is skipped, with whatever only it needs.
	merged := map[uint64]bool{}
	var need func(uint64)
	need = func(id uint64) {
		if merged[id] {
			return
		}
		merged[id] = true
		row := prep.index[id]
		for _, dep := range row.DependencyIDs {
			need(dep)
		}
		for _, offer := range row.Record.Envelope.PendingOffers {
			for _, dep := range offer.Owner.DependencyIDs {
				need(dep)
			}
		}
	}
	for _, root := range prep.bundle.Roots {
		expiry := prep.index[uint64(root.Ordinal)].ExpiresAtUnix
		expired := root.ExpiresAtUnix != 0 && root.ExpiresAtUnix <= now || expiry != 0 && expiry <= now
		commit.roots = append(commit.roots, MergedRoot{Ordinal: root.Ordinal, Expired: expired})
		if !expired {
			need(uint64(root.Ordinal))
		}
	}

	// A recipe is decided once, by its best record: the first unexpired one
	// in dependency order, or the first if all have expired. The recipe's
	// other records land on the same entry.
	best := map[string]*mergeRow{}
	for _, id := range prep.order {
		if !merged[id] {
			continue
		}
		value := prep.index[id]
		row := &mergeRow{ordinal: id, value: value, plan: prep.plans[id]}
		row.expired = value.ExpiresAtUnix != 0 && value.ExpiresAtUnix <= now
		commit.rows = append(commit.rows, row)
		commit.byOrdinal[id] = row
		recipe := row.plan.recipe.String()
		if b := best[recipe]; b == nil || b.expired && !row.expired {
			best[recipe] = row
		}
	}
	for _, row := range commit.rows {
		if b := best[row.plan.recipe.String()]; b == row {
			c.decideMergeRowLocked(row, now, sharedResultID(prep.firstID+row.ordinal-1))
		}
	}
	for _, row := range commit.rows {
		b := best[row.plan.recipe.String()]
		if b == row {
			continue
		}
		// Its parts are admitted against the value the entry holds after
		// the merge: the best record's, or the one the entry keeps.
		row.action, row.target, row.res = mergeKeep, b.res, b.res
		row.keptExpired = b.keptExpired || b.action.installs() && b.expired
	}
	// Everything after the decisions reads the payloads and parts of the
	// records merge installs, which a light record doesn't have: the cache
	// changed since the preparation.
	if commit.installsLight() {
		return nil, errMergeNeedsFullPrep
	}
	commit.byNumber = make(map[sharedResultID]*sharedResult, len(commit.rows))
	commit.installed = map[*sharedResult]*mergeRow{}
	for _, row := range commit.rows {
		commit.byNumber[row.res.id] = row.res
		if row.action.installs() {
			commit.installed[row.res] = row
		}
	}

	if err := c.relocateMergeRowsLocked(commit); err != nil {
		return nil, err
	}
	if err := c.selectMergedOffersLocked(commit); err != nil {
		return nil, err
	}
	if err := c.checkMergeGraphLocked(commit); err != nil {
		return nil, err
	}
	return commit, nil
}

// installsLight reports whether the commit installs a light record.
func (commit *valueMergeCommit) installsLight() bool {
	for _, row := range commit.rows {
		if row.action.installs() && commit.prep.light[row.ordinal] {
			return true
		}
	}
	return false
}

// decideMergeRowLocked decides a record whose recipe no earlier record of the
// bundle has, by the recipe's current entry; fresh is the number a new entry
// takes. Requires egraphMu.
func (c *Cache) decideMergeRowLocked(row *mergeRow, now int64, fresh sharedResultID) {
	cur := c.currentEntryForRecipeLocked(row.plan.recipe)
	row.target = cur
	switch {
	case cur == nil:
		row.action, row.res = mergeCreate, &sharedResult{id: fresh}
	case cur.noValueLocked():
		row.action, row.res = mergeStore, cur
	case !c.resultExpiredAtLocked(cur, now):
		row.action, row.res = mergeKeep, cur
	case row.expired:
		row.action, row.res, row.keptExpired = mergeKeep, cur, true
	case c.blobBacked || !c.resultInUseLocked(cur):
		// The Cloud always replaces: nothing of it uses an entry across an
		// unlock.
		row.action, row.res = mergeReplace, cur
	default:
		row.action, row.res = mergeRetire, &sharedResult{id: fresh}
	}
}

// relocateMergeRowsLocked relocates each decided record from its provisional
// numbers to the final ones; each row's incoming offers are relocated too. A
// new entry keeps its provisional number, so a record whose own number and
// references all keep their provisional numbers is already at its final ones
// and is installed as it is. An installed record whose own number or any
// reference changes is walked; references can move to existing entries or
// when records of one recipe coalesce. A row that keeps its value relocates
// only its offers. Requires egraphMu.
func (c *Cache) relocateMergeRowsLocked(commit *valueMergeCommit) error {
	prep := commit.prep
	number := func(ordinal uint64) (sharedResultID, error) {
		row := commit.byOrdinal[ordinal]
		if row == nil {
			return 0, fmt.Errorf("merge values: reference to ordinal %d outside the merged closure", ordinal)
		}
		return row.res.id, nil
	}
	final := func(provisional uint64) (sharedResultID, error) {
		return number(provisional - prep.firstID + 1)
	}
	relocate := func(ref *PersistedRef) error {
		if ref.RecipeID != nil || ref.ResultID == 0 {
			return nil
		}
		n, err := final(ref.ResultID)
		ref.ResultID = uint64(n)
		return err
	}
	for _, row := range commit.rows {
		source := prep.records[row.ordinal]
		if !row.action.installs() {
			if !c.blobBacked {
				offers, err := clonePartOffers(source.Envelope.PendingOffers)
				if err != nil {
					return err
				}
				for i := range offers {
					if err := visitPersistedPartOffer(&offers[i], nil, relocate); err != nil {
						return err
					}
				}
				compactOfferOwners(offers)
				row.offers = offers
			}
			continue
		}
		moved := false
		for _, provisional := range prep.refs[row.ordinal] {
			n, err := final(provisional)
			if err != nil {
				return err
			}
			moved = moved || uint64(n) != provisional
		}
		rec := source
		if moved {
			var err error
			if rec, err = VisitEncodedReferences(source, relocate); err != nil {
				return err
			}
		} else {
			// The record is shared with the preparation: copy what the
			// commit changes.
			rec.Call = source.Call.clone()
			offers, err := clonePartOffers(source.Envelope.PendingOffers)
			if err != nil {
				return err
			}
			rec.Envelope.PendingOffers = offers
		}
		compactOfferOwners(rec.Envelope.PendingOffers)
		seen := map[sharedResultID]bool{}
		for _, dep := range row.value.DependencyIDs {
			n, err := number(dep)
			if err != nil {
				return err
			}
			if !seen[n] {
				seen[n] = true
				row.deps = append(row.deps, n)
			}
		}
		// The preparation validated the record at its ordinals
		// (validateTransferRecord). Relocation maps each ordinal to one
		// nonzero number and applies that map to references and
		// dependencies alike, and it lists each dependency once as it maps
		// them (seen, above), though two records of one recipe map to one
		// entry. So every child or call reference is still a direct
		// dependency, none is zero and none is listed twice: the payload's
		// checks aren't repeated here. The offers, every row's, are checked
		// at the final numbers with the graph (checkMergeGraphLocked).
		row.rec = rec
		if !c.blobBacked {
			row.offers = rec.Envelope.PendingOffers
		}
	}
	return nil
}

// compactOfferOwners lists each offer owner's dependencies once, sorted. Two
// records of one recipe land on one entry, so an owner that lists both names
// that entry once, as offerPart lists the owners of the offers it receives.
func compactOfferOwners(offers []PersistedPartOffer) {
	for i := range offers {
		owner := &offers[i].Owner
		slices.Sort(owner.DependencyIDs)
		owner.DependencyIDs = slices.Compact(owner.DependencyIDs)
	}
}

// checkMergeGraphLocked checks, before anything changes, the ownership graph
// the merge would leave: every offer's owner names entries the merge leaves,
// and no cycle closes through an entry that takes a record or an offer. The
// graph is each entry's dependencies, the installed record's new ones for an
// entry that takes one, and the owners of each entry's offers as the commit
// leaves them (selectMergedOffersLocked): an offer admission discards, or one
// an admitted offer replaces, adds no edge. Each bundle's own graph is
// acyclic, but a kept entry's edges and a new record's can close a cycle when
// two caches' values of the same recipes depend on each other differently
// through explicit dependencies. A bundle that would close one is refused,
// like any failed check. Requires egraphMu.
func (c *Cache) checkMergeGraphLocked(commit *valueMergeCommit) error {
	installed := commit.installed
	// An entry's children are its dependencies, the installed record's for
	// an entry that takes one, and the owners of its offers as the commit
	// leaves them: an entry that takes a record starts with none, and one a
	// row admits offers to has selectMergedOffersLocked's final set.
	children := func(res *sharedResult) []*sharedResult {
		var out []*sharedResult
		if row := installed[res]; row != nil {
			for _, dep := range row.deps {
				out = append(out, c.mergeEntryLocked(commit, dep))
			}
		} else {
			for id := range res.deps {
				out = append(out, c.resultsByID[id])
			}
		}
		if offers, ok := commit.finalOffers[res]; ok {
			for _, offer := range offers {
				for _, dep := range offer.Owner.DependencyIDs {
					out = append(out, c.mergeEntryLocked(commit, sharedResultID(dep)))
				}
			}
		} else if installed[res] == nil {
			for _, offer := range res.partOffersLocked() {
				out = append(out, slices.Collect(offerOwnershipChildrenLocked(offer.owner))...)
			}
		}
		return out
	}
	// Every changed entry is checked for an owner that names a missing
	// entry, and the graph is searched for a cycle from the changed entries
	// only: the cache's graph and the bundle's were each acyclic, so a new
	// cycle runs through one of them.
	var changed []*sharedResult
	for _, row := range commit.rows {
		for _, offer := range row.offers {
			if _, err := partAddressKey(offer.Address); err != nil {
				return err
			}
			if err := validateOfferReferences(offer); err != nil {
				return fmt.Errorf("merge values: row %d: %w", row.ordinal, err)
			}
			for _, dep := range offer.Owner.DependencyIDs {
				if c.mergeEntryLocked(commit, sharedResultID(dep)) == nil {
					return fmt.Errorf("merge values: row %d: offer owner names missing entry %d", row.ordinal, dep)
				}
			}
		}
		if len(row.admitted) > 0 || installed[row.res] == row {
			changed = append(changed, row.res)
		}
	}
	const (
		visiting = 1
		done     = 2
	)
	state := map[*sharedResult]int{}
	var visit func(*sharedResult) *sharedResult
	visit = func(res *sharedResult) *sharedResult {
		switch state[res] {
		case visiting:
			return res
		case done:
			return nil
		}
		state[res] = visiting
		for _, child := range children(res) {
			if child == nil {
				continue
			}
			if cycle := visit(child); cycle != nil {
				return cycle
			}
		}
		state[res] = done
		return nil
	}
	for _, res := range changed {
		if cycle := visit(res); cycle != nil {
			return fmt.Errorf("merge values: the merge would close a dependency cycle through entry %d", cycle.id)
		}
	}
	return nil
}

// installedInDependencyOrder returns the rows that install a record,
// dependencies before dependents over their final dependencies.
func (commit *valueMergeCommit) installedInDependencyOrder() []*mergeRow {
	var order []*mergeRow
	visited := map[*mergeRow]bool{}
	var visit func(*mergeRow)
	visit = func(row *mergeRow) {
		if visited[row] {
			return
		}
		visited[row] = true
		for _, dep := range row.deps {
			if depRow := commit.installed[commit.byNumber[dep]]; depRow != nil {
				visit(depRow)
			}
		}
		order = append(order, row)
	}
	for _, row := range commit.rows {
		if commit.installed[row.res] == row {
			visit(row)
		}
	}
	return order
}

// mergeEntryLocked returns the entry a merge leaves under number: one it
// creates, or one the cache has. Requires egraphMu.
func (c *Cache) mergeEntryLocked(commit *valueMergeCommit, number sharedResultID) *sharedResult {
	if res := commit.byNumber[number]; res != nil {
		return res
	}
	return c.resultsByID[number]
}

// commitValueMergeLocked changes the cache as the merge decided, and reads
// the reply. Nothing it does can fail short of an invariant violation: an
// engine entry that meets one is taken back as a failed replacement is, and a
// Cloud entry goes back to no value, keeping its holdings. It returns the
// work that runs after the lock is released. Requires egraphMu for writing.
func (c *Cache) commitValueMergeLocked(ctx context.Context, commit *valueMergeCommit) (MergeReply, func(context.Context) error) {
	c.initEgraphLocked()
	if c.offerOwners == nil {
		c.offerOwners = map[offerOwnerID]*offerOwner{}
	}
	notify, notifyOwner := c.beginShareNotificationsLocked()
	var (
		queue    collectionQueue
		errs     error
		replaced []mergeReplacement
		change   RemoteChange
		created  collectionQueue
	)

	// The values.
	for _, row := range commit.rows {
		switch row.action {
		case mergeKeep:
			continue
		case mergeRetire:
			c.retireResultLocked(row.target)
			fallthrough
		case mergeCreate:
			c.installMergedRecordLocked(row.res, row)
			c.resultsByID[row.res.id] = row.res
			row.res.onRelease = c.resultSnapshotLeaseCleanup(row.res)
			c.indexRecipeLocked(row.plan.recipe, row.res)
			created = append(created, row.res)
		case mergeStore:
			c.installMergedRecordLocked(row.res, row)
			if row.res.onRelease == nil {
				row.res.onRelease = c.resultSnapshotLeaseCleanup(row.res)
			}
		case mergeReplace:
			fresh := &sharedResult{}
			c.installMergedRecordLocked(fresh, row)
			old, more, err := c.replaceResultValueInPlaceLocked(ctx, row.res, fresh, row.value.ExpiresAtUnix)
			queue = append(queue, more...)
			errs = errors.Join(errs, err)
			row.res.expiresAtUnix = row.value.ExpiresAtUnix
			// The old value's release runs after the lock; until it has,
			// readers wait on the entry's attachment.
			row.res.attachDepsMu.Lock()
			row.res.attachDepsWaitCh = make(chan struct{})
			row.res.attachDepsErr = nil
			row.res.attachDepsMu.Unlock()
			replaced = append(replaced, mergeReplacement{res: row.res, old: old})
		}
	}
	for _, row := range commit.rows {
		if !row.action.installs() {
			continue
		}
		res := row.res
		if res.deps == nil {
			res.deps = make(map[sharedResultID]struct{}, len(row.deps))
		}
		for _, depID := range row.deps {
			dep := c.resultsByID[depID]
			if dep == nil {
				errs = errors.Join(errs, fmt.Errorf("merge values: missing dependency %d of %d", depID, res.id))
				continue
			}
			res.deps[depID] = struct{}{}
			c.rememberDependencyEdgeLocked(res, dep)
			c.incrementIncomingOwnershipLocked(ctx, dep)
		}
		res.dependencyOwnershipRevision++
		if err := walkTransferCalls(res.loadResultCall(), func(*ResultCall) error { return nil }, func(ref *ResultCallRef) error {
			ref.shared = c.resultsByID[sharedResultID(ref.ResultID)]
			return nil
		}); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	// Requirements are recomputed one level deep, so dependencies first: in
	// the final graph's order, which a recipe's best record can change from
	// the bundle's. checkMergeGraphLocked refused any cycle.
	for _, row := range commit.installedInDependencyOrder() {
		if _, err := c.recomputeRequiredSessionResourcesLocked(row.res); err != nil {
			errs = errors.Join(errs, err)
		}
	}

	// Identity, and the sender's holding. On the Cloud, the entries the
	// identity joins to another class are affected too.
	var cloudState *remoteCacheState
	var joined func(*RemoteChange)
	if c.blobBacked {
		cloudState = c.remoteCacheLocked(commit.from)
		joined = c.trackJoinsLocked()
	}
	for _, row := range commit.rows {
		frame := row.plan.row.loadResultCall()
		if row.action.installs() {
			frame = row.res.loadResultCall()
		}
		c.applyPreparedResultIdentityLocked(ctx, row.res, frame, row.plan.recipe, row.plan.self, row.plan.inputs, row.plan.provenance, row.plan.recipe)
		if !c.blobBacked {
			// The Cloud entry that sent the record stores it, with the
			// record's expiry.
			row.res.noteCloudCopyLocked(row.value.SenderNumber, true, row.value.ExpiresAtUnix)
			continue
		}
		c.mergeSenderHoldingLocked(ctx, cloudState, commit, row, &change)
	}
	if joined != nil {
		joined(&change)
	}

	// Retention: an engine's retention edge, or the Cloud's stored root.
	for i, root := range commit.prep.bundle.Roots {
		if commit.roots[i].Expired {
			continue
		}
		row := commit.byOrdinal[uint64(root.Ordinal)]
		c.upsertPersistedEdgeLocked(ctx, row.res, root.ExpiresAtUnix, false)
	}

	// Parts: an engine admits the incoming offers by what each target kept.
	released, err := c.applyMergedOffersLocked(ctx, commit)
	queue = append(queue, released...)
	errs = errors.Join(errs, err)
	for _, r := range replaced {
		more, err := c.releaseReplacedDependenciesLocked(ctx, r.old, nil)
		queue = append(queue, more...)
		errs = errors.Join(errs, err)
	}
	if errs != nil {
		// Only an invariant violation gets here.
		for _, row := range commit.rows {
			if row.action.installs() {
				queue = c.failMergedEntryLocked(ctx, row.res, queue)
			}
		}
	}
	// An entry the merge created that nothing owns, such as a dependency
	// only a kept record's bundle needed, is collected now, before a sharing
	// pass could hold it; the reply leaves it out. Roots are retained, and an
	// installed record owns its dependencies.
	releases, collectErr := c.collectUnownedResultsLocked(context.WithoutCancel(ctx), created)
	errs = errors.Join(errs, collectErr)
	for _, row := range commit.rows {
		c.queueSnapshotShareRowLocked(ctx, row.res)
	}
	c.flushShareNotificationsLocked(ctx, notify, notifyOwner)
	duplicates := c.takeShareDuplicateHoldsLocked()
	more, collectErr := c.collectUnownedResultsLocked(context.WithoutCancel(ctx), queue)
	releases = append(releases, more...)
	errs = errors.Join(errs, collectErr)

	reply := c.mergeReplyLocked(commit)
	reply.Change = change
	finish := func(ctx context.Context) error {
		cleanupCtx := context.WithoutCancel(ctx)
		c.releaseShareDuplicateHolds(cleanupCtx, duplicates)
		err := errors.Join(errs, runOnReleaseFuncs(cleanupCtx, releases))
		for _, r := range replaced {
			err = errors.Join(err, c.finishMergedReplacement(cleanupCtx, r))
		}
		return err
	}
	return reply, finish
}

// mergeReplacement is a replacement in place that a merge made, for the work
// after the lock: releasing the old value.
type mergeReplacement struct {
	res *sharedResult
	old replacedValue
}

// installMergedRecordLocked gives res the row's relocated record as its
// value: stored and not decoded. res is a new entry, an entry with no value,
// or the fresh value of a replacement. Requires egraphMu for writing.
func (c *Cache) installMergedRecordLocked(res *sharedResult, row *mergeRow) {
	rec := row.rec
	res.imported = true
	res.expiresAtUnix = row.value.ExpiresAtUnix
	res.sessionResourceHandle = rec.Envelope.SessionResourceHandle
	res.description = row.plan.row.description
	res.recordType = row.plan.row.recordType
	res.storeResultCall(rec.Call)
	res.payloadMu.Lock()
	res.isObject = rec.Envelope.Kind == persistedResultKindObject
	res.hasValue = false
	res.self = nil
	res.persistedEnvelope = &rec.Envelope
	res.payloadRevision++
	if res.createdAtUnixNano == 0 {
		res.createdAtUnixNano = time.Now().UnixNano()
	}
	res.payloadMu.Unlock()
	res.transferRevision++
}

// mergeSenderHoldingLocked records, on the Cloud, the sending engine's holding
// of the row's entry: created if absent, with its dependencies from the
// record's, named by the sender's numbers, at the record's replacement count.
// An export reports no parts and no expiry; the sender's rows do. Requires
// egraphMu for writing.
func (c *Cache) mergeSenderHoldingLocked(ctx context.Context, state *remoteCacheState, commit *valueMergeCommit, row *mergeRow, change *RemoteChange) {
	key := HolderKey{Cache: commit.from, Number: row.value.SenderNumber}
	entry, h := c.holdingLocked(key)
	created := false
	if h == nil {
		entry, h = c.newHoldingLocked(ctx, state, key, RemoteHolding{Recipe: row.plan.recipe, Field: row.res.description})
		change.Candidates = append(change.Candidates, key)
		created = true
	}
	deps := make([]uint64, 0, len(row.value.DependencyIDs))
	for _, dep := range row.value.DependencyIDs {
		deps = append(deps, commit.prep.index[dep].SenderNumber)
	}
	candidates, changed := c.applyHeldValueStateLocked(ctx, state, key, entry, h, HeldValueState{Replacements: row.value.SenderReplacements, Deps: deps}, nil, nil)
	change.Candidates = append(change.Candidates, candidates...)
	if created || changed || row.action != mergeKeep {
		change.addRecipeOf(entry)
	}
}

// selectMergedOffersLocked decides, before anything changes, which offers
// each entry is left with, by section 5.1 point 3, against the offers and
// complete parts each entry has as the commit leaves it: a record the entry
// now stores keeps its parts; an unexpired record gives its parts to
// whichever value the entry keeps; an expired record gives an expired kept
// value only the parts it lacks, and an unexpired kept value none. A new or
// replaced entry starts with no offer, and a kept one with its own. A part
// complete there takes no offer, and an offer that means the same as the one
// already there changes nothing. Selection takes the rows in commit order, so
// a later row's admitted offer supersedes an earlier one for the same part.
// applyMergedOffersLocked then attaches the final set, in two steps.
// Requires egraphMu.
func (c *Cache) selectMergedOffersLocked(commit *valueMergeCommit) error {
	held := map[*sharedResult]map[string]PersistedPartOffer{}
	commit.finalOffers = held
	commit.newOffers = map[*sharedResult]map[string]bool{}
	heldOffers := func(res *sharedResult) map[string]PersistedPartOffer {
		if offers := held[res]; offers != nil {
			return offers
		}
		offers := map[string]PersistedPartOffer{}
		if commit.installed[res] == nil {
			for key, offer := range res.partOffersLocked() {
				offers[key] = offer.record
			}
		}
		held[res] = offers
		return offers
	}
	for _, row := range commit.rows {
		row.admitted = nil
		if len(row.offers) == 0 || !row.action.installs() && row.expired && !row.keptExpired {
			continue
		}
		complete := map[string]bool{}
		for _, key := range c.mergedCompletePartKeysLocked(commit, row.res) {
			complete[key] = true
		}
		offers := heldOffers(row.res)
		for _, offer := range row.offers {
			key, err := partAddressKey(offer.Address)
			if err != nil {
				return err
			}
			if complete[key] {
				continue
			}
			if existing, ok := offers[key]; ok && (row.expired || sameOfferMeaning(existing, offer)) {
				continue
			}
			offers[key] = offer
			row.admitted = append(row.admitted, offer)
			if commit.newOffers[row.res] == nil {
				commit.newOffers[row.res] = map[string]bool{}
			}
			commit.newOffers[row.res][key] = true
		}
	}
	return nil
}

// applyMergedOffersLocked applies the offers selectMergedOffersLocked chose,
// after the dependency edges are in place, in two steps. It retires every
// offer an entry holds that its final set replaces or drops, then attaches
// each final offer the entry doesn't already hold: one per part key, however
// many rows admitted one there. Removing an offer can't close a cycle, and
// while it attaches, every graph is part of the final one checkMergeGraphLocked
// accepted, so no attachment's own check can refuse it. What the retired
// offers' owners release is collected with the rest of the commit, at its
// end, after the final offers hold their owners. Requires egraphMu for
// writing.
func (c *Cache) applyMergedOffersLocked(ctx context.Context, commit *valueMergeCommit) (collectionQueue, error) {
	var (
		entries []*sharedResult
		queue   collectionQueue
		errs    error
	)
	seen := map[*sharedResult]bool{}
	for _, row := range commit.rows {
		if !seen[row.res] && len(commit.newOffers[row.res]) > 0 {
			seen[row.res] = true
			entries = append(entries, row.res)
		}
	}
	for _, res := range entries {
		final, fresh := commit.finalOffers[res], commit.newOffers[res]
		var retired []PersistedPartAddress
		for key, offer := range res.partOffersLocked() {
			if _, kept := final[key]; !kept || fresh[key] {
				retired = append(retired, offer.record.Address)
			}
		}
		for _, address := range retired {
			more, err := c.retirePartOfferLocked(ctx, res, address)
			queue = append(queue, more...)
			errs = errors.Join(errs, err)
		}
	}
	for _, res := range entries {
		final := commit.finalOffers[res]
		keys := slices.Sorted(maps.Keys(commit.newOffers[res]))
		for _, key := range keys {
			offer := final[key]
			owner, err := c.newOfferOwnerLocked(ctx, offer.Owner)
			if err != nil {
				errs = errors.Join(errs, err)
				continue
			}
			offer.Owner = owner.record
			if err := c.attachPartOfferLocked(res, offer.Address, &partOffer{record: offer, owner: owner}); err != nil {
				more, releaseErr := c.releaseOfferOwnerLocked(ctx, owner)
				queue = append(queue, more...)
				errs = errors.Join(errs, err, releaseErr)
			}
		}
	}
	return queue, errs
}

// mergedCompletePartKeysLocked returns the complete parts of res as the merge
// leaves it. For an entry that takes a record, they are what the record
// proves, the part probe's LocalComplete over it, as the entry's own spans
// report them, read outside the lock (valueMergePrep.completeParts). For an
// entry that keeps its value, they are completePartKeysLocked's. Requires
// egraphMu.
func (c *Cache) mergedCompletePartKeysLocked(commit *valueMergeCommit, res *sharedResult) []string {
	row := commit.installed[res]
	if row == nil {
		return completePartKeysLocked(res)
	}
	return commit.prep.completeParts[row.ordinal]
}

// recordCompletePartKeys returns the part keys record proves complete: the
// part probe's LocalComplete over it, sorted, or none if the record doesn't
// describe its parts.
func recordCompletePartKeys(record PersistedRecord) []string {
	probes, err := describePartRecord(record)
	if err != nil {
		return nil
	}
	var keys []string
	for _, probe := range probes {
		if !probe.LocalComplete {
			continue
		}
		if key, err := partAddressKey(probe.Descriptor.Address); err == nil {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

// completePartKeysLocked returns res's complete parts as far as they are known
// under the lock: those its value's last captured record reports, while that
// capture still describes the value, and those its part gate has settled.
func completePartKeysLocked(res *sharedResult) []string {
	keys := res.settledPartKeys()
	if cached := res.completeParts.Load(); cached != nil && cached.version.check(res) == nil {
		keys = append(keys, cached.keys...)
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

// failMergedEntryLocked takes back an entry whose merged value met an
// invariant violation. An engine entry leaves the recipe index and loses its
// retention edge, as a failed replacement does, and is collected once nothing
// owns it: an engine entry with no value would read as a cached nil result.
// A Cloud entry goes back to no value and keeps its holdings. Requires
// egraphMu for writing.
func (c *Cache) failMergedEntryLocked(ctx context.Context, res *sharedResult, queue collectionQueue) collectionQueue {
	if c.blobBacked {
		res.payloadMu.Lock()
		res.persistedEnvelope = nil
		res.hasValue = false
		res.payloadRevision++
		res.payloadMu.Unlock()
		return queue
	}
	c.unindexRecipesLocked(res)
	if _, retained := c.persistedEdgesByResult[res.id]; retained {
		delete(c.persistedEdgesByResult, res.id)
		queue, _ = c.decrementIncomingOwnershipLocked(ctx, res, queue)
	}
	return append(queue, res)
}

// finishMergedReplacement releases the value a merge replaced, resets the
// per-value state the stored record starts without, and then lets readers
// in. If the release fails, the entry is taken back as a failed replacement
// is.
func (c *Cache) finishMergedReplacement(ctx context.Context, r mergeReplacement) error {
	err := c.finishValueReplacement(ctx, r.res, r.old.release)
	if err != nil {
		c.egraphMu.Lock()
		queue := c.failMergedEntryLocked(ctx, r.res, nil)
		releases, collectErr := c.collectUnownedResultsLocked(ctx, queue)
		c.egraphMu.Unlock()
		err = errors.Join(err, collectErr, runOnReleaseFuncs(ctx, releases))
	}
	r.res.attachDepsMu.Lock()
	if r.res.attachDepsWaitCh != nil {
		r.res.attachDepsErr = err
		close(r.res.attachDepsWaitCh)
	}
	r.res.attachDepsMu.Unlock()
	return err
}

// mergeReplyLocked reads every target as the commit left it. Requires
// egraphMu.
func (c *Cache) mergeReplyLocked(commit *valueMergeCommit) MergeReply {
	reply := MergeReply{Generation: c.identity.Generation, EngineTimeUnixNano: time.Now().UnixNano()}
	for _, row := range commit.rows {
		res := row.res
		if c.resultsByID[res.id] != res {
			// The commit's collection removed an entry the merge created
			// that nothing owns: there is no target to report.
			continue
		}
		value := MergedValue{Ordinal: row.value.Ordinal, Number: uint64(res.id), Replacements: res.replacements, ExpiresAtUnix: res.expiresAtUnix}
		for dep := range res.deps {
			value.Deps = append(value.Deps, uint64(dep))
		}
		slices.Sort(value.Deps)
		complete := map[string]bool{}
		for _, key := range c.mergedCompletePartKeysLocked(commit, res) {
			complete[key] = true
			value.Parts = appendPartAddress(value.Parts, key)
		}
		for key := range res.partOffersLocked() {
			if !complete[key] {
				value.OfferedParts = appendPartAddress(value.OfferedParts, key)
			}
		}
		slices.SortFunc(value.OfferedParts, comparePartAddresses)
		reply.Values = append(reply.Values, value)
	}
	for _, root := range commit.roots {
		if !root.Expired {
			res := commit.byOrdinal[uint64(root.Ordinal)].res
			root.Number = uint64(res.id)
			if edge, ok := c.persistedEdgesByResult[res.id]; ok {
				root.Retained, root.RetentionExpiresAtUnix = true, edge.expiresAtUnix
			}
		}
		reply.Roots = append(reply.Roots, root)
	}
	return reply
}

func appendPartAddress(addresses []PersistedPartAddress, key string) []PersistedPartAddress {
	var address PersistedPartAddress
	if err := json.Unmarshal([]byte(key), &address); err != nil {
		return addresses
	}
	return append(addresses, address)
}

func comparePartAddresses(a, b PersistedPartAddress) int {
	ka, _ := partAddressKey(a)
	kb, _ := partAddressKey(b)
	switch {
	case ka < kb:
		return -1
	case ka > kb:
		return 1
	}
	return 0
}
