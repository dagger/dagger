package dagui

import (
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// branchFixture is one agent's transcript over the chain
//
//	llm -> withPrompt(p1) -> withResponse(r1, text) -> withPrompt(p2) ->
//	withResponse(r2, tool call) -> withToolResult(tr) -> withWorkspace(ws) ->
//	withResponse(r3, text) -> withPrompt(ev, an event from another agent) ->
//	withResponse(r4, text)
//
// with reply spans stamped at their withResponse, plus "legacy" reply spans
// still carrying the request digest they answered.
type branchFixture struct {
	db *DB
}

const (
	bfLoop byte = iota + 1
	bfPrompt1
	bfReply1
	bfLegacyReply1
	bfPrompt2
	bfTool
	bfToolExec
	bfLegacyTool
	bfReply3
	bfLegacyReply3
	bfEvent
	bfReply4
)

func newBranchFixture(t *testing.T) *branchFixture {
	t.Helper()
	db := NewDB()
	llmCall(db, "xxh3:llm", "llm", "")
	llmCall(db, "xxh3:p1", "withPrompt", "xxh3:llm")
	llmCall(db, "xxh3:r1", "withResponse", "xxh3:p1")
	llmCall(db, "xxh3:p2", "withPrompt", "xxh3:r1")
	llmCall(db, "xxh3:r2", "withResponse", "xxh3:p2")
	llmCall(db, "xxh3:tr", "withToolResult", "xxh3:r2")
	llmCall(db, "xxh3:ws", "withWorkspace", "xxh3:tr")
	llmCall(db, "xxh3:r3", "withResponse", "xxh3:ws")
	llmCall(db, "xxh3:ev", "withPrompt", "xxh3:r3")
	llmCall(db, "xxh3:r4", "withResponse", "xxh3:ev")
	for dig, text := range map[string]string{
		"xxh3:p1": "first prompt",
		"xxh3:p2": "second prompt",
		"xxh3:ev": "alice is now idle",
	} {
		db.Calls[dig].Args = []*callpbv1.Argument{{
			Name:  "prompt",
			Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: text}},
		}}
	}

	message := func(id byte, name string, parent SpanID, role, digest string) SpanSnapshot {
		snap := messageSnapshot(id, name, parent, role)
		snap.LLMCallDigest = digest
		return snap
	}
	loop := messageSnapshot(bfLoop, "agent loop", SpanID{}, "")
	loop.Agent, loop.AgentID = true, "chief"
	event := message(bfEvent, "event", spanID(bfLoop), "user", "xxh3:ev")
	event.LLMOriginKind, event.LLMOriginAgentName = "EVENT", "alice"
	db.ImportSnapshots([]SpanSnapshot{
		loop,
		message(bfPrompt1, "prompt 1", spanID(bfLoop), "user", "xxh3:p1"),
		message(bfReply1, "reply 1", spanID(bfLoop), "assistant", "xxh3:r1"),
		message(bfLegacyReply1, "legacy reply 1", spanID(bfLoop), "assistant", "xxh3:p1"),
		message(bfPrompt2, "prompt 2", spanID(bfLoop), "user", "xxh3:p2"),
		message(bfTool, "Bash", spanID(bfLoop), "assistant", "xxh3:r2"),
		messageSnapshot(bfToolExec, "exec", spanID(bfTool), ""),
		message(bfLegacyTool, "legacy Bash", spanID(bfLoop), "assistant", "xxh3:p2"),
		message(bfReply3, "reply 3", spanID(bfLoop), "assistant", "xxh3:r3"),
		message(bfLegacyReply3, "legacy reply 3", spanID(bfLoop), "assistant", "xxh3:ws"),
		event,
		message(bfReply4, "reply 4", spanID(bfLoop), "assistant", "xxh3:r4"),
	})
	return &branchFixture{db: db}
}

func (f *branchFixture) branch(t *testing.T, id byte) string {
	t.Helper()
	digest, err := f.db.LLMBranchDigest(f.db.Spans.Map[spanID(id)])
	if err != nil {
		t.Fatalf("LLMBranchDigest(%d): %v", id, err)
	}
	return digest
}

// TestLLMBranchDigest pins where a branch from each kind of row goes, the
// way Pi's /tree and Claude Code's /rewind do: a message (the user's own, or
// an event another agent put on the record) goes back to just before it, so
// it can be edited and resubmitted; a reply keeps itself — never just the
// request it answered — and a reply that called tools keeps the results that
// settle it, so the branch is never a history with an unanswered tool call.
func TestLLMBranchDigest(t *testing.T) {
	f := newBranchFixture(t)
	for _, tc := range []struct {
		name string
		id   byte
		want string
	}{
		{"first prompt goes back to the seed", bfPrompt1, "xxh3:llm"},
		{"reply keeps itself", bfReply1, "xxh3:r1"},
		{"legacy reply resolves its response", bfLegacyReply1, "xxh3:r1"},
		{"prompt goes back to before itself", bfPrompt2, "xxh3:r1"},
		{"tool call keeps its results", bfTool, "xxh3:ws"},
		{"beneath a tool call", bfToolExec, "xxh3:ws"},
		{"legacy tool call keeps its results", bfLegacyTool, "xxh3:ws"},
		{"final reply", bfReply3, "xxh3:r3"},
		{"legacy final reply", bfLegacyReply3, "xxh3:r3"},
		{"an agent's event goes back to before itself", bfEvent, "xxh3:r3"},
		{"reply to the event", bfReply4, "xxh3:r4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := f.branch(t, tc.id); got != tc.want {
				t.Fatalf("LLMBranchDigest = %q, want %q", got, tc.want)
			}
		})
	}

	// A later, separate tool binding on a settled reply is not part of it.
	llmCall(f.db, "xxh3:r1-tools", "withTools", "xxh3:r1")
	if got := f.branch(t, bfReply1); got != "xxh3:r1" {
		t.Fatalf("LLMBranchDigest = %q, want the reply itself", got)
	}
}

// TestLLMMessageCallAndText covers what a branch from a message loads back
// into the input: the recorded text of exactly that message, including when
// several messages drained at one boundary share a digest.
func TestLLMMessageCallAndText(t *testing.T) {
	f := newBranchFixture(t)
	for id, want := range map[byte]string{
		bfPrompt1: "first prompt",
		bfPrompt2: "second prompt",
		bfEvent:   "alice is now idle",
	} {
		if got := LLMMessageText(f.db.LLMMessageCall(f.db.Spans.Map[spanID(id)])); got != want {
			t.Errorf("message %d text = %q, want %q", id, got, want)
		}
	}
	if f.db.LLMMessageCall(f.db.Spans.Map[spanID(bfReply1)]) != nil {
		t.Error("a reply is not a message")
	}

	// Two messages drained at one step boundary both carry the request
	// digest (the second's); start order maps them onto the chain.
	db := NewDB()
	llmCall(db, "xxh3:llm", "llm", "")
	llmCall(db, "xxh3:a", "withPrompt", "xxh3:llm")
	llmCall(db, "xxh3:b", "withContent", "xxh3:a")
	db.Calls["xxh3:a"].Args = []*callpbv1.Argument{{
		Name: "prompt", Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: "first"}},
	}}
	textBlock := func(kind, text string) *callpbv1.Literal {
		return &callpbv1.Literal{Value: &callpbv1.Literal_Object{Object: &callpbv1.Object{Values: []*callpbv1.Argument{
			{Name: "kind", Value: &callpbv1.Literal{Value: &callpbv1.Literal_Enum{Enum: kind}}},
			{Name: "text", Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: text}}},
		}}}}
	}
	db.Calls["xxh3:b"].Args = []*callpbv1.Argument{{
		Name: "content", Value: &callpbv1.Literal{Value: &callpbv1.Literal_List{List: &callpbv1.List{Values: []*callpbv1.Literal{
			textBlock("TEXT", "look at this"),
			textBlock("IMAGE", ""),
		}}}},
	}}
	first := messageSnapshot(1, "first", SpanID{}, "user")
	first.LLMCallDigest = "xxh3:b"
	second := messageSnapshot(2, "second", SpanID{}, "user")
	second.LLMCallDigest = "xxh3:b"
	db.ImportSnapshots([]SpanSnapshot{first, second})
	if got := db.LLMMessageCall(db.Spans.Map[spanID(1)]); got == nil || got.Digest != "xxh3:a" {
		t.Fatalf("first peer call = %v, want xxh3:a", got)
	}
	if got := LLMMessageText(db.LLMMessageCall(db.Spans.Map[spanID(2)])); got != "look at this" {
		t.Fatalf("content message text = %q", got)
	}
	if got, err := db.LLMBranchDigest(db.Spans.Map[spanID(2)]); err != nil || got != "xxh3:a" {
		t.Fatalf("branch from the second peer = %q, %v; want xxh3:a", got, err)
	}
}

// TestLLMBranchDigestRefusesToGuess covers the honest failures: a legacy
// reply whose request was answered more than once (it was branched from and
// re-run), and one whose response never reached this client.
func TestLLMBranchDigestRefusesToGuess(t *testing.T) {
	f := newBranchFixture(t)
	llmCall(f.db, "xxh3:r1-again", "withResponse", "xxh3:p1")
	_, err := f.db.LLMBranchDigest(f.db.Spans.Map[spanID(bfLegacyReply1)])
	if err == nil || !strings.Contains(err.Error(), "2 recorded responses") {
		t.Fatalf("ambiguous legacy reply: err = %v", err)
	}
	// A stamped reply is exact regardless.
	if got := f.branch(t, bfReply1); got != "xxh3:r1" {
		t.Fatalf("LLMBranchDigest = %q, want xxh3:r1", got)
	}

	delete(f.db.Calls, "xxh3:r3")
	if _, err := f.db.LLMBranchDigest(f.db.Spans.Map[spanID(bfLegacyReply3)]); err == nil {
		t.Fatal("a reply whose response never arrived must not branch to its request")
	}

	delete(f.db.Calls, "xxh3:p2")
	if _, err := f.db.LLMBranchDigest(f.db.Spans.Map[spanID(bfPrompt2)]); err == nil {
		t.Fatal("a message whose call never arrived has no known state before it")
	}

	if _, err := f.db.LLMBranchDigest(f.db.Spans.Map[spanID(bfLoop)]); err == nil {
		t.Fatal("a span with no LLM call has nothing to branch to")
	}
}

// TestRewindsAgreeWithBranchTargets joins the two halves: the rewind marker
// the engine publishes when it reseeds to a row's branch target must mark
// exactly what the branch dropped — for a message, the message itself and
// everything after it (its text goes back to the editor); for a reply,
// everything after it — whether the spans are stamped or legacy.
func TestRewindsAgreeWithBranchTargets(t *testing.T) {
	const markerID byte = 100
	check := func(t *testing.T, from byte, wantAbandoned ...string) {
		t.Helper()
		f := newBranchFixture(t)
		to := f.branch(t, from)
		marker := messageSnapshot(markerID, "conversation rewound", spanID(bfLoop), "user")
		marker.AgentRewindFrom, marker.AgentRewindTo = "xxh3:r4", to
		f.db.ImportSnapshots([]SpanSnapshot{marker})
		abandoned := map[string]bool{}
		for span := range f.db.Spans.Iter() {
			if span.LLMRole == "" || span.AgentRewindMarker() {
				continue
			}
			if f.db.SupersededBy(span) != nil {
				abandoned[span.Name] = true
			}
		}
		want := map[string]bool{}
		for _, name := range wantAbandoned {
			want[name] = true
			if !abandoned[name] {
				t.Errorf("branch from %d (to %s): %q should be abandoned", from, to, name)
			}
		}
		for name := range abandoned {
			if !want[name] {
				t.Errorf("branch from %d (to %s): %q must be kept", from, to, name)
			}
		}
	}
	later := []string{"event", "reply 4"}

	t.Run("branch from a reply keeps it", func(t *testing.T) {
		check(t, bfReply1, append([]string{"prompt 2", "Bash", "legacy Bash", "reply 3", "legacy reply 3"}, later...)...)
	})
	t.Run("branch from a prompt abandons it", func(t *testing.T) {
		check(t, bfPrompt2, append([]string{"prompt 2", "Bash", "legacy Bash", "reply 3", "legacy reply 3"}, later...)...)
	})
	t.Run("branch from the first prompt abandons everything", func(t *testing.T) {
		check(t, bfPrompt1, append([]string{"prompt 1", "reply 1", "legacy reply 1", "prompt 2", "Bash", "legacy Bash", "reply 3", "legacy reply 3"}, later...)...)
	})
	t.Run("branch from a tool call keeps its results", func(t *testing.T) {
		check(t, bfTool, append([]string{"reply 3", "legacy reply 3"}, later...)...)
	})
	t.Run("branch from an agent's event abandons it", func(t *testing.T) {
		check(t, bfEvent, later...)
	})
}
