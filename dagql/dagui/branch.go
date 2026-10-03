package dagui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	telemetry "github.com/dagger/otel-go"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// Branch targets: which LLM state a branch from a transcript row keeps.
//
// Branching follows Pi's /tree and Claude Code's /rewind, and never starts a
// turn. A row's span carries the digest of an LLM call (LLMCallDigest), but
// which call depends on the row:
//
//   - A message (a user prompt, or one another agent or the engine put on the
//     record) carries its withPrompt/withContent state. Branching from it goes
//     back to just BEFORE it: the message is abandoned, and its text returns
//     to the editor to be edited and resubmitted — the inline-edit rewind.
//   - A reply's spans carry its withResponse state — or, for spans recorded
//     before the engine re-stamped them, the REQUEST state the reply answered,
//     which does not hold the reply at all. Branching from it keeps it. A
//     reply that called tools is not settled at its withResponse: the step
//     goes on to record each tool's result, and a history that stops at an
//     unanswered tool call is one no provider accepts, so the branch keeps
//     the results too.
//
// LLMBranchDigest resolves all of that from the call payloads this client
// ingested, which is also what the rewind marker computed from the engine's
// reseed is joined against, so the transcript agrees with the engine.

// llmStepFields are the selectors a step records after a response, before
// the next request: the tools' results, and the state changes the tools made
// (see core's stateDeltaSelectors).
var llmStepFields = []string{"withToolResult", "withWorkspace", "withTools"}

// llmMessageFields are the selectors that record a user-role message.
var llmMessageFields = []string{"withPrompt", "withContent"}

// LLMCallCarrier returns span, or its nearest ancestor, carrying an LLM call
// digest: the conversation row a nested span (a tool's execution, say)
// belongs to. Nil when there is none.
func LLMCallCarrier(span *Span) *Span {
	for s := span; s != nil; s = s.ParentSpan {
		if s.LLMCallDigest != "" {
			return s
		}
	}
	return nil
}

// LLMBranchDigest returns the digest of the LLM state a branch from span
// goes to: for a user-role message, the state just before it; for a reply
// (text, thinking, a tool call, or anything beneath one), the state that
// settles that reply. Spans without an LLM call digest inherit their nearest
// ancestor's.
//
// It fails rather than guess when the payloads cannot pin the state down — a
// message's or reply's call never reached this client, or several candidates
// do — since branching to a guessed state would silently drop or invent
// history.
func (db *DB) LLMBranchDigest(span *Span) (string, error) {
	carrier := LLMCallCarrier(span)
	if carrier == nil {
		return "", fmt.Errorf("no LLM call to branch from")
	}
	digest := carrier.LLMCallDigest
	if carrier.LLMRole != telemetry.LLMRoleAssistant {
		msg := db.LLMMessageCall(carrier)
		if msg == nil || msg.ReceiverDigest == "" {
			return "", fmt.Errorf("the call recording this message never reached this client")
		}
		return msg.ReceiverDigest, nil
	}
	response, err := db.llmResponseDigest(digest)
	if err != nil {
		return "", err
	}
	return db.llmStepEndDigest(response)
}

// LLMMessageCall returns the withPrompt or withContent call that recorded a
// user-role message span, or nil if it cannot be resolved.
//
// Message spans carry the digest of the request they were sent in, so
// several messages drained at one step boundary share the final message's
// digest. Those peers (same digest, same agent) are mapped by start order
// onto the consecutive message calls of the receiver chain, oldest first.
func (db *DB) LLMMessageCall(span *Span) *callpbv1.Call {
	if span == nil || span.LLMCallDigest == "" || span.LLMRole != telemetry.LLMRoleUser {
		return nil
	}
	digest := span.LLMCallDigest
	call := db.Call(digest)
	for call != nil && !slices.Contains(llmMessageFields, call.Field) {
		call = db.Call(call.ReceiverDigest)
	}
	if call == nil {
		return nil
	}
	owner := nearestAgentID(span)
	var peers []*Span
	for candidate := range db.Spans.Iter() {
		if !candidate.Internal && candidate.LLMRole == telemetry.LLMRoleUser &&
			candidate.LLMCallDigest == digest && nearestAgentID(candidate) == owner {
			peers = append(peers, candidate)
		}
	}
	sort.SliceStable(peers, func(i, j int) bool {
		return peers[i].StartTime.Before(peers[j].StartTime)
	})
	selected := slices.Index(peers, span)
	if selected < 0 {
		return nil
	}
	for range len(peers) - selected - 1 {
		call = db.Call(call.ReceiverDigest)
		if call == nil || !slices.Contains(llmMessageFields, call.Field) {
			return nil
		}
	}
	return call
}

// LLMMessageText returns the text a withPrompt or withContent call recorded:
// its prompt, or its text blocks joined by blank lines. Non-text content
// (images, documents) has no text form and is left out.
func LLMMessageText(call *callpbv1.Call) string {
	if call == nil {
		return ""
	}
	for _, arg := range call.Args {
		if arg == nil || arg.Value == nil {
			continue
		}
		switch arg.Name {
		case "prompt":
			return literalString(arg.Value)
		case "content":
			var texts []string
			for _, block := range arg.Value.GetList().GetValues() {
				var kind, text string
				for _, field := range block.GetObject().GetValues() {
					switch field.GetName() {
					case "kind":
						kind = field.GetValue().GetEnum()
					case "text":
						text = literalString(field.GetValue())
					}
				}
				if kind == "TEXT" && text != "" {
					texts = append(texts, text)
				}
			}
			return strings.Join(texts, "\n\n")
		}
	}
	return ""
}

// literalString reads a string literal, inline or digested.
func literalString(lit *callpbv1.Literal) string {
	if ds := lit.GetDigestedString(); ds != nil {
		return ds.GetValue()
	}
	return lit.GetString_()
}

// llmResponseDigest returns the withResponse call a reply span belongs to,
// given the digest it carries: the withResponse itself, or — for a span
// still carrying its request's digest — the one response recorded on that
// request.
func (db *DB) llmResponseDigest(digest string) (string, error) {
	if call := db.Call(digest); call != nil && call.Field == "withResponse" {
		return call.Digest, nil
	}
	responses := db.llmChildren(digest, "withResponse")
	switch len(responses) {
	case 0:
		return "", fmt.Errorf("the response to %s never reached this client", digest)
	case 1:
		return responses[0], nil
	default:
		return "", fmt.Errorf("%s has %d recorded responses; cannot tell which one this reply is", digest, len(responses))
	}
}

// llmStepEndDigest follows a response through the selectors its step
// recorded after it, returning the state that settles it: the response
// itself when it called no tools, else the state after its tool results.
func (db *DB) llmStepEndDigest(response string) (string, error) {
	cur, end := response, response
	seen := map[string]bool{response: true}
	for {
		next := db.llmChildren(cur, llmStepFields...)
		if len(next) == 0 {
			return end, nil
		}
		if len(next) > 1 {
			return "", fmt.Errorf("%s was continued %d ways; cannot tell which one this reply settled into", cur, len(next))
		}
		cur = next[0]
		if seen[cur] {
			return "", fmt.Errorf("call chain loops at %s", cur)
		}
		seen[cur] = true
		if db.Calls[cur].Field == "withToolResult" || end != response {
			// Results settle the step; state changes recorded around them
			// belong to it too. Without any result, a bare withWorkspace or
			// withTools on a reply is a later, separate change.
			end = cur
		}
	}
}

// llmChildren returns, sorted, the digests of ingested calls on receiver
// whose field is one of fields.
func (db *DB) llmChildren(receiver string, fields ...string) []string {
	var out []string
	for dig, call := range db.Calls {
		if call != nil && call.ReceiverDigest == receiver && slices.Contains(fields, call.Field) {
			out = append(out, dig)
		}
	}
	sort.Strings(out)
	return out
}
