package dagql

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Dehydration trades a registered object result's decoded value for the
// persisted envelope boot restore produces, keeping everything else about the
// entry: its number (the handle clients hold), call frame, e-graph identity,
// session ownership and dependency edges, and snapshot owner leases. The next
// reference decodes the envelope again through ensurePersistedHitValueLoaded,
// as for a row restored at boot; the resolver does not run again.

// DehydrateOptions selects what DehydrateColdResults dehydrates.
type DehydrateOptions struct {
	// UnusedFor dehydrates only results not used for at least this long.
	UnusedFor time.Duration
	// Now is the reference time for UnusedFor; zero means time.Now.
	Now time.Time
}

// DehydrateReport counts what a DehydrateColdResults pass found and did.
type DehydrateReport struct {
	// Results is the number of registered results the pass looked at.
	Results int `json:"results"`
	// AlreadyDehydrated have only an envelope (restored or dehydrated).
	AlreadyDehydrated int `json:"alreadyDehydrated"`
	// Dehydrated is the number of results this pass dehydrated.
	Dehydrated int `json:"dehydrated"`
	// EnvelopeBytes is the size of the envelopes the dehydrated results keep.
	EnvelopeBytes int64 `json:"envelopeBytes"`
	// DehydratedByType counts the dehydrated results by type name.
	DehydratedByType map[string]int `json:"dehydratedByType"`
	// Skipped counts the results left as they were, by reason.
	Skipped map[string]int `json:"skipped"`
	// SkippedByType counts the results left as they were, by type name and
	// reason.
	SkippedByType map[string]int `json:"skippedByType"`
	// Samples holds one example's details for some skip reasons.
	Samples map[string]string `json:"samples,omitempty"`
}

func (r *DehydrateReport) skip(typeName, reason string) {
	r.Skipped[reason]++
	r.SkippedByType[typeName+": "+reason]++
}

// Reasons a result is not dehydrated.
const (
	dehydrateSkipNoValue           = "no value"
	dehydrateSkipNotObject         = "not an object"
	dehydrateSkipNoCodec           = "no persisted codec"
	dehydrateSkipRecentlyUsed      = "recently used"
	dehydrateSkipInlineBorrow      = "inline borrow"
	dehydrateSkipPartManaged       = "part managed"
	dehydrateSkipPersistDeclined   = "persistence declined"
	dehydrateSkipAttachment        = "dependency attachment not clean"
	dehydrateSkipLazy              = "lazy evaluation pending"
	dehydrateSkipDecoding          = "decode or lease sync in flight"
	dehydrateSkipLinkIntent        = "snapshot link intent pending"
	dehydrateSkipLinksDiffer       = "snapshot links not synced"
	dehydrateSkipDependentHydrated = "a dependent is hydrated"
	dehydrateSkipChanged           = "changed while encoding"
	dehydrateSkipUnregistered      = "not registered"
	dehydrateSkipEncode            = "encode failed"
)

// DehydrateColdResults dehydrates every eligible registered result. A result
// is only dehydrated after every result depending on it (depParents), since a
// hydrated dependent can hold its value through a field: results are visited
// newest first, and the pass repeats while it makes progress.
//
// It is a measurement tool, for the debug endpoint, and not safe beside live
// calls: a serve path that has read a hydrated payload state re-reads the
// value later (ensurePersistedHitValueLoaded's fast path), and a running
// resolver dereferences its receiver's value whenever it likes, so either can
// find the value gone. Run it while the sessions are idle.
func (c *Cache) DehydrateColdResults(ctx context.Context, opts DehydrateOptions) (DehydrateReport, error) {
	report := DehydrateReport{
		DehydratedByType: map[string]int{},
		Skipped:          map[string]int{},
		SkippedByType:    map[string]int{},
		Samples:          map[string]string{},
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	c.egraphMu.RLock()
	pending := make([]*sharedResult, 0, len(c.resultsByID))
	for _, res := range c.resultsByID {
		pending = append(pending, res)
	}
	c.egraphMu.RUnlock()
	slices.SortFunc(pending, func(a, b *sharedResult) int {
		switch {
		case a.id > b.id:
			return -1
		case a.id < b.id:
			return 1
		}
		return 0
	})
	report.Results = len(pending)

	blocked := map[*sharedResult]string{}
	for {
		if err := context.Cause(ctx); err != nil {
			return report, err
		}
		progress := false
		next := pending[:0]
		for _, res := range pending {
			typeName, envBytes, reason := c.dehydrateResult(ctx, res, now, opts.UnusedFor, report.Samples)
			switch reason {
			case "":
				progress = true
				report.Dehydrated++
				report.EnvelopeBytes += envBytes
				report.DehydratedByType[typeName]++
				delete(blocked, res)
			case dehydrateSkipDependentHydrated:
				blocked[res] = typeName
				next = append(next, res)
			case dehydrateSkipNoValue:
				if res.loadPayloadState().persistedEnvelope != nil {
					report.AlreadyDehydrated++
				} else {
					report.skip(typeName, reason)
				}
			default:
				report.skip(typeName, reason)
			}
		}
		pending = next
		if !progress || len(pending) == 0 {
			break
		}
	}
	for _, typeName := range blocked {
		report.skip(typeName, dehydrateSkipDependentHydrated)
	}
	return report, nil
}

// dehydrateResult dehydrates res if it is eligible, returning its type name,
// the size of the envelope it keeps, and the reason it was left as it was,
// if it was.
//
//nolint:gocyclo // one eligibility check after another
func (c *Cache) dehydrateResult(ctx context.Context, res *sharedResult, now time.Time, unusedFor time.Duration, samples map[string]string) (string, int64, string) {
	state := res.loadPayloadState()
	typeName := "?"
	if state.self != nil {
		if typ := state.self.Type(); typ != nil {
			typeName = typ.Name()
		}
	} else if state.persistedEnvelope != nil && state.persistedEnvelope.TypeName != "" {
		typeName = state.persistedEnvelope.TypeName
	}
	if !state.hasValue || state.self == nil || state.persistedEnvelope != nil {
		return typeName, 0, dehydrateSkipNoValue
	}
	if !state.isObject {
		return typeName, 0, dehydrateSkipNotObject
	}
	if _, ok := state.self.(PersistedObject); !ok {
		return typeName, 0, dehydrateSkipNoCodec
	}
	if _, ok := PersistedObjectFamilyFor(state.self); !ok {
		return typeName, 0, dehydrateSkipNoCodec
	}
	if unusedFor > 0 && now.Sub(time.Unix(0, state.lastUsedAtUnixNano)) < unusedFor {
		return typeName, 0, dehydrateSkipRecentlyUsed
	}
	if res.inlineBorrow != nil {
		return typeName, 0, dehydrateSkipInlineBorrow
	}
	if res.partGate.active.Load() {
		return typeName, 0, dehydrateSkipPartManaged
	}
	if state.snapshotLinkIntent != nil {
		return typeName, 0, dehydrateSkipLinkIntent
	}
	if res.attachmentState() != resultAttachmentClean {
		return typeName, 0, dehydrateSkipAttachment
	}
	res.persistDecodeMu.Lock()
	decoding := res.persistDecodeWaitCh != nil || res.persistLeaseSyncPending
	res.persistDecodeMu.Unlock()
	if decoding {
		return typeName, 0, dehydrateSkipDecoding
	}
	if HasPendingLazyEvaluation(Result[Typed]{shared: res}) {
		return typeName, 0, dehydrateSkipLazy
	}
	c.egraphMu.RLock()
	reason := c.dehydrateBlockedLocked(res)
	c.egraphMu.RUnlock()
	if reason != "" {
		return typeName, 0, reason
	}

	frame := res.loadResultCall()
	encodeCtx := ctx
	if frame != nil {
		encodeCtx = ContextWithCall(ctx, frame)
	}
	encoding, err := encodePersistedResult(encodeCtx, c, Result[Typed]{shared: res}, nil)
	if err != nil {
		return typeName, 0, dehydrateSkipEncode + ": " + firstLine(err.Error())
	}
	if !sameSnapshotLinks(encoding.SnapshotLinks, state.snapshotOwnerLinks) {
		if samples != nil {
			samples[dehydrateSkipLinksDiffer+": "+typeName] = fmt.Sprintf("result %d: encoded %+v, stored %+v", res.id, encoding.SnapshotLinks, state.snapshotOwnerLinks)
		}
		return typeName, 0, dehydrateSkipLinksDiffer
	}
	env := encoding.Envelope
	envBytes := persistedEnvelopePayloadBytes(&env)

	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	if reason := c.dehydrateBlockedLocked(res); reason != "" {
		return typeName, 0, reason
	}
	res.payloadMu.Lock()
	if res.payloadRevision != state.payloadRevision || !res.hasValue || res.persistedEnvelope != nil || res.snapshotLinkIntent != nil {
		res.payloadMu.Unlock()
		return typeName, 0, dehydrateSkipChanged
	}
	res.persistedEnvelope = &env
	res.self = nil
	res.hasValue = false
	res.payloadRevision++
	res.dehydrated.Store(true)
	// FIXME(spike): the entry's release drops the value's own release (the
	// value's OnRelease, or a decode's cleanup) with the value, keeping only
	// its snapshot owner leases' cleanup: refs the value held open leak.
	res.onRelease = c.resultSnapshotLeaseCleanup(res)
	res.payloadMu.Unlock()
	c.setResultPayloadBytesLocked(res, envBytes)
	return typeName, envBytes, ""
}

// dehydrateBlockedLocked is why res, registered or not, may not be
// dehydrated now as far as the e-graph says. Requires egraphMu.
func (c *Cache) dehydrateBlockedLocked(res *sharedResult) string {
	if c.resultsByID[res.id] != res {
		return dehydrateSkipUnregistered
	}
	for parentID := range res.depParents {
		if parent := c.resultsByID[parentID]; parent != nil && dehydrateHoldsResults(parent) {
			return dehydrateSkipDependentHydrated
		}
	}
	return ""
}

// dehydrateHoldsResults reports whether res's decoded value may hold other
// results: it is hydrated, and is not a scalar. A scalar's dependencies are
// only edges (its call's receiver and arguments): its value holds none.
func dehydrateHoldsResults(res *sharedResult) bool {
	state := res.loadPayloadState()
	if !state.hasValue || state.self == nil {
		return false
	}
	if state.isObject {
		return true
	}
	switch state.self.(type) {
	case String, Int, Float, Boolean:
		return false
	}
	if _, ok := state.self.(ScalarValue); ok {
		if _, isList := state.self.(Enumerable); !isList {
			return false
		}
	}
	return true
}

// DebugDropAllValues drops every registered result's in-memory value, its
// release, and its lazy callbacks, without an envelope to load it again. It
// breaks every result it touches and leaks what their releases would free:
// it exists only to measure, on a throwaway engine, the most memory that
// dropping values (as dehydration does) could ever give back.
func (c *Cache) DebugDropAllValues() int {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	n := 0
	for _, res := range c.resultsByID {
		res.payloadMu.Lock()
		if res.hasValue && res.self != nil {
			res.self = nil
			res.hasValue = false
			res.onRelease = nil
			res.payloadRevision++
			n++
		}
		res.payloadMu.Unlock()
		res.lazyMu.Lock()
		res.lazyWhole.eval = nil
		res.lazyWhole.continuation = nil
		res.lazyPartGroups = nil
		res.lazyMu.Unlock()
	}
	return n
}

func sameSnapshotLinks(a, b []PersistedSnapshotRefLink) bool {
	key := func(l PersistedSnapshotRefLink) string {
		return fmt.Sprintf("%s\x00%s\x00%v", l.RefKey, l.Role, l.OutputPath)
	}
	set := map[string]int{}
	for _, l := range a {
		if !l.Borrowed {
			set[key(l)]++
		}
	}
	for _, l := range b {
		if !l.Borrowed {
			set[key(l)]--
		}
	}
	for _, n := range set {
		if n != 0 {
			return false
		}
	}
	return true
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
