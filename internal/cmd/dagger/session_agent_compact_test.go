package daggercmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/dagql/idtui"
	"github.com/stretchr/testify/require"
)

// Compaction summaries, branch summaries, and the tool results of a reply a
// branch kept leave a conversation ending in input the model has not
// answered. The engine holds such input to lead the next message; resume is a
// no-op on it. These tests pin the CLI's half: like Pi's /tree and Claude
// Code's /rewind, /compact, auto-compaction and every kind of branch never
// resume or send on their own.

// chainQueryPath returns the field path of a single-chain GraphQL query as
// the SDK renders it (`query{node(id:"x"){withPrompt(...){model}}}` ->
// [node withPrompt model]), skipping arguments and string literals.
func chainQueryPath(q string) []string {
	var path []string
	var ident strings.Builder
	parens := 0
	flush := func() string {
		s := ident.String()
		ident.Reset()
		return s
	}
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case c == '"':
			// Skip the string literal, escapes included.
			for i++; i < len(q) && q[i] != '"'; i++ {
				if q[i] == '\\' {
					i++
				}
			}
		case parens > 0:
			switch c {
			case '(':
				parens++
			case ')':
				parens--
			}
		case c == '(':
			parens++
			if s := flush(); s != "" {
				path = append(path, s)
			}
		case c == '{' || c == '}' || c == ' ' || c == '\n':
			if s := flush(); s != "" {
				path = append(path, s)
			}
		default:
			ident.WriteByte(c)
		}
	}
	// Drop the operation keyword and name, and inline fragments' type
	// conditions: neither nests the response.
	var fields []string
	for i := 0; i < len(path); i++ {
		switch {
		case i < 2 && (path[i] == "query" || path[i] == "Query"):
		case path[i] == "..." && i+2 < len(path) && path[i+1] == "on":
			i += 2
		default:
			fields = append(fields, path[i])
		}
	}
	return fields
}

// chainDag is a dagger client answering single-chain queries with the value
// leaves maps the chain's final field to, nested along the chain. It records
// every query's path.
type chainDag struct {
	mu      sync.Mutex
	queries [][]string
}

func newChainDag(t *testing.T, leaves map[string]any) (*dagger.Client, *chainDag) {
	t.Helper()
	cd := &chainDag{}
	dag, err := dagger.Connect(t.Context(), dagger.WithConn(agentTestConn{do: func(req *http.Request) (*http.Response, error) {
		var query dagger.Request
		require.NoError(t, json.NewDecoder(req.Body).Decode(&query))
		path := chainQueryPath(query.Query)
		cd.mu.Lock()
		cd.queries = append(cd.queries, path)
		cd.mu.Unlock()
		require.NotEmpty(t, path, "query: %s", query.Query)
		leaf, ok := leaves[path[len(path)-1]]
		require.True(t, ok, "unexpected query: %s", query.Query)
		for i := len(path) - 1; i >= 0; i-- {
			leaf = map[string]any{path[i]: leaf}
		}
		body, err := json.Marshal(map[string]any{"data": leaf})
		require.NoError(t, err)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	}}))
	require.NoError(t, err)
	t.Cleanup(func() { dag.Close() })
	return dag, cd
}

// sawChain reports whether any recorded query's path contains all of the
// given fields, in order.
func (cd *chainDag) sawChain(fields ...string) bool {
	cd.mu.Lock()
	defer cd.mu.Unlock()
	for _, path := range cd.queries {
		i := 0
		for _, f := range path {
			if i < len(fields) && f == fields[i] {
				i++
			}
		}
		if i == len(fields) {
			return true
		}
	}
	return false
}

// idleAgent builds a session whose focused agent is idle with a live runtime,
// its conversation rooted on a chain client.
func idleAgent(t *testing.T, leaves map[string]any) (*sessionAgent, *fakeRuntime, *chainDag) {
	t.Helper()
	dag, cd := newChainDag(t, leaves)
	s, agents := testSession(t, "chief")
	s.dag = dag
	a := agents[0]
	a.llm = dagger.Ref[*dagger.LLM](dag, "chief-llm")
	rt := runtimeOf(t, a)
	rt.refuseRunningReseed = true
	rt.snapshot = "chief-snapshot"
	rt.setState(dagger.AgentStateIdle)
	return a, rt, cd
}

func TestCompactInPlaceDoesNotStartATurn(t *testing.T) {
	a, rt, cd := idleAgent(t, map[string]any{
		"lastReply": "the summary",
		"model":     "test-model",
	})
	require.NoError(t, a.CompactInPlace(t.Context()))
	require.Equal(t, []string{"reseed"}, rt.verbLog(),
		"/compact reseeds the summary and leaves it for the next prompt: no resume, no send")
	require.True(t, cd.sawChain("withoutMessageHistory", "withPrompt", "model"),
		"the summary note leads the compacted conversation")
}

func TestAutoCompactionSummaryLeadsThePrompt(t *testing.T) {
	a, rt, cd := idleAgent(t, map[string]any{
		"lastReply":     "the summary",
		"model":         "test-model",
		"contextTokens": 1_000_000,
		"contextWindow": 200_000,
	})
	a.autoCompact = true
	require.NoError(t, a.WithPrompt(t.Context(), "next prompt"))
	require.Equal(t, []string{"reseed", "send", "resume"}, rt.verbLog(),
		"the compacted conversation is reseeded before the prompt is sent, and only the send's own resume follows")
	require.Equal(t, []string{"next prompt"}, rt.sent)
	require.True(t, cd.sawChain("withoutMessageHistory", "withPrompt", "model"))
}

func TestBranchWithSummaryDoesNotStartATurn(t *testing.T) {
	a, rt, cd := idleAgent(t, map[string]any{
		"transcript":    "user: hi\nassistant: hello",
		"contextWindow": 200_000,
		"lastReply":     "what the abandoned branch explored",
		"model":         "test-model",
	})
	target := dagger.Ref[*dagger.LLM](a.session.dag, "branch-point")
	require.NoError(t, a.Branch(t.Context(), target, idtui.BranchSummary{Summarize: true}))
	require.Equal(t, []string{"reseed"}, rt.verbLog(),
		"a summarized branch holds the note for the next prompt")
	require.True(t, cd.sawChain("node", "withPrompt", "model"), "the summary note is appended to the branch target")
}

func TestPlainBranchDoesNotStartATurn(t *testing.T) {
	// A branch from a reply (whatever its target leaves pending is held by
	// the engine) and the inline-edit rewind a plain branch from a prompt
	// uses: neither resumes nor sends.
	a, rt, _ := idleAgent(t, map[string]any{"model": "test-model"})
	target := dagger.Ref[*dagger.LLM](a.session.dag, "after-a-reply")
	require.NoError(t, a.Branch(t.Context(), target, idtui.BranchSummary{}))
	require.Equal(t, []string{"reseed"}, rt.verbLog())

	a, rt, _ = idleAgent(t, map[string]any{"model": "test-model"})
	h := &shellCallHandler{dag: a.session.dag, mode: modeShell, llmSession: a.session}
	require.NoError(t, h.EditFromID(t.Context(), "before-a-prompt")())
	require.Equal(t, []string{"reseed"}, rt.verbLog())
	require.Equal(t, modePrompt, h.mode)
}

func TestBranchMidTurnLeavesTheAgentParked(t *testing.T) {
	// Mid-turn, the branch interrupts first (Claude's "Esc, then rewind")
	// and never resumes afterwards: the agent stays parked until the user's
	// next message.
	for _, summary := range []idtui.BranchSummary{{}, {Summarize: true}} {
		a, rt, _ := idleAgent(t, map[string]any{
			"transcript":    "user: hi\nassistant: hello",
			"contextWindow": 200_000,
			"lastReply":     "what the abandoned branch explored",
			"model":         "test-model",
		})
		rt.setState(dagger.AgentStateRunning)
		target := dagger.Ref[*dagger.LLM](a.session.dag, "branch-point")
		require.NoError(t, a.Branch(t.Context(), target, summary))
		require.Equal(t, []string{"interrupt", "reseed"}, rt.verbLog(), "summarize=%t", summary.Summarize)
	}
}
