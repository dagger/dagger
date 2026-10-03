package dagui

import (
	"strings"
	"testing"
)

// branchFixture is one agent's transcript over the chain
//
//	llm -> withPrompt(p1) -> withResponse(r1, text) -> withPrompt(p2) ->
//	withResponse(r2, tool call) -> withToolResult(tr) -> withWorkspace(ws) ->
//	withResponse(r3, text)
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

	message := func(id byte, name string, parent SpanID, role, digest string) SpanSnapshot {
		snap := messageSnapshot(id, name, parent, role)
		snap.LLMCallDigest = digest
		return snap
	}
	loop := messageSnapshot(bfLoop, "agent loop", SpanID{}, "")
	loop.Agent, loop.AgentID = true, "chief"
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

// TestLLMBranchDigest pins what a branch from each kind of row keeps: a
// prompt keeps itself (and is answered again), a reply keeps itself — never
// just the request it answered — and a reply that called tools keeps the
// results that settle it, so the branch is never a history with an unanswered
// tool call.
func TestLLMBranchDigest(t *testing.T) {
	f := newBranchFixture(t)
	for _, tc := range []struct {
		name string
		id   byte
		want string
	}{
		{"prompt keeps itself", bfPrompt1, "xxh3:p1"},
		{"reply keeps itself", bfReply1, "xxh3:r1"},
		{"legacy reply resolves its response", bfLegacyReply1, "xxh3:r1"},
		{"prompt after a reply", bfPrompt2, "xxh3:p2"},
		{"tool call keeps its results", bfTool, "xxh3:ws"},
		{"beneath a tool call", bfToolExec, "xxh3:ws"},
		{"legacy tool call keeps its results", bfLegacyTool, "xxh3:ws"},
		{"final reply", bfReply3, "xxh3:r3"},
		{"legacy final reply", bfLegacyReply3, "xxh3:r3"},
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

	if _, err := f.db.LLMBranchDigest(f.db.Spans.Map[spanID(bfLoop)]); err == nil {
		t.Fatal("a span with no LLM call has nothing to branch to")
	}
}

// TestRewindsAgreeWithBranchTargets joins the two halves: the rewind marker
// the engine publishes when it reseeds to a branch target must mark exactly
// the rows after the branched-from row as abandoned — the row itself (and
// everything before it) stays, whether its span is stamped or legacy.
func TestRewindsAgreeWithBranchTargets(t *testing.T) {
	const markerID byte = 100
	rewindTo := func(t *testing.T, to string) map[string]bool {
		t.Helper()
		f := newBranchFixture(t)
		marker := messageSnapshot(markerID, "conversation rewound", spanID(bfLoop), "user")
		marker.AgentRewindFrom, marker.AgentRewindTo = "xxh3:r3", to
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
		return abandoned
	}
	check := func(t *testing.T, to string, wantAbandoned ...string) {
		t.Helper()
		abandoned := rewindTo(t, to)
		want := map[string]bool{}
		for _, name := range wantAbandoned {
			want[name] = true
			if !abandoned[name] {
				t.Errorf("rewind to %s: %q should be abandoned", to, name)
			}
		}
		for name := range abandoned {
			if !want[name] {
				t.Errorf("rewind to %s: %q must be kept", to, name)
			}
		}
	}

	t.Run("branch from a reply keeps it", func(t *testing.T) {
		// The repro: branching from reply 1 used to reseed to p1, dropping
		// the reply while the transcript claimed it was kept.
		check(t, "xxh3:r1", "prompt 2", "Bash", "legacy Bash", "reply 3", "legacy reply 3")
	})
	t.Run("branch from a prompt re-answers it", func(t *testing.T) {
		// Reseeding to p2 keeps the prompt and re-runs it, so its old
		// answer — stamped or legacy — is gone.
		check(t, "xxh3:p2", "Bash", "legacy Bash", "reply 3", "legacy reply 3")
	})
	t.Run("branch from a tool call keeps its results", func(t *testing.T) {
		check(t, "xxh3:ws", "reply 3", "legacy reply 3")
	})
}
