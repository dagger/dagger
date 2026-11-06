package dagql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

// ResultFact is application-owned JSON attached to a value's lifetime. Payload
// must not contain engine-local result IDs. Facts do not affect value identity.
type ResultFact struct {
	Namespace string          `json:"namespace"`
	Payload   json.RawMessage `json:"payload"`
}

const resultFactsCoverageVersion = 1

func cloneResultFacts(facts []ResultFact) []ResultFact {
	out := slices.Clone(facts)
	for i := range out {
		out[i].Payload = bytes.Clone(out[i].Payload)
	}
	return out
}

func unionResultFacts(groups ...[]ResultFact) []ResultFact {
	var out []ResultFact
	for _, facts := range groups {
		out = append(out, facts...)
	}
	slices.SortFunc(out, func(a, b ResultFact) int {
		if n := bytes.Compare([]byte(a.Namespace), []byte(b.Namespace)); n != 0 {
			return n
		}
		return bytes.Compare(a.Payload, b.Payload)
	})
	return slices.CompactFunc(out, func(a, b ResultFact) bool {
		return a.Namespace == b.Namespace && bytes.Equal(a.Payload, b.Payload)
	})
}

func resultFactsEqual(a, b []ResultFact) bool {
	return slices.EqualFunc(a, b, func(a, b ResultFact) bool {
		return a.Namespace == b.Namespace && bytes.Equal(a.Payload, b.Payload)
	})
}

func resultFactPayloadBytes(facts []ResultFact) int64 {
	var n int64
	for _, fact := range facts {
		n += int64(len(fact.Namespace) + len(fact.Payload))
	}
	return n
}

func setEnvelopeResultFacts(env *PersistedResultEnvelope, facts []ResultFact, incomplete bool) {
	env.ResultFacts = cloneResultFacts(facts)
	env.FactsCoverageVersion = resultFactsCoverageVersion
	if incomplete {
		env.FactsCoverageVersion = 0
	}
}

// setResultFactsLocked replaces immutable fact bytes under egraphMu. A decoded
// value keeps its facts independently of the temporary persisted envelope.
func (c *Cache) setResultFactsLocked(row *sharedResult, facts []ResultFact, incomplete bool) {
	if resultFactsEqual(row.resultFacts, facts) && row.resultFactsIncomplete == incomplete {
		coverage := resultFactsCoverageVersion
		if incomplete {
			coverage = 0
		}
		row.payloadMu.RLock()
		env := row.persistedEnvelope
		current := env == nil || (env.FactsCoverageVersion == coverage && resultFactsEqual(env.ResultFacts, facts))
		row.payloadMu.RUnlock()
		if current {
			return
		}
	}
	facts = cloneResultFacts(facts)
	n := resultFactPayloadBytes(facts)
	if c.resultsByID[row.id] == row {
		c.resultPayloadBytes += n - row.resultFactBytes
	}
	row.payloadBytes += n - row.resultFactBytes
	row.resultFactBytes = n
	row.transferRevision++
	row.storedTransfer.Store(nil)
	row.payloadMu.Lock()
	row.resultFacts, row.resultFactsIncomplete = facts, incomplete
	// Facts change record bytes, but do not change snapshot-part completion.
	row.resultFactsRevision++
	if row.persistedEnvelope != nil {
		env := *row.persistedEnvelope
		setEnvelopeResultFacts(&env, facts, incomplete)
		env.ResultFacts = facts
		row.persistedEnvelope = &env
		if row.storedRecordBytes != 0 {
			row.storedRecordBytes = encodedRecordBytes(PersistedRecord{Call: row.loadResultCall(), Envelope: env})
		}
	}
	row.payloadMu.Unlock()
}

func validateResultFacts(env PersistedResultEnvelope, root bool) error {
	if !root && (env.FactsCoverageVersion != 0 || len(env.ResultFacts) != 0) {
		return fmt.Errorf("result facts are only valid on a root envelope")
	}
	if env.FactsCoverageVersion != 0 && env.FactsCoverageVersion != resultFactsCoverageVersion {
		return fmt.Errorf("unsupported result facts coverage version %d", env.FactsCoverageVersion)
	}
	for _, fact := range env.ResultFacts {
		if fact.Namespace == "" || !json.Valid(fact.Payload) {
			return fmt.Errorf("result fact requires namespace and valid JSON")
		}
	}
	return nil
}

// syncResultFactsLocked copies the union to every equivalent value. A later
// collection of one producer must not remove an origin from surviving values.
func (c *Cache) syncResultFactsLocked(seeds ...*sharedResult) {
	rows := map[*sharedResult]bool{}
	queue := slices.Clone(seeds)
	var groups [][]ResultFact
	incomplete := false
	for len(queue) != 0 {
		row := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if row == nil || rows[row] {
			continue
		}
		rows[row] = true
		if row.hasOwnValueLocked() {
			groups = append(groups, row.resultFacts)
			incomplete = incomplete || row.resultFactsIncomplete
		}
		for class := range c.outputEqClassRootsLocked(row.id) {
			for id := range c.outputEqClassResults[class] {
				if peer := c.resultsByID[id]; peer != nil && !rows[peer] {
					queue = append(queue, peer)
				}
			}
		}
	}
	facts := unionResultFacts(groups...)
	for row := range rows {
		if row.hasOwnValueLocked() {
			c.setResultFactsLocked(row, facts, incomplete)
		}
	}
	for row := range rows {
		// DerefValue normally keeps the outer row. Its nullable AnyResult
		// branch instead returns an existing inner row and has no context in
		// which to find this cache. Give that inner row the outer facts while
		// storing them, before a later dereference can expose its ID.
		state := row.loadPayloadState()
		if inner, valid := derefTyped(state.self); valid {
			if result, ok := inner.(AnyResult); ok {
				c.inheritResultFactsLocked(row, result.cacheSharedResult())
			}
		}
	}
}

func (c *Cache) inheritResultFactsLocked(parent, child *sharedResult) {
	if parent == nil || child == nil || parent == child || (child.id != 0 && c.resultsByID[child.id] != child) {
		return
	}
	facts := unionResultFacts(child.resultFacts, parent.resultFacts)
	incomplete := child.resultFactsIncomplete || parent.resultFactsIncomplete
	if resultFactsEqual(child.resultFacts, facts) && child.resultFactsIncomplete == incomplete {
		return
	}
	c.setResultFactsLocked(child, facts, incomplete)
	c.syncResultFactsLocked(child)
}

// inheritResultFacts is a trusted value-conversion operation. It adds no
// execution dependency or value equivalence between the parent and child.
func (c *Cache) inheritResultFacts(parent, child *sharedResult) {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	if parent == nil || (parent.id != 0 && c.resultsByID[parent.id] != parent) {
		return
	}
	c.inheritResultFactsLocked(parent, child)
}

// applyRequestFactsLocked runs in publication's graph critical section, or
// before returning an existing value. Facts were prepared before execution.
func (c *Cache) applyRequestFactsLocked(req *CallRequest, row *sharedResult) {
	if requestFactsSatisfiedLocked(req, row) {
		return
	}
	c.setResultFactsLocked(row, unionResultFacts(row.resultFacts, req.ResultFacts), row.resultFactsIncomplete || req.ResultFactsIncomplete)
	c.syncResultFactsLocked(row)
}

// requestFactsSatisfiedLocked needs only the graph read lock. Repeat facts
// do not change the row or its already-synchronized equivalent values.
func requestFactsSatisfiedLocked(req *CallRequest, row *sharedResult) bool {
	if req == nil || row == nil {
		return true
	}
	if req.ResultFactsIncomplete && !row.resultFactsIncomplete {
		return false
	}
	for _, fact := range req.ResultFacts {
		if !slices.ContainsFunc(row.resultFacts, func(existing ResultFact) bool {
			return existing.Namespace == fact.Namespace && bytes.Equal(existing.Payload, fact.Payload)
		}) {
			return false
		}
	}
	return true
}

// ResultFacts reads facts without evaluating a value. complete reports coverage
// of all origins, independently of whether this namespace has any records.
func (c *Cache) ResultFacts(ctx context.Context, sessionID string, ref *ResultCallRef, namespace string) ([]ResultFact, bool, error) {
	if ref == nil {
		return nil, false, fmt.Errorf("result facts: nil reference")
	}
	if err := ref.Validate(); err != nil {
		return nil, false, err
	}
	op, err := c.beginSessionOperation(sessionID)
	if err != nil {
		return nil, false, err
	}
	defer op.finish(false)
	if err := context.Cause(ctx); err != nil {
		return nil, false, err
	}
	id := sharedResultID(ref.ResultID)
	if ref.Call != nil {
		id, err = c.resultIDForCall(ctx, ref.Call)
		if err != nil {
			return nil, false, nil
		}
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	row := c.resultsByID[id]
	if row == nil || !c.sessionSatisfiesResourceRequirementsLocked(sessionID, row) {
		return nil, false, fmt.Errorf("result facts: result is unavailable to session %q", sessionID)
	}
	if !row.hasOwnValueLocked() {
		return nil, false, nil
	}
	var facts []ResultFact
	for _, fact := range row.resultFacts {
		if fact.Namespace == namespace {
			facts = append(facts, fact)
		}
	}
	return cloneResultFacts(facts), !row.resultFactsIncomplete, nil
}
