package idtui

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/stretchr/testify/require"
)

func salvageStr(s string) *callpbv1.Literal {
	return &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: s}}
}

func salvageArg(name string, value *callpbv1.Literal) *callpbv1.Argument {
	return &callpbv1.Argument{Name: name, Value: value}
}

// salvageContent builds withResponse content: kind/text/toolName/callId/
// arguments objects, the way recorded checkpoints carry them.
func salvageContent(blocks ...consoleTranscriptBlock) *callpbv1.Literal {
	list := &callpbv1.List{}
	for _, block := range blocks {
		obj := &callpbv1.Object{Values: []*callpbv1.Argument{
			salvageArg("kind", &callpbv1.Literal{Value: &callpbv1.Literal_Enum{Enum: block.Kind}}),
			salvageArg("text", salvageStr(block.Text)),
			salvageArg("toolName", salvageStr(block.ToolName)),
			salvageArg("callId", salvageStr(block.CallID)),
			salvageArg("arguments", salvageStr(block.Arguments)),
		}}
		list.Values = append(list.Values, &callpbv1.Literal{Value: &callpbv1.Literal_Object{Object: obj}})
	}
	return &callpbv1.Literal{Value: &callpbv1.Literal_List{List: list}}
}

const salvageEditPatch = "diff --git a/a.go b/a.go\nindex 1111111..2222222 100644\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-x\n+y\n"

// salvageCalls is an agent's checkpoint spine that crossed a module tool
// returning the conversation in a new workspace (as checkout does), whose
// receiver never reached the client.
func salvageCalls() map[string]*callpbv1.Call {
	calls := map[string]*callpbv1.Call{}
	recv := ""
	add := func(dig, field string, args ...*callpbv1.Argument) {
		calls[dig] = &callpbv1.Call{
			Digest: dig, ReceiverDigest: recv, Field: field,
			Type: &callpbv1.Type{NamedType: "LLM", NonNull: true}, Args: args,
		}
		recv = dig
	}
	toolCall := func(name, id, args string) consoleTranscriptBlock {
		return consoleTranscriptBlock{Kind: "TOOL_CALL", ToolName: name, CallID: id, Arguments: args}
	}
	result := func(dig, id, text string, errored bool) {
		add(dig, "withToolResult", salvageArg("callId", salvageStr(id)), salvageArg("content", salvageStr(text)),
			salvageArg("errored", &callpbv1.Literal{Value: &callpbv1.Literal_Bool{Bool: errored}}))
	}
	add("xxh3:d1", "llm")
	add("xxh3:d2", "withPrompt", salvageArg("prompt", salvageStr("fix the lag")))
	add("xxh3:d3", "withResponse", salvageArg("content", salvageContent(
		consoleTranscriptBlock{Kind: "TEXT", Text: "Checking out main."},
		toolCall("checkout", "c1", `{"repo": "https://github.com/dagger/dagger#main"}`),
	)))
	recv = "xxh3:host-never-arrived"
	add("xxh3:d4", "checkout",
		salvageArg("llm", &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: "xxh3:d3"}}),
		salvageArg("repo", &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: "xxh3:repo"}}))
	calls["xxh3:d4"].Module = &callpbv1.Module{Name: "github"}
	result("xxh3:d5", "c1", "Checked out main.", false)
	add("xxh3:d6", "withResponse", salvageArg("content", salvageContent(
		toolCall("editor_edit", "c2", `{"filePath": "a.go", "oldText": "x", "newText": "y"}`),
		toolCall("editor_read", "c3", `{"filePath": "a.go"}`),
		toolCall("editor_write", "c4", `{"filePath": "b.go", "contents": "package b\n"}`),
	)))
	result("xxh3:d7", "c2", salvageEditPatch, false)
	result("xxh3:d8", "c3", "1→ y", false)
	result("xxh3:d9", "c4", "b.go +1", false)
	add("xxh3:d10", "withResponse", salvageArg("content", salvageContent(
		toolCall("committer_commit", "c5", `{"message": "idtui: fix lag\n\nbody"}`),
		toolCall("editor_edit", "c6", `{"filePath": "a.go", "oldText": "nope", "newText": "z"}`),
	)))
	result("xxh3:d11", "c5", "Committed abc1234\n---\nWorkspace moved.", false)
	result("xxh3:d12", "c6", "oldText not found", true)
	return calls
}

func TestConsolePartialCheckpoint(t *testing.T) {
	db := dagui.NewDB()
	db.Calls = salvageCalls()
	agent := consoleAgent{ID: "worker", State: "IDLE", SnapshotDigest: "xxh3:d12"}

	_, err := decodeConsoleCheckpoint(db, agent)
	require.ErrorContains(t, err, "not a core LLM selector", "strict decoding still refuses the module step")

	snap, err := decodeLoadedCheckpoint(db, agent)
	require.NoError(t, err)
	require.Equal(t, "loaded-telemetry", snap.Source)
	msgs := snap.Snapshot.Messages
	require.Len(t, msgs, 11)
	require.Equal(t, "fix the lag", msgs[0].Content[0].Text, "crossed the checkout through its llm argument")
	require.Equal(t, consoleGapRole, msgs[2].Role)
	require.Contains(t, msgs[2].Content[0].Text, "skipped github.checkout (xxh3:d4)")
	require.Equal(t, []string{msgs[2].Content[0].Text}, snap.Gaps)
	for i, msg := range msgs {
		require.Equal(t, i, msg.Index)
	}

	// A frame that never arrived ends the walk with an explicit gap rather
	// than an error, keeping every later message.
	delete(db.Calls, "xxh3:d2")
	snap, err = decodeLoadedCheckpoint(db, agent)
	require.NoError(t, err)
	require.Len(t, snap.Gaps, 2)
	require.Equal(t, consoleGapRole, snap.Snapshot.Messages[0].Role)
	require.Contains(t, snap.Gaps[0], "frame xxh3:d2 never reached this client")
	require.Equal(t, "Checking out main.", snap.Snapshot.Messages[1].Content[0].Text)

	// Nothing recoverable is still an error.
	_, err = decodeLoadedCheckpoint(db, consoleAgent{ID: "gone", SnapshotDigest: "xxh3:missing"})
	require.ErrorContains(t, err, "no messages recoverable")
}

func TestConsoleInferredCheckpoint(t *testing.T) {
	db := dagui.NewDB()
	db.Calls = salvageCalls()
	llmFrame := func(dig, recv, field string) {
		db.Calls[dig] = &callpbv1.Call{
			Digest: dig, ReceiverDigest: recv, Field: field,
			Type: &callpbv1.Type{NamedType: "LLM"}, Args: []*callpbv1.Argument{salvageArg("prompt", salvageStr(dig))},
		}
	}
	// A session-title side chain branching off the conversation, started
	// last: recency would pick it, spine length must not, and it must not
	// count as a continuation either.
	llmFrame("xxh3:title0", "xxh3:d12", "withoutMessageHistory")
	llmFrame("xxh3:title1", "xxh3:title0", "withPrompt")
	llmFrame("xxh3:title2", "xxh3:title1", "withPrompt")
	// A nested agent's own, longer conversation is not the chief's.
	llmFrame("xxh3:w0", "", "llm")
	prev := "xxh3:w0"
	for i := range 20 {
		dig := "xxh3:w" + string(rune('a'+i))
		llmFrame(dig, prev, "withPrompt")
		prev = dig
	}
	loop, worker := prettyTestSpanID(1), prettyTestSpanID(2)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: loop, Agent: true, AgentID: "chief", AgentName: "chief"},
		// Only an early step's span has loaded; later steps are only calls.
		{ID: prettyTestSpanID(3), ParentID: loop, CallDigest: "xxh3:d7"},
		{ID: prettyTestSpanID(4), ParentID: loop, CallDigest: "xxh3:title2"},
		{ID: worker, ParentID: loop, Agent: true, AgentID: "worker", AgentName: "worker"},
		{ID: prettyTestSpanID(5), ParentID: worker, CallDigest: "xxh3:wc"},
	})
	require.Equal(t, "xxh3:d12", inferCheckpointDigest(db, "chief"), "extended forward past the loaded spans")
	require.Equal(t, "xxh3:wt", inferCheckpointDigest(db, "worker"))
	require.Empty(t, inferCheckpointDigest(db, "nobody"))

	snap, err := decodeLoadedCheckpoint(db, consoleAgent{ID: "chief"})
	require.NoError(t, err)
	require.Equal(t, "xxh3:d12", snap.Digest)
	require.Len(t, snap.Gaps, 2)
	last := snap.Snapshot.Messages[len(snap.Snapshot.Messages)-1]
	require.Equal(t, consoleGapRole, last.Role)
	require.Contains(t, last.Content[0].Text, "inferred xxh3:d12")
}

func TestConsoleLoadedFallback(t *testing.T) {
	loadedSnap := consoleAgentSnapshot{Source: "loaded-telemetry", Digest: "xxh3:d12"}
	loadedSnap.Snapshot.Messages = []consoleTranscriptMessage{{Role: "USER"}}
	loaded := func(context.Context, string, string) (consoleAgentSnapshot, error) { return loadedSnap, nil }
	noLoaded := func(context.Context, string, string) (consoleAgentSnapshot, error) {
		return consoleAgentSnapshot{}, errors.New("nothing loaded")
	}
	empty := consoleAgentSnapshot{}
	empty.Snapshot.ID = "restored-but-empty"
	full := empty
	full.Snapshot.Messages = []consoleTranscriptMessage{{Role: "USER"}, {Role: "ASSISTANT"}}
	runtime := func(snap consoleAgentSnapshot, err error) consoleSnapshotReader {
		return func(context.Context, string, string) (consoleAgentSnapshot, error) { return snap, err }
	}
	ctx := context.Background()

	got, err := withLoadedFallback(runtime(full, nil), loaded)(ctx, "a", "a")
	require.NoError(t, err)
	require.Len(t, got.Snapshot.Messages, 2, "a complete runtime snapshot wins")

	got, err = withLoadedFallback(runtime(empty, nil), loaded)(ctx, "a", "a")
	require.NoError(t, err)
	require.Equal(t, "loaded-telemetry", got.Source)
	require.Contains(t, got.Warning, "no messages")

	got, err = withLoadedFallback(runtime(empty, io.EOF), loaded)(ctx, "a", "a")
	require.NoError(t, err)
	require.Contains(t, got.Warning, "EOF")

	_, err = withLoadedFallback(runtime(empty, io.EOF), noLoaded)(ctx, "a", "a")
	require.ErrorIs(t, err, io.EOF, "without a fallback, the runtime error stands")
	got, err = withLoadedFallback(runtime(empty, nil), noLoaded)(ctx, "a", "a")
	require.NoError(t, err)
	require.Equal(t, "restored-but-empty", got.Snapshot.ID)

	_, err = withLoadedFallback(nil, noLoaded)(ctx, "a", "a")
	require.ErrorContains(t, err, "no engine session")
	got, err = withLoadedFallback(nil, loaded)(ctx, "a", "a")
	require.NoError(t, err)
	require.Empty(t, got.Warning)
}

// salvageFrontend renders a trace whose full download fails, with the
// agent's spine already ingested for rendering.
func salvageFrontend(t *testing.T) (*frontendPretty, *consoleTraceInspector) {
	t.Helper()
	db := dagui.NewDB()
	db.ImportSnapshots([]dagui.SpanSnapshot{{ID: prettyTestSpanID(9), Agent: true, AgentID: "worker", AgentName: "worker"}})
	publishAgentControl(t, db, "worker", func(a *agentcontrol.Agent) {
		a.State, a.Digest = "RUNNING", "xxh3:d12"
	})
	for dig, frame := range salvageCalls() {
		db.Calls[dig] = frame
	}
	fe := NewWithDB(io.Discard, db)
	fe.traceID = "broken"
	return fe, &consoleTraceInspector{frontend: fe, fetch: func(context.Context, string, *dagui.DB) error {
		return errors.New("live telemetry payload is 128901045 bytes (maximum 67108864)")
	}}
}

func TestConsoleTraceFallsBackToLoadedTelemetry(t *testing.T) {
	_, inspector := salvageFrontend(t)

	w := httptest.NewRecorder()
	inspector.agents(w, httptest.NewRequest(http.MethodGet, "/agents", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	var roster struct {
		Agents  []consoleAgent
		Source  string
		Warning string
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &roster))
	require.Equal(t, "loaded-telemetry", roster.Source)
	require.Contains(t, roster.Warning, "128901045")
	require.Len(t, roster.Agents, 1)

	w = httptest.NewRecorder()
	inspector.transcript(w, httptest.NewRequest(http.MethodGet, "/transcript?agent=worker&limit=100", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	var page struct {
		Source   string
		Partial  bool
		Gaps     []string
		Warning  string
		Total    int
		Messages []consoleTranscriptMessage
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Equal(t, "loaded-telemetry", page.Source)
	require.True(t, page.Partial)
	require.Len(t, page.Gaps, 1)
	require.Contains(t, page.Warning, "128901045")
	require.Equal(t, 11, page.Total)

	w = httptest.NewRecorder()
	inspector.transcript(w, httptest.NewRequest(http.MethodGet, "/transcript?agent=worker&role=gap", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Equal(t, 1, page.Total)
	require.Equal(t, 2, page.Messages[0].Index)
}

func TestConsoleCall(t *testing.T) {
	fe, _ := salvageFrontend(t)
	request := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		fe.consoleCallHandler(w, httptest.NewRequest(http.MethodGet, "/call"+query, nil))
		return w
	}
	require.Equal(t, 400, request("").Code)
	w := request("?dig=xxh3:missing")
	require.Equal(t, 404, w.Code)
	require.Contains(t, w.Body.String(), "never reached this client")

	w = request("?dig=xxh3:d4")
	require.Equal(t, 200, w.Code, w.Body.String())
	var call consoleCall
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &call))
	require.Equal(t, "checkout", call.Field)
	require.Equal(t, "LLM!", call.Type)
	require.Equal(t, "github", call.Module)
	require.Equal(t, "xxh3:host-never-arrived", call.Receiver)
	require.Equal(t, map[string]any{"call": "xxh3:d3"}, call.Args[0].Value)

	w = request("?dig=xxh3:d7&arg=content")
	require.Equal(t, 200, w.Code)
	require.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	require.Equal(t, salvageEditPatch, w.Body.String(), "string arguments come back raw and whole")
	w = request("?dig=xxh3:d7&arg=errored")
	require.Equal(t, "false\n", w.Body.String())
	w = request("?dig=xxh3:d7&arg=nope")
	require.Equal(t, 404, w.Code)
	require.Contains(t, w.Body.String(), "has: callId, content, errored")
}

func TestConsoleSalvage(t *testing.T) {
	_, inspector := salvageFrontend(t)
	w := httptest.NewRecorder()
	inspector.salvage(w, httptest.NewRequest(http.MethodGet, "/salvage", nil))
	require.Equal(t, 400, w.Code)
	w = httptest.NewRecorder()
	inspector.salvage(w, httptest.NewRequest(http.MethodGet, "/salvage?agent=nobody", nil))
	require.Equal(t, 404, w.Code)

	w = httptest.NewRecorder()
	inspector.salvage(w, httptest.NewRequest(http.MethodGet, "/salvage?agent=worker", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "application/x-tar", w.Header().Get("Content-Type"))
	files := map[string]string{}
	var names []string
	tr := tar.NewReader(bytes.NewReader(w.Body.Bytes()))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		names = append(names, hdr.Name)
		files[hdr.Name] = string(body)
	}
	require.Equal(t, []string{
		"index.md", "transcript.md",
		"steps/0001-checkout.json",
		"steps/0002-editor_edit.json", "steps/0002-editor_edit.patch",
		"steps/0003-editor_write.json",
		"steps/0004-committer_commit.json",
		"steps/0005-editor_edit.json",
	}, names, "reads are left out; the edit's diff becomes a patch")

	require.Equal(t, salvageEditPatch, files["steps/0002-editor_edit.patch"])
	var write consoleSalvageStep
	require.NoError(t, json.Unmarshal([]byte(files["steps/0003-editor_write.json"]), &write))
	require.Equal(t, "editor_write", write.Tool)
	var args map[string]string
	require.NoError(t, json.Unmarshal(write.Arguments, &args))
	require.Equal(t, "package b\n", args["contents"])
	require.Equal(t, "b.go +1", *write.Result)

	index := files["index.md"]
	for _, want := range []string{
		"- source: loaded-telemetry",
		"- warning: live telemetry payload is 128901045 bytes",
		"## Gaps (1)",
		"- 0001 [message 1] checkout https://github.com/dagger/dagger#main\n",
		"- 0002 [message 4] editor_edit a.go (patch)",
		"- 0003 [message 4] editor_write b.go\n",
		`- 0004 [message 8] committer_commit "idtui: fix lag"`,
		"- 0005 [message 8] editor_edit a.go ERRORED",
	} {
		require.Contains(t, index, want)
	}
	transcript := files["transcript.md"]
	for _, want := range []string{
		"## [0] USER\n\nfix the lag\n",
		"## [2] GAP\n",
		"### tool call: editor_write (c4)\n\n```json\n{\n  \"filePath\": \"b.go\",\n  \"contents\": \"package b\\n\"\n}\n```\n",
		"### tool result (c6) ERRORED\n",
	} {
		require.Contains(t, transcript, want)
	}
}

func TestConsoleResultPatch(t *testing.T) {
	require.Empty(t, consoleResultPatch("Committed abc1234"))
	require.Equal(t, salvageEditPatch, consoleResultPatch("Restoring files.\n\n"+salvageEditPatch+"---\na.go +1 -1\n"))
	require.Equal(t, "````\nhas ``` inside\n````\n", consoleFenced("", "has ``` inside"))
	require.True(t, strings.HasPrefix(consoleFenced("json", "{}"), "```json\n"))
}
