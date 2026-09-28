package dagui

import (
	"sort"
)

// Rewinds: the client half of the rewind marker contract
// (engine/telemetryattrs, AgentRewindFromDigestAttr).
//
// When an agent's conversation is replaced by one of its own ancestors —
// inline prompt editing rewinds to the state just before the edited prompt —
// every message span emitted between the two is still in the trace, but no
// longer in the model's history. The engine records the fork as a marker
// span carrying the two recipe digests; this file joins that marker against
// the message spans, using the same receiver walk over ingested call payloads
// that branch-from-message and prompt editing rely on, so a transcript can
// show which messages the model no longer remembers.

// Rewind is one recorded rewind of an agent's conversation: the marker span
// and the message spans it abandoned.
type Rewind struct {
	// Span is the marker the engine emitted at the point the conversation
	// forked — the row a transcript resumes from.
	Span *Span

	// Abandoned are the surfaced message spans (prompts, replies, thinking,
	// tool calls) between the adopted and abandoned conversations, oldest
	// first. Empty when the marker's chain could not be walked with the
	// payloads this client holds; the marker itself still stands.
	Abandoned []*Span
}

// Rewinds returns every rewind marker in the DB with the messages it
// abandoned, oldest first. Cached per DB mutation; callers must treat the
// result as read-only.
func (db *DB) Rewinds() []*Rewind {
	if db.rewindsInit && db.rewindsAt == db.mutations {
		return db.rewinds
	}
	db.rewinds, db.superseded = db.buildRewinds()
	db.rewindsAt = db.mutations
	db.rewindsInit = true
	return db.rewinds
}

// RewindFor returns the rewind a marker span records, or nil for any other
// span.
func (db *DB) RewindFor(marker *Span) *Rewind {
	if marker == nil || !marker.AgentRewindMarker() {
		return nil
	}
	for _, rewind := range db.Rewinds() {
		if rewind.Span == marker {
			return rewind
		}
	}
	return nil
}

// SupersededBy returns the rewind that abandoned span, or nil while the span
// is still part of its conversation. A span beneath an abandoned message (a
// tool call's execution subtree) is abandoned with it.
func (db *DB) SupersededBy(span *Span) *Rewind {
	if span == nil {
		return nil
	}
	db.Rewinds()
	if len(db.superseded) == 0 {
		return nil
	}
	for cur := span; cur != nil; cur = cur.ParentSpan {
		if rewind, ok := db.superseded[cur.ID]; ok {
			return rewind
		}
		if cur.Agent {
			// A loop span is never abandoned; nothing above it can be
			// either, so stop before walking into the enclosing agent.
			return nil
		}
	}
	return nil
}

func (db *DB) buildRewinds() ([]*Rewind, map[SpanID]*Rewind) {
	var markers []*Span
	for span := range db.Spans.Iter() {
		if span.AgentRewindMarker() {
			markers = append(markers, span)
		}
	}
	if len(markers) == 0 {
		return nil, nil
	}
	sort.SliceStable(markers, func(i, j int) bool {
		return markers[i].Before(markers[j])
	})

	rewinds := make([]*Rewind, 0, len(markers))
	superseded := map[SpanID]*Rewind{}
	for _, marker := range markers {
		rewind := &Rewind{Span: marker}
		rewinds = append(rewinds, rewind)
		abandoned := db.abandonedDigests(marker.AgentRewindFrom, marker.AgentRewindTo)
		if len(abandoned) == 0 {
			continue
		}
		owner := nearestAgentID(marker)
		for span := range db.Spans.Iter() {
			if span.LLMRole == "" || span.Internal || span.AgentRewindMarker() {
				continue
			}
			if !abandoned[span.LLMCallDigest] {
				continue
			}
			// The same digest is reused when the edited prompt is
			// resubmitted verbatim: the abandoned messages are the ones
			// that predate the marker, never the ones the resumed
			// conversation emits after it.
			if !span.StartTime.Before(marker.StartTime) {
				continue
			}
			if nearestAgentID(span) != owner {
				continue
			}
			if _, taken := superseded[span.ID]; taken {
				// An earlier rewind already abandoned it; the earliest
				// marker is the one a transcript should attribute it to.
				continue
			}
			superseded[span.ID] = rewind
			rewind.Abandoned = append(rewind.Abandoned, span)
		}
		sort.SliceStable(rewind.Abandoned, func(i, j int) bool {
			return rewind.Abandoned[i].Before(rewind.Abandoned[j])
		})
	}
	return rewinds, superseded
}

// abandonedDigests walks the abandoned conversation's receiver chain back to
// the adopted one and returns every call digest strictly after it — the LLM
// states whose message spans the rewind cut. It returns nil when the adopted
// digest is not reached: either a payload never arrived, or the marker does
// not describe a rewind this client can interpret, and in both cases marking
// nothing is the honest choice.
func (db *DB) abandonedDigests(from, to string) map[string]bool {
	if from == "" || to == "" || from == to {
		return nil
	}
	digests := map[string]bool{}
	cur := from
	for cur != to {
		if cur == "" || digests[cur] {
			// Ran off the root, or looped: the adopted state is not an
			// ancestor of the abandoned one as far as this client can see.
			return nil
		}
		digests[cur] = true
		call := db.Call(cur)
		if call == nil {
			return nil
		}
		cur = call.ReceiverDigest
	}
	return digests
}

// nearestAgentID is the runtime handle of the loop span enclosing span, or
// "" outside any agent.
func nearestAgentID(span *Span) string {
	for cur := span; cur != nil; cur = cur.ParentSpan {
		if cur.Agent {
			return cur.AgentID
		}
	}
	return ""
}
