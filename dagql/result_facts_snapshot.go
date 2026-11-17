package dagql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrResultFactsSnapshotUnsupported means that the reference has no exact
// producer ID. Callers can use ResultFacts for its usual call lookup behavior.
var ErrResultFactsSnapshotUnsupported = errors.New("result facts snapshot requires an exact result ID")

// ResultFactsToken identifies one exact producer, frame and fact generation in
// one cache. It is comparable, process-local, and never a persisted identity.
// Holding a token does not retain session ownership or grant resource access.
type ResultFactsToken struct {
	cache           *Cache
	row             *sharedResult
	frame           *ResultCall
	frameRevision   uint64
	factsRevision   uint64
	payloadRevision uint64
	available       bool
}

// ResultFactsSnapshot is an immutable view of one namespace's facts. Retain it
// only for the lifetime of the caller's bounded, session-owned prepared state.
// A snapshot remains readable after replacement; validate its token before reuse.
// The zero value has no facts, coverage, or available producer.
type ResultFactsSnapshot struct {
	token     ResultFactsToken
	namespace string
	facts     []ResultFact
	complete  bool
}

func (s ResultFactsSnapshot) Token() ResultFactsToken { return s.token }
func (s ResultFactsSnapshot) Complete() bool          { return s.complete }
func (s ResultFactsSnapshot) Available() bool         { return s.token.available }

func (s ResultFactsSnapshot) ResultID() uint64 {
	if s.token.row == nil {
		return 0
	}
	return uint64(s.token.row.id)
}

// Call returns an independent copy of the producer frame covered by Token.
func (s ResultFactsSnapshot) Call() *ResultCall { return s.token.frame.clone() }

func (s ResultFactsSnapshot) Len() int {
	var n int
	for _, fact := range s.facts {
		if fact.Namespace == s.namespace {
			n++
		}
	}
	return n
}

// Decode decodes the indexed fact in this namespace. It copies the JSON before
// passing it to application code, including custom JSON unmarshallers. No method
// exposes writable cache-owned bytes. Decode once per token, namespace, and
// decoder format.
func (s ResultFactsSnapshot) Decode(index int, target any) error {
	if index < 0 {
		return fmt.Errorf("result facts snapshot: invalid fact index %d", index)
	}
	for _, fact := range s.facts {
		if fact.Namespace != s.namespace {
			continue
		}
		if index == 0 {
			return json.Unmarshal(bytes.Clone(fact.Payload), target)
		}
		index--
	}
	return fmt.Errorf("result facts snapshot: fact index out of range")
}

// ResultFactsSnapshot reads facts and frame metadata for an exact result ID,
// without evaluating the value or choosing a canonically equivalent result.
// Complete describes producer coverage even when this namespace has no facts.
// A holding-only row has Available=false and Complete=false; a cached null is
// available. Missing rows and denied access return the same errors as ResultFacts.
func (c *Cache) ResultFactsSnapshot(ctx context.Context, sessionID string, ref *ResultCallRef, namespace string) (snapshot ResultFactsSnapshot, rerr error) {
	if ref == nil {
		return snapshot, fmt.Errorf("result facts: nil reference")
	}
	if err := ref.Validate(); err != nil {
		return snapshot, err
	}
	op, err := c.beginSessionOperation(sessionID)
	if err != nil {
		return snapshot, err
	}
	defer func() {
		if op.finish(rerr == nil) {
			snapshot = ResultFactsSnapshot{}
			rerr = fmt.Errorf("result facts snapshot: %w: %q", ErrCacheSessionReleased, sessionID)
		}
	}()
	if err := context.Cause(ctx); err != nil {
		return snapshot, err
	}
	if ref.Call != nil {
		return snapshot, ErrResultFactsSnapshotUnsupported
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	if err := context.Cause(ctx); err != nil {
		return snapshot, err
	}
	row := c.resultsByID[sharedResultID(ref.ResultID)]
	if row == nil || !c.sessionSatisfiesResourceRequirementsLocked(sessionID, row) {
		return snapshot, fmt.Errorf("result facts: result is unavailable to session %q", sessionID)
	}
	token := ResultFactsToken{cache: c, row: row, available: !row.noValueLocked()}
	row.resultCallMu.RLock()
	token.frame, token.frameRevision = row.resultCall, row.resultCallRevision
	row.resultCallMu.RUnlock()
	row.payloadMu.RLock()
	token.factsRevision, token.payloadRevision = row.resultFactsRevision, row.payloadRevision
	snapshot = ResultFactsSnapshot{token: token, namespace: namespace}
	if token.available {
		snapshot.facts, snapshot.complete = row.resultFacts, !row.resultFactsIncomplete
	}
	row.payloadMu.RUnlock()
	if err := context.Cause(ctx); err != nil {
		return ResultFactsSnapshot{}, err
	}
	return snapshot, nil
}

// ValidateResultFactsSnapshots validates all dependencies without copying or
// decoding facts. A false result means rebuild or use normal import. Missing or
// replaced rows, frames, facts, payloads, and availability changes invalidate the
// batch; denied current access returns the same error as ResultFacts.
//
// For tokens read before this call, a successful batch describes the state when
// its graph read lock was acquired. Facts, availability and requirements cannot
// change under that lock. Frames and payloads also have separate locks; their
// monotonic revisions ensure every match was current at the start of the scan.
// Session handle sets only grow until release. Cancellation is checked after
// the scan, and session release before return refuses success.
func (c *Cache) ValidateResultFactsSnapshots(ctx context.Context, sessionID string, tokens []ResultFactsToken) (valid bool, rerr error) {
	op, err := c.beginSessionOperation(sessionID)
	if err != nil {
		return false, err
	}
	defer func() {
		if op.finish(rerr == nil) {
			valid = false
			rerr = fmt.Errorf("result facts snapshot: %w: %q", ErrCacheSessionReleased, sessionID)
		}
	}()
	if err := context.Cause(ctx); err != nil {
		return false, err
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	for _, token := range tokens {
		if err := context.Cause(ctx); err != nil {
			return false, err
		}
		if token.cache != c || token.row == nil || c.resultsByID[token.row.id] != token.row {
			return false, nil
		}
		row := token.row
		if !c.sessionSatisfiesResourceRequirementsLocked(sessionID, row) {
			return false, fmt.Errorf("result facts: result is unavailable to session %q", sessionID)
		}
		row.resultCallMu.RLock()
		sameFrame := row.resultCall == token.frame && row.resultCallRevision == token.frameRevision
		row.resultCallMu.RUnlock()
		row.payloadMu.RLock()
		samePayload := row.resultFactsRevision == token.factsRevision && row.payloadRevision == token.payloadRevision
		row.payloadMu.RUnlock()
		available := !row.noValueLocked()
		if !sameFrame || !samePayload || available != token.available {
			return false, nil
		}
	}
	if err := context.Cause(ctx); err != nil {
		return false, err
	}
	return true, nil
}
