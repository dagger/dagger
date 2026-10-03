package dagui

import (
	"fmt"
	"slices"
	"sort"

	telemetry "github.com/dagger/otel-go"
)

// Branch targets: which LLM state a branch from a transcript row keeps.
//
// A row's span carries the digest of an LLM call (LLMCallDigest), but which
// call depends on the row. A prompt carries its withPrompt state: branching
// there keeps the prompt, and the agent answers it again. A reply's spans
// carry its withResponse state — or, for spans recorded before the engine
// re-stamped them, the REQUEST state the reply answered, which does not hold
// the reply at all. And a reply that called tools is not settled at its
// withResponse: the step goes on to record each tool's result, and a history
// that stops at an unanswered tool call is one no provider accepts.
//
// LLMBranchDigest resolves all of that from the call payloads this client
// ingested, so a branch from a reply keeps exactly that reply and its tool
// results — the conversation the transcript shows up to that row, which is
// also what the rewind marker computed from the engine's reseed will agree
// with.

// llmStepFields are the selectors a step records after a response, before
// the next request: the tools' results, and the state changes the tools made
// (see core's stateDeltaSelectors).
var llmStepFields = []string{"withToolResult", "withWorkspace", "withTools"}

// LLMBranchDigest returns the digest of the LLM state a branch from span
// keeps: the span's own LLM call, or for a reply (text, thinking, a tool
// call, or anything beneath one) the state that settles that reply. Spans
// without an LLM call digest inherit their nearest ancestor's.
//
// It fails rather than guess when the payloads cannot pin the state down — a
// reply's response or tool results never reached this client, or several
// candidates do — since branching to a guessed state would silently drop or
// invent history.
func (db *DB) LLMBranchDigest(span *Span) (string, error) {
	var carrier *Span
	for s := span; s != nil; s = s.ParentSpan {
		if s.LLMCallDigest != "" {
			carrier = s
			break
		}
	}
	if carrier == nil {
		return "", fmt.Errorf("no LLM call to branch from")
	}
	digest := carrier.LLMCallDigest
	if carrier.LLMRole != telemetry.LLMRoleAssistant {
		return digest, nil
	}
	response, err := db.llmResponseDigest(digest)
	if err != nil {
		return "", err
	}
	return db.llmStepEndDigest(response)
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
