package idtui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/stretchr/testify/require"
)

func TestConsoleHeadFromSteps(t *testing.T) {
	text := func(s string) *string { return &s }
	moved := "Committed abc\n---\nWorkspace moved: github.com/a/b @" + salvageBase + " -> @" + salvageHead + ". The new checkout is 1 commit(s) ahead."
	replaced := "Checked out.\n---\nWorkspace replaced: github.com/a/b @" + salvageHead + " [git-ref://" + salvageHead + "] -> github.com/c/d @" + strings.Repeat("e", 40) + " (main) [git-ref://" + strings.Repeat("e", 40) + "]. Files were not diffed."
	steps := []consoleSalvageStep{
		{Seq: 1, Tool: "committer_commit", Result: text(moved)},
		{Seq: 2, Tool: "editor_edit", Result: text("a.go +1")},
		{Seq: 3, Tool: "checkout", Result: text(replaced)},
		{Seq: 4, Tool: "committer_cherryPick", Result: text(moved), Errored: true},
		{Seq: 5, Tool: "editor_edit", Result: text("b.go +1")},
		{Seq: 6, Tool: "editor_write"},
		{Seq: 7, Tool: "editor_edit", Result: text("c.go +1")},
	}
	head := consoleHeadFromSteps(steps)
	require.Equal(t, strings.Repeat("e", 40), head.Commit, "an errored step's trailer doesn't count")
	require.Equal(t, "github.com/c/d", head.Repo, "a replaced workspace names its new repository")
	require.Equal(t, 3, head.Step)
	require.Equal(t, "checkout", head.Tool)
	require.Equal(t, 2, head.Later, "changes with a result after the head; the one without is left out")

	head = consoleHeadFromSteps(steps[:2])
	require.Equal(t, salvageHead, head.Commit)
	require.Equal(t, "github.com/a/b", head.Repo)
	require.Equal(t, 1, head.Later)

	require.Empty(t, consoleHeadFromSteps(steps[1:2]).Commit)
}

func TestConsoleLocateHead(t *testing.T) {
	db := dagui.NewDB()
	db.Calls = salvageHeadCalls()

	head := consoleHead{Commit: salvageHead}
	locateConsoleHead(db, &head)
	require.True(t, head.Recoverable)
	require.Equal(t, "xxh3:clean", head.Digest)
	require.Equal(t, []string{"https://github.com/dagger/dagger"}, head.Fetches)

	// Without the clean chain, the host one is all there is: it rebuilds, but
	// nothing else could evaluate it the same way.
	delete(db.Calls, "xxh3:clean")
	head = consoleHead{Commit: salvageHead}
	locateConsoleHead(db, &head)
	require.False(t, head.Recoverable)
	require.Equal(t, "xxh3:fromhost", head.Digest)
	require.Equal(t, "GitRepository.ref", head.Field, "named for its receiver's type")
	require.Equal(t, []string{"reads the client's host (Query.host)"}, head.Problems)

	// A chain whose frame never arrived doesn't rebuild at all.
	delete(db.Calls, "xxh3:host")
	head = consoleHead{Commit: salvageHead}
	locateConsoleHead(db, &head)
	require.False(t, head.Recoverable)
	require.Empty(t, head.Digest)
	require.Len(t, head.Problems, 1)
	require.Contains(t, head.Problems[0], "never reached this client")

	head = consoleHead{Commit: strings.Repeat("f", 40)}
	locateConsoleHead(db, &head)
	require.Equal(t, []string{"no GitRef call for this commit reached this client"}, head.Problems)
}

func TestConsoleRecipeHazards(t *testing.T) {
	recipe := &callpbv1.RecipeDAG{CallsByDigest: map[string]*callpbv1.Call{
		"a": {Field: "setSecret"},
		"b": {Field: "currentWorkspace"},
		"c": {Field: "withPrompt", ReceiverDigest: "x", Type: &callpbv1.Type{NamedType: "LLM"}},
		"d": {Field: "withPrompt", ReceiverDigest: "c", Type: &callpbv1.Type{NamedType: "LLM"}},
		"e": {Field: "withExec", ReceiverDigest: "x"},
		"f": {Field: "build", ReceiverDigest: "x", Module: &callpbv1.Module{Name: "go"}},
		"g": {Field: "git", Args: []*callpbv1.Argument{salvageArg("url", salvageStr("https://example.com/r"))}},
	}}
	problems, fetches, notes := consoleRecipeHazards(recipe)
	require.ElementsMatch(t, []string{
		"uses a secret (Query.setSecret), which no recipe carries",
		"reads the client's workspace (Query.currentWorkspace)",
		"replays model calls (LLM), which won't produce the same result, 2 calls",
	}, problems)
	require.Equal(t, []string{"https://example.com/r"}, fetches)
	require.Equal(t, []string{"runs functions from modules: go", "runs 1 container execs"}, notes)
}

func TestConsoleHeadEndpoints(t *testing.T) {
	_, inspector := salvageFrontend(t)
	request := func(handler http.HandlerFunc, target string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest(http.MethodGet, target, nil))
		return w
	}

	w := request(inspector.heads, "/heads")
	require.Equal(t, 200, w.Code)
	require.Empty(t, w.Body.String(), "no agent looked at yet")

	require.Equal(t, 400, request(inspector.head, "/head").Code)
	w = request(inspector.head, "/head?agent=worker")
	require.Equal(t, 200, w.Code, w.Body.String())
	var head consoleHead
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &head))
	require.Equal(t, "worker", head.Agent)
	require.Equal(t, salvageHead, head.Commit)
	require.Equal(t, "github.com/dagger/dagger", head.Repo)
	require.Equal(t, 4, head.Step)
	require.Equal(t, "xxh3:clean", head.Digest)
	require.True(t, head.Recoverable)

	w = request(inspector.head, "/head?agent=worker&format=id")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	var id call.ID
	require.NoError(t, id.Decode(w.Body.String()))
	require.Equal(t, "__resolvedRef", id.Field())
	require.Equal(t, "xxh3:clean", id.Digest().String())

	w = request(inspector.heads, "/heads")
	require.Equal(t, "worker\t"+salvageHead+"\tyes\tworker\n", w.Body.String())
}
