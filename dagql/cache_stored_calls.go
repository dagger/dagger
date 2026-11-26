package dagql

import (
	"context"
	"fmt"
)

// StoredCallsResult is StoredCalls' answer: of the roots it was asked
// about, each list in the roots' order.
type StoredCallsResult struct {
	// Sent are the held roots a stored bundle sends a kept call on, the
	// root as the call's value.
	Sent []uint64
	// Partial are the held roots a stored bundle counts partial
	// (StoredBundleResult.Partial) because a term kept on them that the
	// recipient may be sent has no call it can place.
	Partial []uint64
	// NotHeld are the roots the recipient doesn't hold: StoredCalls doesn't
	// answer for them.
	NotHeld []uint64
}

// StoredCalls answers what StoredBundle(roots, nowUnix, held) decides of the
// calls kept on each root the recipient holds, without building the
// bundle: whether it sends one (Sent), and whether it counts the root
// partial for a term it may send none of whose calls can be placed
// (Partial). A caller may rely on that: for a held root, each answer is
// StoredBundle's at the moment StoredCalls reads the root. It doesn't say
// which call of a term a bundle sends, nor with which inputs: those depend
// on what the rest of the bundle carries.
//
// A bundle carries a held root as a held row, and decides the calls kept on
// it as it decides any entry's (storedBundleCallsLocked): a term the
// recipient's check made (HeldBy.Calls, Terms) is sent if one of its calls
// can be placed, and makes the root partial if none can. Whether a call can
// be placed depends only on whether each of its inputs has an entry that
// places it, its own or one of its class, held or stored
// (inputPlaceLocked). What else the bundle carries changes which entry
// places an input, never whether one does, so StoredCalls decides each root
// alone, with what the recipient holds.
//
// A root the cache has no entry for, or one it knows only through holdings,
// is in no list: a bundle carries nothing of it. A root the recipient
// doesn't hold is NotHeld: a bundle carries it in full and walks below it,
// and what it decides of it then depends on the rest of the bundle, as a
// part of its class it can't place for a service the bundle has no row of.
// StoredCalls doesn't answer for it: a caller that needs that answer builds
// the bundle.
//
// It refuses a recipient with Parts, Through or Whole, whose bundles decide
// a root's calls from what the rest of the bundle carries. Only a
// blob-backed cache answers. It reads under one hold of egraphMu for
// reading, which it lets go between two roots when another acquisition
// waits (giveWayLocked), so a writer waits for one root's answer, not the
// read.
func (c *Cache) StoredCalls(ctx context.Context, roots []uint64, nowUnix int64, held *HeldBy) (StoredCallsResult, error) {
	var out StoredCallsResult
	if !c.blobBacked {
		return out, fmt.Errorf("stored calls: the cache has no blob store")
	}
	if held != nil && (held.Parts != nil || held.Through != nil || held.Whole) {
		return out, fmt.Errorf("stored calls: a recipient with parts, entries gone through or whole holds is answered by a bundle's build alone")
	}
	if err := context.Cause(ctx); err != nil {
		return out, err
	}
	read := c.egraphMu.RLockAs(c.graphReader(ctx, workExport))
	defer read.Unlock()
	// The view of the recipient's terms keys them over the classes of one
	// hold (namedTerms): a union between two holds moves a class's root, so
	// each hold takes a view of its own.
	terms := newNamedTerms(held, nil)
	for i, number := range roots {
		c.storedCallsAnswerLocked(&out, held, terms, number, nowUnix)
		if c.testBatchAnswered != nil {
			c.testBatchAnswered(i)
		}
		if i+1 < len(roots) && c.egraphMu.Waiting() {
			c.giveWayLocked(&read)
			terms = newNamedTerms(held, nil)
		}
	}
	return out, nil
}

// storedCallsAnswerLocked adds StoredCalls' answer for the root numbered
// number to out. Requires egraphMu.
func (c *Cache) storedCallsAnswerLocked(out *StoredCallsResult, held *HeldBy, terms *namedTerms, number uint64, nowUnix int64) {
	res := c.resultsByID[sharedResultID(number)]
	if res == nil || res.noValueLocked() {
		return
	}
	if !held.heldLocked(res, nowUnix) {
		out.NotHeld = append(out.NotHeld, number)
		return
	}
	sent, partial := c.storedCallsLocked(held, terms, res, nowUnix)
	if sent {
		out.Sent = append(out.Sent, number)
	}
	if partial {
		out.Partial = append(out.Partial, number)
	}
}

// storedCallsLocked is StoredCalls' answer for res, a root the recipient
// held holds: whether a bundle sends a call kept on it, and whether it
// counts it partial, as storedBundleCallsLocked decides them. It places an
// input on an entry the recipient holds or the Cloud stores. A bundle also
// places one on an entry it carries, but each of those the recipient holds
// or the Cloud stores, so whether an input can be placed is the same.
// Requires egraphMu.
func (c *Cache) storedCallsLocked(held *HeldBy, terms *namedTerms, res *sharedResult, nowUnix int64) (sent, partial bool) {
	if len(res.keptCallsLocked()) == 0 {
		return false, false
	}
	has := func(entry *sharedResult) bool { return held.heldLocked(entry, nowUnix) }
	for _, group := range c.keptCallTermsLocked(res) {
		if group = c.sendableCallsLocked(held, terms, group); len(group) == 0 {
			continue
		}
		if chosen, _ := c.placeableCallLocked(group, nowUnix, has); chosen == nil {
			partial = true
		} else {
			sent = true
		}
	}
	return sent, partial
}
