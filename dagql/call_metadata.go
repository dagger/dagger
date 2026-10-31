package dagql

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/opencontainers/go-digest"
)

// CallObserver observes a planned field call, after dynamic inputs have been
// resolved. Its completion callback runs on hits, misses and errors, including
// fields that suppress telemetry. It must not infer execution from a cache hit.
// The completion bool reports whether this invocation's initializer started.
// Returning an existing result does not make it false, and joining another
// invocation's initializer does not make it true.
// Engine-side invocation state must be forwarded explicitly to nested clients;
// a context value alone cannot cross their transport boundary.
type CallObserver func(context.Context, *CallRequest) (context.Context, func(AnyResult, bool, error))

// ObserveCalls installs an observer independently of telemetry. Like Around,
// install this before serving requests. Fork inherits the observer.
func (s *Server) ObserveCalls(observer CallObserver) {
	s.callObserver = observer
}

// Clone returns an independent copy while preserving shared call subgraphs.
func (frame *ResultCall) Clone() *ResultCall { return frame.clone() }

// ResultCallForRef resolves descriptive call metadata without evaluating or
// decoding the referenced value. The returned frame is immutable.
func (c *Cache) ResultCallForRef(ref *ResultCallRef) (*ResultCall, error) {
	if ref == nil {
		return nil, fmt.Errorf("resolve result call: nil reference")
	}
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	return (*ResultCall)(nil).refCall(c, ref)
}

const callMetadataOp = "dagql.callMetadata.v1"

// CallMetadataRecord belongs to an exact origin recipe, never its output's
// content-equivalence class. Payload is opaque JSON: it must not contain
// engine-local result IDs, which this store cannot relocate. CandidateKey is
// only a search key; finding a record does not validate its reuse.
type CallMetadataRecord struct {
	Origin        digest.Digest   `json:"origin"`
	Namespace     string          `json:"namespace"`
	CandidateKey  string          `json:"candidateKey,omitempty"`
	Payload       json.RawMessage `json:"payload"`
	ExpiresAtUnix int64           `json:"expiresAtUnix,omitempty"`
}

// StoreCallMetadata stores an opaque completed-call observation as an ordinary
// cache scalar. It uses the origin's persistence and TTL policy, retains its
// result and structural inputs, and participates in normal cache pruning. Its
// row indexes add no output equivalence and own no cache holds.
//
// The caller is responsible for recording complete coverage, including errors
// and nested cache hits. DoNotCache observations are not retained. Other
// invocation policies are part of the origin recipe, and must also be checked
// by callers using a broader CandidateKey.
// Pass the completed result when available to retain its exact entry rather
// than resolving the recipe to a representative.
func (c *Cache) StoreCallMetadata(ctx context.Context, sessionID string, resolver TypeResolver, origin *CallRequest, namespace, candidateKey string, payload json.RawMessage, result ...AnyResult) error {
	if c == nil || origin == nil || origin.ResultCall == nil || namespace == "" {
		return fmt.Errorf("store call metadata: origin and namespace are required")
	}
	if !json.Valid(payload) {
		return fmt.Errorf("store call metadata: payload is not valid JSON")
	}
	if origin.DoNotCache {
		return nil
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	originDigest, err := origin.deriveRecipeDigest(c)
	if err != nil {
		return fmt.Errorf("store call metadata: origin recipe: %w", err)
	}
	// Only a published call can own a reusable observation. Failed calls still
	// reach the observer, but have no completed cache row to retain here.
	var originID sharedResultID
	var provided *sharedResult
	if len(result) > 1 {
		return fmt.Errorf("store call metadata: at most one completed result is allowed")
	}
	if len(result) == 1 && result[0] != nil && result[0].cacheSharedResult() != nil {
		provided = result[0].cacheSharedResult()
		originID = provided.id
	} else {
		originID, err = c.resultIDForCall(origin.ResultCall)
		if err != nil {
			return nil
		}
	}
	c.egraphMu.RLock()
	originRow := c.resultsByID[originID]
	if originRow == nil || (provided != nil && provided != originRow) || !c.sessionSatisfiesResourceRequirementsLocked(sessionID, originRow) {
		c.egraphMu.RUnlock()
		return nil
	}
	deadline := originRow.expiresAtUnix
	persistable := origin.IsPersistable && !originRow.persistDeclined
	c.egraphMu.RUnlock()
	record := CallMetadataRecord{
		Origin: originDigest, Namespace: namespace, CandidateKey: candidateKey,
		Payload: bytes.Clone(payload), ExpiresAtUnix: deadline,
	}
	if deadline != 0 && deadline <= time.Now().Unix() {
		return nil
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	// Including the observation's digest prevents a later conflicting report
	// from silently adopting an older value. Readers reject conflicting reports
	// for one origin rather than inventing a merge rule for opaque payloads.
	frame := &ResultCall{
		Kind: ResultCallKindSynthetic, SyntheticOp: callMetadataOp,
		Type:     NewResultCallType(NewString("").Type()),
		Receiver: &ResultCallRef{ResultID: uint64(originID)},
		Args: []*ResultCallArg{
			callMetadataStringArg("namespace", namespace),
			callMetadataStringArg("candidateKey", candidateKey),
			callMetadataStringArg("observation", digest.FromBytes(encoded).String()),
		},
	}
	// An adopted output may not retain this request's inputs. Keep their
	// individual references; wrapping the origin would resolve to its output.
	_, refs, err := origin.selfDigestAndInputRefs(c)
	if err != nil {
		return fmt.Errorf("store call metadata: origin inputs: %w", err)
	}
	for i, ref := range refs {
		if ref.Result != nil {
			frame.ImplicitInputs = append(frame.ImplicitInputs, &ResultCallArg{
				Name:  fmt.Sprintf("input%d", i),
				Value: &ResultCallLiteral{Kind: ResultCallLiteralKindResultRef, ResultRef: ref.Result},
			})
		}
	}
	_, err = c.GetOrInitCall(ctx, sessionID, resolver, &CallRequest{
		ResultCall: frame, TTL: origin.TTL, IsPersistable: persistable,
		ConcurrencyKey: sessionID,
	}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(NewString(string(encoded)), frame)
	})
	return err
}

func callMetadataStringArg(name, value string) *ResultCallArg {
	return &ResultCallArg{Name: name, Value: &ResultCallLiteral{Kind: ResultCallLiteralKindString, StringValue: value}}
}

// CallMetadata returns a copied observation for this exact origin recipe. A
// missing record, including data written before recording was enabled, is a
// miss. It never borrows metadata from an equivalent output or another recipe.
func (c *Cache) CallMetadata(ctx context.Context, sessionID, namespace string, origin *ResultCall) (json.RawMessage, bool, error) {
	if origin == nil {
		return nil, false, nil
	}
	originDigest, err := origin.deriveRecipeDigest(c)
	if err != nil {
		return nil, false, err
	}
	records, err := c.callMetadataRecords(ctx, sessionID, namespace, func() map[sharedResultID]struct{} {
		return c.callMetadata.byOrigin[callMetadataLookupKey{namespace, originDigest.String()}]
	})
	if err != nil || len(records) != 1 {
		return nil, false, err
	}
	return records[0].Payload, true, nil
}

// CallMetadataCandidates finds observations by an application-defined key.
// They still require application-level validation of all inputs and policies.
// This uses ordinary cache row indexes; it does not execute calls, force lazy parts,
// or decode objects. Expired, unavailable and conflicting origins are omitted.
func (c *Cache) CallMetadataCandidates(ctx context.Context, sessionID, namespace, candidateKey string) ([]CallMetadataRecord, error) {
	return c.callMetadataRecords(ctx, sessionID, namespace, func() map[sharedResultID]struct{} {
		return c.callMetadata.byCandidate[callMetadataLookupKey{namespace, candidateKey}]
	})
}

// CallMetadataForResult finds all recorded origins of a result, including
// origins whose output is equivalent to it. This is a conservative fallback
// when a result reference no longer identifies the requesting recipe: a field
// can return an existing result, or the cache can serve an equivalent producer.
// Callers must combine these records, not choose the narrowest origin.
// The records do not establish that every alias was observed. Missing coverage
// must remain unknown even if another equivalent producer has a record.
func (c *Cache) CallMetadataForResult(ctx context.Context, sessionID, namespace string, ref *ResultCallRef) ([]CallMetadataRecord, error) {
	if ref == nil {
		return nil, nil
	}
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	resultID := sharedResultID(ref.ResultID)
	if ref.Call != nil {
		var err error
		resultID, err = c.resultIDForCall(ref.Call)
		if err != nil {
			return nil, nil
		}
	}
	return c.callMetadataRecords(ctx, sessionID, namespace, func() map[sharedResultID]struct{} {
		selected := map[sharedResultID]struct{}{}
		addOutput := func(id sharedResultID) {
			for rowID := range c.callMetadata.byResult[id] {
				selected[rowID] = struct{}{}
			}
		}
		addOutput(resultID)
		// Output equality only widens this fallback. Metadata observations
		// themselves remain distinct and never acquire output equivalence.
		for class := range c.outputEqClassRootsLocked(resultID) {
			for id := range c.outputEqClassResults[class] {
				addOutput(id)
			}
		}
		return selected
	})
}

// selectRows runs under egraphMu. Conflict checks include every observation of
// each selected origin, including observations with a different candidate key.
func (c *Cache) callMetadataRecords(ctx context.Context, sessionID, namespace string, selectRows func() map[sharedResultID]struct{}) ([]CallMetadataRecord, error) {
	op, err := c.beginSessionOperation(sessionID)
	if err != nil {
		return nil, err
	}
	defer op.finish(false)
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	byOrigin := map[digest.Digest]CallMetadataRecord{}
	matched := map[digest.Digest]bool{}
	conflicts := map[digest.Digest]bool{}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	selected := selectRows()
	for id := range c.callMetadataOriginsLocked(selected, namespace) {
		entry := c.callMetadata.records[id]
		row := c.resultsByID[id]
		if row == nil || c.servableCandidateLocked(id, now, nil) == nil || row.attachmentState() != resultAttachmentClean || !c.sessionSatisfiesResourceRequirementsLocked(sessionID, row) {
			continue
		}
		record := entry.record
		if record.ExpiresAtUnix != 0 && record.ExpiresAtUnix <= now {
			continue
		}
		if previous, ok := byOrigin[record.Origin]; ok && (!bytes.Equal(previous.Payload, record.Payload) || previous.CandidateKey != record.CandidateKey) {
			conflicts[record.Origin] = true
		}
		byOrigin[record.Origin] = record
		if _, ok := selected[id]; ok {
			matched[record.Origin] = true
		}
	}
	var records []CallMetadataRecord
	for origin, record := range byOrigin {
		if !conflicts[origin] && matched[origin] {
			record.Payload = bytes.Clone(record.Payload)
			records = append(records, record)
		}
	}
	slices.SortFunc(records, func(a, b CallMetadataRecord) int { return cmp.Compare(a.Origin, b.Origin) })
	return records, nil
}

func callMetadataFrameArg(frame *ResultCall, name string) (string, bool) {
	for _, arg := range frame.Args {
		if arg != nil && arg.Name == name && arg.Value != nil && arg.Value.Kind == ResultCallLiteralKindString {
			return arg.Value.StringValue, true
		}
	}
	return "", false
}
