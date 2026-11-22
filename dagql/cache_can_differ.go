package dagql

import (
	"sync"

	"github.com/dagger/dagger/engine/slog"
)

// entryCanDifferLocked reports whether another run of res's step could give
// another result: whether its call doesn't declare that its result follows
// from its inputs (ResultCall.FollowsFromInputs). The declaration is the
// engine's, per call, and the Cloud keeps it in two places: in the frame of
// the record it stores, and on each holding a call row reported, as the row
// stamps it. An entry with neither
// can differ: its record or its rows came from before the declaration. So
// does one whose record and holdings, or two of whose holdings, disagree,
// which the same call on the same build never does; the first disagreement
// for an entry is logged. Every reader that asks whether a step can differ
// asks here. Requires egraphMu.
func (c *Cache) entryCanDifferLocked(res *sharedResult) bool {
	differ, _ := c.entryCanDifferWhyLocked(res)
	return differ
}

// entryCanDifferWhyLocked is entryCanDifferLocked, with why it can differ:
// "undeclared", its record or call rows declaring nothing; "unknown", neither
// saying; or "disagree". Requires egraphMu.
func (c *Cache) entryCanDifferWhyLocked(res *sharedResult) (bool, string) {
	declared, known, agree := false, false, true
	note := func(follows bool) {
		switch {
		case !known:
			declared, known = follows, true
		case follows != declared:
			agree = false
		}
	}
	if res.stored != nil || res.persistedEnvelope != nil {
		if call := res.loadResultCall(); call != nil {
			note(call.FollowsFromInputs)
		} else if body := res.stored; body != nil && body.frame != nil {
			note(body.follows)
		}
	}
	for _, ref := range res.holderRefsLocked() {
		if follows, known := declares(ref.h.follows, ref.h.rowed); known {
			note(follows)
		}
	}
	if !agree {
		if _, logged := declarationDisagreements.LoadOrStore(declarationDisagreement{c, res.id}, struct{}{}); !logged {
			slog.Warn("remote cache entry's record and holdings disagree on whether it follows from its inputs; it can differ", "entry", res.id, "field", entryFieldLocked(res))
		}
		return true, "disagree"
	}
	switch {
	case !known:
		return true, "unknown"
	case !declared:
		return true, "undeclared"
	}
	return false, ""
}

// declarationDisagreement names an entry whose record and holdings disagreed
// on its declaration (entryCanDifferLocked), which is logged once.
type declarationDisagreement struct {
	cache *Cache
	id    sharedResultID
}

var declarationDisagreements sync.Map

// entryFieldLocked returns the field of res's call, as its description or
// its frame names it, or "" if neither does. Requires egraphMu.
func entryFieldLocked(res *sharedResult) string {
	switch {
	case res == nil:
		return ""
	case res.description != "":
		return res.description
	}
	if call := res.loadResultCall(); call != nil {
		return call.Field
	}
	if body := res.stored; body != nil && body.frame != nil {
		if frame, err := body.decodeFrame(); err == nil && frame != nil {
			return frame.Field
		}
	}
	return ""
}

// declares is a holding's word on the declaration: what its call row
// stamped, follows, and whether a call row's description set it, known. A
// holding no call row described, such as a merge's or a handover's, says
// nothing.
func declares(follows, callRow bool) (bool, bool) {
	return follows, callRow
}

// Declares is the holding's word on the declaration (declares).
func (h RemoteHolding) Declares() (follows, known bool) {
	return declares(h.FollowsFromInputs, h.CallRow)
}

// CanDiffer reports whether, by this holding alone, its entry's step can
// differ: unless its call row declared that it follows from its inputs. The
// entry's own answer, with its record's frame, is entryCanDifferLocked's.
func (h RemoteHolding) CanDiffer() bool {
	follows, known := h.Declares()
	return !known || !follows
}

// Declares is the holding's word on the declaration (declares).
func (h RemoteHoldingInfo) Declares() (follows, known bool) {
	return declares(h.FollowsFromInputs, h.CallRow)
}

// CanDiffer is RemoteHolding.CanDiffer, of the holding's facts.
func (h RemoteHoldingInfo) CanDiffer() bool {
	follows, known := h.Declares()
	return !known || !follows
}

// Declares is the holding's word on the declaration (declares).
func (h HoldingFacts) Declares() (follows, known bool) {
	return declares(h.FollowsFromInputs, h.CallRow)
}

// CanDiffer is RemoteHolding.CanDiffer, of the holding's facts.
func (h HoldingFacts) CanDiffer() bool {
	follows, known := h.Declares()
	return !known || !follows
}
