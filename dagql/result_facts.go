package dagql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

// ResultFact is application-owned JSON attached to a call result's lifetime. Payload
// must not contain engine-local result IDs. Facts do not affect value identity.
type ResultFact struct {
	Namespace string          `json:"namespace"`
	Payload   json.RawMessage `json:"payload"`
}

const resultFactsCoverageVersion = 2

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
	if env.FactsCoverageVersion != 0 && env.FactsCoverageVersion != 1 && env.FactsCoverageVersion != resultFactsCoverageVersion {
		return fmt.Errorf("unsupported result facts coverage version %d", env.FactsCoverageVersion)
	}
	for _, fact := range env.ResultFacts {
		if fact.Namespace == "" || !json.Valid(fact.Payload) {
			return fmt.Errorf("result fact requires namespace and valid JSON")
		}
	}
	return nil
}

// applyRequestFactsLocked runs in publication's graph critical section, or
// before returning an existing value. Facts were prepared before execution.
func (c *Cache) applyRequestFactsLocked(req *CallRequest, row *sharedResult) {
	if requestFactsSatisfiedLocked(req, row) {
		return
	}
	c.setResultFactsLocked(row, unionResultFacts(row.resultFacts, req.ResultFacts), row.resultFactsIncomplete || req.ResultFactsIncomplete)
}

// requestFactsSatisfiedLocked needs only the graph read lock. Repeat facts
// do not change the row.
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
// of this producer, independently of whether this namespace has any records.
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
	// A cached null has facts even though it has no decoded or stored value.
	// Use the same availability rule as call lookup to exclude holding-only rows.
	if row.noValueLocked() {
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
