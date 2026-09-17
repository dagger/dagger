package dagql

import (
	"errors"
	"fmt"
)

// partRefusal is a refusal in the reselect class that names the site that made
// it. It unwraps to ErrPartReselect, so every retry loop and every errors.Is
// caller treats it exactly as the bare sentinel. The site is what the reselect
// watch's warning reports, so a loop that is not making progress says where it
// is refused; continuation-evidence's reselect audit lists every site by this
// name with its class.
type partRefusal struct {
	site string
	// cause is the error the site answered with a reselect, kept as text: the
	// refusal's class is ErrPartReselect whatever the cause was.
	cause error
	// changed is set only by partChanged. row, expected and current are then
	// the row whose counters the site compared, what this attempt observed,
	// and what the site read when it refused.
	changed           bool
	row               sharedResultID
	expected, current partSourceFacts
}

func (r *partRefusal) Error() string {
	if r.cause != nil {
		return ErrPartReselect.Error() + ": " + r.site + ": " + r.cause.Error()
	}
	return ErrPartReselect.Error() + ": " + r.site
}

func (r *partRefusal) Unwrap() error { return ErrPartReselect }

func partRefused(site string) error { return &partRefusal{site: site} }

func partRefusedBy(site string, cause error) error { return &partRefusal{site: site, cause: cause} }

// partChanged is the refusal of a site that compares counters of row which
// only ever grow: expected is what this attempt observed and current is what
// the site reads now, under its own lock. The fields a site does not compare
// stay zero on both sides. expires is not a counter and is ignored.
//
// When the counters are equal the site refused for a cause no counter covers,
// and the refusal is the ordinary one. Only a refusal whose counters have
// moved is recorded by the progress rule, so a site can never be recorded
// under contention that did not move them. See PartDemandState.refused.
func partChanged(site string, row *sharedResult, expected, current partSourceFacts) error {
	expected.expires, current.expires = 0, 0
	if expected == current {
		return partRefused(site)
	}
	return &partRefusal{site: site, changed: true, row: row.id, expected: expected, current: current}
}

// changed is partChanged for a site that refused because version.check
// failed. Of what that check compares only the payload revision is a counter
// shown to be monotonic: a typed output's revision is one for File and
// Directory, but a Container reports it from whichever of three sources is
// current, and a held core guard fails the same check. Both of those leave the
// payload revision equal, which makes the refusal the ordinary one.
func (version *capturedRowRevision) changed(site string, row *sharedResult) error {
	row.payloadMu.RLock()
	current := row.payloadRevision
	row.payloadMu.RUnlock()
	return partChanged(site, row, partSourceFacts{payload: version.payload.payloadRevision}, partSourceFacts{payload: current})
}

// ErrPartNoProgress is an invariant failure, outside the reselect class: a
// reselect loop was refused twice at one site with the site's counters at the
// same values. See PartNoProgressError.
var ErrPartNoProgress = errors.New("part demand is not making progress")

// PartNoProgressError reports the repeat. Expected and Current are the
// counters of row ResultID that the site compared at the repeated refusal.
type PartNoProgressError struct {
	Loop, Site                string
	ResultID                  uint64
	Address                   PersistedPartAddress
	Expected, Current         PartCounters
	FirstLoop                 string
	FirstIteration, Iteration uint64
}

// PartCounters are the monotonic counters of one row that a changed site may
// compare. A site leaves the ones it does not compare zero.
type PartCounters struct {
	Payload, Gate, Offers, Resources, Ownership uint64
}

func (f partSourceFacts) counters() PartCounters {
	return PartCounters{Payload: f.payload, Gate: f.gate, Offers: f.offers, Resources: f.resources, Ownership: f.ownership}
}

func (e *PartNoProgressError) Error() string {
	return fmt.Sprintf("%s: %s, iteration %d, part %q: %q refused result %d twice at the same counters (expected %+v, current %+v; first in %s, iteration %d)",
		ErrPartNoProgress, e.Loop, e.Iteration, e.Address.Part, e.Site, e.ResultID, e.Expected, e.Current, e.FirstLoop, e.FirstIteration)
}

func (e *PartNoProgressError) Unwrap() error { return ErrPartNoProgress }

type partProgressKey struct {
	site     string
	row      sharedResultID
	counters partSourceFacts
}

type partProgressSeen struct {
	loop      string
	iteration uint64
}

// refused is the progress rule. A reselect loop calls it with the refusal it
// is about to retry. A refusal made by partChanged is recorded by its site,
// row and current counters; the same record a second time returns a
// PartNoProgressError, which the loop returns instead of retrying.
//
// It cannot fire under contention. A recorded refusal read counters that had
// moved past what its attempt observed, and the next attempt observes after
// that refusal, so whatever a later refusal at any site reads is greater still
// in some counter and never less in any. A repeat means the site refused
// although nothing it counts had moved: its expectation is wrong, and retrying
// would spin. Every other refusal, and every caller without demand state, is
// retried as before.
//
// One refusal is one record, because a loop that records a refusal never
// returns it onward into another loop that records into the same demand
// state. publishEvaluatedParts, installChainPart's re-preparation loop and the
// Lazy decision's two scans retry what they record, and return only success,
// an error outside the class or an uncounted refusal of their own. demandPart's
// acquire loop records what reaches it unrecorded: from selection, from
// InstallReadyPart in the obtain Body, and from a source check that failed
// before the decision's scans saw it. A loop that handed a recorded refusal on
// would make the next loop's record of it look like a repeat.
func (s *PartDemandState) refused(loop string, iteration uint64, address PersistedPartAddress, err error) error {
	var refusal *partRefusal
	if s == nil || !errors.As(err, &refusal) || !refusal.changed {
		return nil
	}
	key := partProgressKey{site: refusal.site, row: refusal.row, counters: refusal.current}
	s.mu.Lock()
	defer s.mu.Unlock()
	if first, seen := s.progress[key]; seen {
		return &PartNoProgressError{Loop: loop, Site: refusal.site, ResultID: uint64(refusal.row), Address: clonePartAddress(address),
			Expected: refusal.expected.counters(), Current: refusal.current.counters(),
			FirstLoop: first.loop, FirstIteration: first.iteration, Iteration: iteration}
	}
	if s.progress == nil {
		s.progress = map[partProgressKey]partProgressSeen{}
	}
	s.progress[key] = partProgressSeen{loop: loop, iteration: iteration}
	return nil
}
