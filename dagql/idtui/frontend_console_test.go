package idtui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"google.golang.org/protobuf/proto"
)

func TestConsoleAgents(t *testing.T) {
	db := dagui.NewDB()
	start := time.Unix(100, 0)
	chief, child, restored := prettyTestSpanID(1), prettyTestSpanID(2), prettyTestSpanID(3)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: chief, Name: "agent: chief", Agent: true, AgentID: "chief-id", AgentName: "chief", AgentState: "STOPPED", StartTime: start},
		{ID: child, ParentID: chief, Agent: true, AgentID: "worker-id", AgentName: "worker", AgentState: "RUNNING", StartTime: start.Add(time.Second)},
		{ID: restored, Agent: true, AgentID: "worker-id", AgentName: "worker", AgentState: "IDLE", AgentSnapshotDigest: "xxh3:snapshot", StartTime: start.Add(2 * time.Second)},
	})
	fe := NewWithDB(io.Discard, db)
	w := httptest.NewRecorder()
	fe.consoleAgentsHandler(w, httptest.NewRequest(http.MethodGet, "/agents", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var got struct {
		Agents                           []consoleAgent
		LoadedSpansOnly, EngineConnected bool
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.True(t, got.LoadedSpansOnly)
	require.False(t, got.EngineConnected)
	require.Len(t, got.Agents, 2)
	require.Equal(t, "worker-id", got.Agents[1].ID)
	require.Equal(t, "chief-id", got.Agents[1].ParentID)
	require.Equal(t, "IDLE", got.Agents[1].State)
	require.Equal(t, "xxh3:snapshot", got.Agents[1].SnapshotDigest)
	require.Equal(t, []string{child.String(), restored.String()}, got.Agents[1].SpanIDs)
}

func TestConsoleTranscript(t *testing.T) {
	db := dagui.NewDB()
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: prettyTestSpanID(1), Agent: true, AgentID: "chief-id", AgentName: "chief", AgentState: "IDLE"},
		{ID: prettyTestSpanID(2), Agent: true, AgentID: "worker-1", AgentName: "worker"},
		{ID: prettyTestSpanID(3), Agent: true, AgentID: "worker-2", AgentName: "worker"},
		// A display name that collides with another agent's handle must not
		// make that handle ambiguous.
		{ID: prettyTestSpanID(4), Agent: true, AgentID: "other", AgentName: "chief-id"},
	})
	fe := NewWithDB(io.Discard, db)
	fe.FocusedSpan = prettyTestSpanID(3)
	longText := strings.Repeat("<source> α & ?\n", 1000) + "\x1b[31mnot a terminal\x1b[0m"
	msg := func(role, text string) consoleTranscriptMessage {
		return consoleTranscriptMessage{Role: role, Content: []consoleTranscriptBlock{{Kind: "TEXT", Text: text}}}
	}
	var snap consoleAgentSnapshot
	snap.State = "IDLE"
	snap.Snapshot.ID = "snapshot-id"
	snap.Snapshot.Messages = []consoleTranscriptMessage{
		msg("SYSTEM", "system"), msg("USER", longText), msg("ASSISTANT", "answer"),
		{Role: "ASSISTANT", Content: []consoleTranscriptBlock{
			{Kind: "THINKING", Text: "considering"},
			{Kind: "TOOL_CALL", ToolName: "staff_spawn", CallID: "call-1", Arguments: `{"task":"restart a worker"}`},
		}},
		{Role: "USER", Content: []consoleTranscriptBlock{{Kind: "TOOL_RESULT", CallID: "call-1", Text: "result", Errored: true}}},
		msg("USER", "second prompt"),
	}
	snap.Snapshot.Messages[5].Origin = &consoleTranscriptOrigin{Kind: "AGENT", AgentName: "worker", Ref: "#3", ReplyTo: "#2"}
	calls := 0
	read := func(ctx context.Context, id, _ string) (consoleAgentSnapshot, error) { //nolint:unparam // Successful consoleSnapshotReader test double.
		calls++
		require.True(t, fe.consoleMu.TryLock(), "engine I/O held the console lock")
		fe.consoleMu.Unlock()
		require.Equal(t, "chief-id", id)
		_, bounded := ctx.Deadline()
		require.True(t, bounded)
		return snap, nil
	}
	request := func(query string, reader consoleSnapshotReader) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		fe.serveConsoleTranscript(w, httptest.NewRequest(http.MethodGet, "/transcript"+query, nil), reader)
		return w
	}
	for _, tc := range []struct {
		query string
		code  int
	}{
		{"", 400}, {"?agent=chief&role=oops", 400}, {"?agent=chief&offset=-1", 400},
		{"?agent=chief&offset=bad", 400}, {"?agent=chief&limit=0", 400},
		{"?agent=chief&limit=101", 400}, {"?agent=chief&limit=bad", 400},
		{"?agent=chief&grep=%5B", 400}, {"?agent=missing", 404}, {"?agent=worker", 409},
	} {
		t.Run(tc.query, func(t *testing.T) {
			require.Equal(t, tc.code, request(tc.query, read).Code)
		})
	}
	require.Zero(t, calls, "invalid or ambiguous requests contacted the engine")
	require.Equal(t, 409, request("?agent=chief", nil).Code)

	var page struct {
		Agent                consoleAgent
		SnapshotID           string
		Total, Offset, Limit int
		HasMore              bool
		Messages             []consoleTranscriptMessage
	}
	decode := func(query string) {
		w := request(query, read)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	}
	decode("?agent=chief-id&limit=1")
	require.Equal(t, 5, page.Total)
	require.True(t, page.HasMore)
	require.Equal(t, 1, page.Messages[0].Index)
	require.Equal(t, longText, page.Messages[0].Content[0].Text)
	require.Equal(t, "snapshot-id", page.SnapshotID)
	require.Equal(t, "IDLE", page.Agent.State)
	decode("?agent=chief&role=user&offset=1&limit=1")
	require.Equal(t, 3, page.Total)
	require.Equal(t, 4, page.Messages[0].Index)
	require.True(t, page.Messages[0].Content[0].Errored)
	decode("?agent=chief&tool=staff_spawn&grep=restart")
	require.Equal(t, 1, page.Total)
	require.Equal(t, 3, page.Messages[0].Index)
	require.Len(t, page.Messages[0].Content, 2, "tool filter preserves whole messages")
	require.Equal(t, `{"task":"restart a worker"}`, page.Messages[0].Content[1].Arguments)
	decode("?agent=chief&role=user&grep=second")
	require.Equal(t, snap.Snapshot.Messages[5].Origin, page.Messages[0].Origin)
	decode("?agent=chief&role=system")
	require.Equal(t, 0, page.Messages[0].Index)
	decode("?agent=chief&grep=not-found")
	require.Empty(t, page.Messages)
	require.False(t, page.HasMore)
	decode("?agent=chief&offset=999999999&limit=100")
	require.Empty(t, page.Messages)
	require.False(t, page.HasMore)
	require.Equal(t, prettyTestSpanID(3), fe.FocusedSpan, "read changed focus")
	require.Equal(t, "IDLE", db.Agents()[0].State, "read changed the roster")

	w := request("?agent=chief", func(context.Context, string, string) (consoleAgentSnapshot, error) {
		return consoleAgentSnapshot{}, io.EOF
	})
	require.Equal(t, 502, w.Code)
	require.Contains(t, w.Body.String(), "read agent checkpoint")
	w = request("?agent=chief", func(context.Context, string, string) (consoleAgentSnapshot, error) {
		return consoleAgentSnapshot{}, nil
	})
	require.Equal(t, 502, w.Code)
}

func TestConsoleRecordedCheckpoint(t *testing.T) {
	llmType := &ast.Type{NamedType: "LLM", NonNull: true}
	id := call.New().Append(llmType, "llm")
	root := id.Digest().String()
	arg := func(name string, value any) *call.Argument {
		lit, err := call.ToLiteral(value)
		require.NoError(t, err)
		return call.NewArgument(name, lit, false)
	}
	appendFrame := func(field string, args ...*call.Argument) {
		id = id.Append(llmType, field, call.WithArgs(args...))
	}
	// The tool's source is not available at all. Message extraction must not
	// require this frame or its dependencies, let alone evaluate it.
	tool := call.New().Append(&ast.Type{NamedType: "MissingTool"}, "unavailable")
	appendFrame("withTools", call.NewArgument("object", call.NewLiteralID(tool), false), arg("owner", "middleware"))
	// Historical traces remain readable after the stateful selector is removed.
	appendFrame("__withCompositionOwner", arg("owner", "middleware"))
	appendFrame("withSystemPrompt", arg("prompt", "system"), arg("owner", "middleware"))
	appendFrame("withPrompt", arg("prompt", "user\nprompt"), arg("origin", map[string]any{"kind": "AGENT", "agentName": "chief", "ref": "#3"}))
	appendFrame("withResponse", arg("content", []any{
		map[string]any{"kind": "THINKING", "text": "thinking"},
		map[string]any{"kind": "TOOL_CALL", "toolName": "staff_spawn", "callId": "call-1", "arguments": `{"task":"do work"}`},
	}))
	appendFrame("withToolResult", arg("callId", "call-1"), arg("content", "result\n"), arg("errored", true))
	dag, err := id.ToProto()
	require.NoError(t, err)
	newDB := func() *dagui.DB {
		db := dagui.NewDB()
		db.Calls = proto.Clone(dag).(*callpbv1.DAG).GetRecipe().CallsByDigest
		delete(db.Calls, tool.Digest().String())
		return db
	}
	agent := consoleAgent{ID: "worker", State: "RUNNING", SnapshotDigest: id.Digest().String()}
	db := newDB()
	got, err := decodeConsoleCheckpoint(db, agent)
	require.NoError(t, err)
	require.Equal(t, "recorded-checkpoint", got.Source)
	require.Equal(t, agent.SnapshotDigest, got.Digest)
	require.Empty(t, got.Snapshot.ID, "must not invent an engine handle")
	require.Len(t, got.Snapshot.Messages, 4)
	require.Equal(t, "system", got.Snapshot.Messages[0].Content[0].Text)
	require.Equal(t, "user\nprompt", got.Snapshot.Messages[1].Content[0].Text)
	require.Equal(t, "chief", got.Snapshot.Messages[1].Origin.AgentName)
	require.Equal(t, `{"task":"do work"}`, got.Snapshot.Messages[2].Content[1].Arguments)
	require.Equal(t, "call-1", got.Snapshot.Messages[3].Content[0].CallID)
	require.True(t, got.Snapshot.Messages[3].Content[0].Errored)
	require.Equal(t, "result\n", got.Snapshot.Messages[3].Content[0].Text)

	// Exercise the recorded HTTP route with no engine client and a separate
	// (empty) rendered DB. It must read only the cached extraction payloads.
	db.ImportSnapshots([]dagui.SpanSnapshot{{ID: prettyTestSpanID(9), Agent: true, AgentID: agent.ID, AgentName: "worker", AgentState: "RUNNING", AgentSnapshotDigest: agent.SnapshotDigest}})
	fe := NewWithDB(io.Discard, dagui.NewDB())
	fe.traceID = "recorded"
	inspector := &consoleTraceInspector{frontend: fe, traceID: "recorded", db: db}
	w := httptest.NewRecorder()
	inspector.agents(w, httptest.NewRequest(http.MethodGet, "/agents", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"source":"recorded-trace"`)
	require.Contains(t, w.Body.String(), `"loadedSpansOnly":false`)
	w = httptest.NewRecorder()
	inspector.transcript(w, httptest.NewRequest(http.MethodGet, "/transcript?agent=worker&tool=staff_spawn", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	var extracted struct {
		Source, SnapshotDigest string
		Messages               []consoleTranscriptMessage
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &extracted))
	require.Equal(t, "recorded-checkpoint", extracted.Source)
	require.Equal(t, agent.SnapshotDigest, extracted.SnapshotDigest)
	require.Len(t, extracted.Messages, 1)
	require.Equal(t, 2, extracted.Messages[0].Index)
	require.Empty(t, fe.db.Agents(), "extraction mutated the rendered DB")

	for _, tc := range []struct {
		name, want string
		mutate     func(*dagui.DB)
	}{
		{"missing", "missing", func(db *dagui.DB) { delete(db.Calls, root) }},
		{"cycle", "cycle", func(db *dagui.DB) { db.Calls[root].ReceiverDigest = agent.SnapshotDigest }},
		{"unknown", "unsupported", func(db *dagui.DB) { db.Calls[agent.SnapshotDigest].Field = "step" }},
		{"module", "not a core LLM", func(db *dagui.DB) { db.Calls[root].Module = &callpbv1.Module{Name: "custom"} }},
		{"missing message data", "invalid withToolResult", func(db *dagui.DB) { db.Calls[agent.SnapshotDigest].Args = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newDB()
			tc.mutate(db)
			_, err := decodeConsoleCheckpoint(db, agent)
			require.ErrorContains(t, err, tc.want)
		})
	}
	_, err = decodeConsoleCheckpoint(db, consoleAgent{ID: "no-anchor"})
	require.ErrorContains(t, err, "no recorded checkpoint")
	_, err = consoleLiteralData(&callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: "not-data"}})
	require.ErrorContains(t, err, "expected recorded literal data")
}

func TestConsoleTimings(t *testing.T) {
	db := dagui.NewDB()
	rootID, midID, leafID := prettyTestSpanID(1), prettyTestSpanID(2), prettyTestSpanID(3)
	start := time.Unix(100, 0).UTC()
	// Deliberately import out of chronological order. The short internal parent
	// must not hide its longer child when filtered out.
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: leafID, ParentID: midID, Name: "leaf\noperation", StartTime: start.Add(2 * time.Second), EndTime: start.Add(4 * time.Second), Final: true},
		{ID: midID, ParentID: rootID, Name: "internal parent", Internal: true, StartTime: start.Add(time.Second), EndTime: start.Add(time.Second + time.Millisecond), Final: true},
		{ID: rootID, Name: "root", StartTime: start, Final: true},
		{ID: prettyTestSpanID(4), Name: "unrelated", StartTime: start, EndTime: start.Add(time.Second), Final: true},
		{ID: prettyTestSpanID(5), ParentID: rootID, Name: "running", StartTime: start.Add(3 * time.Second), Final: true},
		{ID: prettyTestSpanID(6), ParentID: rootID, Name: "unknown timing", Final: true},
	})
	detail, ok := RenderSpanTimings(db, rootID, 0, 0, start.Add(5*time.Second))
	if !ok {
		t.Fatal("root not found")
	}
	for _, want := range []string{
		"Loaded spans only (including internal); incomplete",
		"not CPU self time",
		rootID.String() + "  0000000000000000  0s  5s (so far)  \"root\"",
		midID.String() + "  " + rootID.String() + "  1s  1ms  \"internal parent\"",
		leafID.String() + "  " + midID.String() + "  2s  2s  \"leaf\\noperation\"",
		"3s  2s (so far)  \"running\"",
		"unknown  unknown  \"unknown timing\"",
		"shown: 5; omitted: 0 (0 below minDuration, 0 over limit); loaded subtree: 5",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("missing %q:\n%s", want, detail)
		}
	}
	if strings.Contains(detail, "unrelated") {
		t.Errorf("included unrelated span:\n%s", detail)
	}
	if strings.Index(detail, "\"internal parent\"") > strings.Index(detail, "\"leaf\\noperation\"") {
		t.Errorf("not chronological:\n%s", detail)
	}
	filtered, _ := RenderSpanTimings(db, rootID, time.Second, 2, start.Add(5*time.Second))
	if !strings.Contains(filtered, "\"leaf\\noperation\"") || strings.Contains(filtered, "\"internal parent\"") {
		t.Errorf("filter pruned a descendant or retained short parent:\n%s", filtered)
	}
	if !strings.Contains(filtered, "shown: 2; omitted: 3 (1 below minDuration, 2 over limit)") {
		t.Errorf("incorrect omission counts:\n%s", filtered)
	}
	unknown, _ := RenderSpanTimings(db, rootID, time.Hour, 0, start.Add(5*time.Second))
	if !strings.Contains(unknown, "unknown  unknown  \"unknown timing\"") {
		t.Errorf("unknown timing was filtered out:\n%s", unknown)
	}
	if _, ok := RenderSpanTimings(db, prettyTestSpanID(99), 0, 0, start); ok {
		t.Error("unknown root reported found")
	}
}

func TestConsoleTimingsHandler(t *testing.T) {
	db := dagui.NewDB()
	id := prettyTestSpanID(1)
	db.ImportSnapshots([]dagui.SpanSnapshot{{ID: id, Name: "root", StartTime: time.Unix(100, 0), EndTime: time.Unix(101, 0), Final: true}})
	fe := NewWithDB(io.Discard, db)
	for _, tc := range []struct {
		query string
		code  int
	}{
		{"", http.StatusBadRequest},
		{"?root=bad", http.StatusBadRequest},
		{"?root=" + prettyTestSpanID(99).String(), http.StatusNotFound},
		{"?root=" + id.String(), http.StatusOK},
		{"?root=" + id.String() + "&minDuration=1ms&limit=0", http.StatusOK},
		{"?root=" + id.String() + "&minDuration=-1s", http.StatusBadRequest},
		{"?root=" + id.String() + "&minDuration=oops", http.StatusBadRequest},
		{"?root=" + id.String() + "&limit=-1", http.StatusBadRequest},
		{"?root=" + id.String() + "&limit=oops", http.StatusBadRequest},
	} {
		t.Run(tc.query, func(t *testing.T) {
			w := httptest.NewRecorder()
			fe.consoleTimingsHandler(w, httptest.NewRequest(http.MethodGet, "/timings"+tc.query, nil))
			if w.Code != tc.code {
				t.Errorf("status = %d, want %d: %s", w.Code, tc.code, w.Body.String())
			}
		})
	}
}

func TestValidateConsoleKey(t *testing.T) {
	valid := []string{
		// named keys
		"enter", "esc", "escape", "tab", "space", "backspace",
		"up", "down", "left", "right", "home", "end", "pgup", "pgdown",
		"insert", "delete", "begin", "find", "select",
		// bare characters (including the keymap's own bindings)
		"a", "L", "T", "/", "-", "?",
		// the plus key and modified pluses
		"+", "ctrl++",
		// f-keys
		"f1", "f10", "f20",
		// modifier combos
		"ctrl+c", "ctrl+s", "alt+enter", "shift+tab", "ctrl+alt+delete",
		"meta+x", "super+z", "hyper+q",
	}
	for _, spec := range valid {
		if err := validateConsoleKey(spec); err != nil {
			t.Errorf("validateConsoleKey(%q) = %v, want nil", spec, err)
		}
	}

	invalid := []string{
		// the emacs/tmux-style names that motivated validation: tuist would
		// type these into the TUI as literal text
		"C-s", "C-c", "M-x",
		// typos and unknown names
		"entr", "escpe", "control+c", "ctl+c",
		// a modifier with nothing after it is parsed as a key name
		"ctrl+notakey",
	}
	for _, spec := range invalid {
		err := validateConsoleKey(spec)
		if err == nil {
			t.Errorf("validateConsoleKey(%q) = nil, want error", spec)
			continue
		}
		if !strings.Contains(err.Error(), spec) {
			t.Errorf("validateConsoleKey(%q) error does not name the token: %v", spec, err)
		}
	}

	// A trailing bare modifier is a single-part spec, so it's a key *name*
	// lookup — "ctrl" alone is not a key.
	if err := validateConsoleKey("ctrl"); err == nil {
		t.Error("validateConsoleKey(\"ctrl\") = nil, want error")
	}

	// Repeat syntax is stripped by parseConsoleKeys before validation; a
	// malformed count survives as part of the token and must be rejected.
	keys := parseConsoleKeys("down*3 enter")
	if len(keys) != 4 {
		t.Fatalf("parseConsoleKeys(\"down*3 enter\") = %v", keys)
	}
	for _, k := range keys {
		if err := validateConsoleKey(k); err != nil {
			t.Errorf("validateConsoleKey(%q) = %v, want nil", k, err)
		}
	}
	for _, k := range parseConsoleKeys("down*x") {
		if err := validateConsoleKey(k); err == nil {
			t.Errorf("validateConsoleKey(%q) = nil, want error", k)
		}
	}
}

func TestConsoleDuration(t *testing.T) {
	for _, tc := range []struct {
		in   string
		def  time.Duration
		want time.Duration
		err  bool
	}{
		{in: "", def: 2 * time.Second, want: 2 * time.Second},
		{in: "5s", want: 5 * time.Second},
		{in: "1500ms", want: 1500 * time.Millisecond},
		{in: "30", want: 30 * time.Second},
		{in: "2.5", want: 2500 * time.Millisecond},
		{in: "bogus", err: true},
		{in: "-5s", err: true},
		{in: "-3", err: true},
	} {
		got, err := consoleDuration(tc.in, tc.def)
		if tc.err {
			if err == nil {
				t.Errorf("consoleDuration(%q) = %v, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("consoleDuration(%q) = %v, want %v", tc.in, err, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("consoleDuration(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestConsoleSpanDetail(t *testing.T) {
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	midID := prettyTestSpanID(2)
	leafID := prettyTestSpanID(3)
	start := time.Unix(100, 0).UTC()
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "root call",
			StartTime: start,
			EndTime:   start.Add(3 * time.Second),
			Final:     true,
		},
		{
			ID:          midID,
			TraceID:     prettyTestTraceID(),
			Name:        "middle span",
			StartTime:   start.Add(time.Second),
			EndTime:     start.Add(3 * time.Second),
			ParentID:    rootID,
			Passthrough: true,
			RollUpLogs:  true,
			Final:       true,
		},
		{
			ID:           leafID,
			TraceID:      prettyTestTraceID(),
			Name:         "leaf op",
			StartTime:    start.Add(2 * time.Second),
			EndTime:      start.Add(3 * time.Second),
			ParentID:     midID,
			Internal:     true,
			Encapsulated: true,
			Cached:       true,
			Final:        true,
		},
	})
	db.SetPrimarySpan(rootID)

	detail, ok := RenderSpanDetail(db, leafID)
	if !ok {
		t.Fatalf("RenderSpanDetail(%s) = not found", leafID)
	}
	for _, want := range []string{
		"span:     " + leafID.String() + "  leaf op",
		"status:   ok",
		"started:  " + start.Add(2*time.Second).Format(time.RFC3339Nano),
		"ended:    " + start.Add(3*time.Second).Format(time.RFC3339Nano),
		"duration: 1s",
		"flags:    internal encapsulated cached",
		"parents (nearest first):",
		"  " + midID.String() + "  middle span  [passthrough rollUpLogs]",
		"  " + rootID.String() + "  root call",
		"children (loaded): 0",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("span detail missing %q:\n%s", want, detail)
		}
	}
	// The flagless root ancestor must not grow an empty flag bracket.
	if strings.Contains(detail, "root call  [") {
		t.Errorf("root ancestor line should have no flag bracket:\n%s", detail)
	}

	// A root span reports its (lack of a) parent chain explicitly, and lists
	// its direct children (with their flags) as the way down.
	rootDetail, ok := RenderSpanDetail(db, rootID)
	if !ok {
		t.Fatalf("RenderSpanDetail(%s) = not found", rootID)
	}
	for _, want := range []string{
		"(none — root span)",
		"flags:    (none)",
		"children (loaded): 1",
		"  " + midID.String() + "  ok     middle span  [passthrough rollUpLogs]",
	} {
		if !strings.Contains(rootDetail, want) {
			t.Errorf("root detail missing %q:\n%s", want, rootDetail)
		}
	}

	// Unknown spans are reported as such, not as an empty page.
	if _, ok := RenderSpanDetail(db, prettyTestSpanID(99)); ok {
		t.Error("RenderSpanDetail of unknown span reported ok")
	}
}

func TestRenderSpanDetailErrors(t *testing.T) {
	db := dagui.NewDB()
	rootID, failedID, originID := prettyTestSpanID(1), prettyTestSpanID(2), prettyTestSpanID(3)
	start := time.Unix(100, 0).UTC()
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: rootID, TraceID: prettyTestTraceID(), Name: "root", StartTime: start, EndTime: start.Add(time.Second), Final: true},
		{ID: originID, TraceID: prettyTestTraceID(), ParentID: rootID, Name: "exec false", StartTime: start, EndTime: start.Add(time.Second),
			Status: sdktrace.Status{Code: codes.Error, Description: "exit code: 1"}, Final: true},
		{ID: failedID, TraceID: prettyTestTraceID(), ParentID: rootID, Name: "check", StartTime: start, EndTime: start.Add(time.Second),
			Status: sdktrace.Status{Code: codes.Error, Description: "process failed\nsee above"},
			Links: []dagui.SpanLink{{
				SpanContext: dagui.SpanContext{TraceID: prettyTestTraceID(), SpanID: originID},
				Purpose:     telemetry.LinkPurposeErrorOrigin,
			}}, Final: true},
	})
	detail, ok := RenderSpanDetail(db, failedID)
	if !ok {
		t.Fatal("failed span not found")
	}
	for _, want := range []string{
		"status:   ERROR",
		// A multi-line status message stays aligned under its label.
		"error:    process failed\n          see above",
		"error origins:\n  " + originID.String() + "  exec false",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("failed span detail missing %q:\n%s", want, detail)
		}
	}
	// Children are listed with their own status so a failing branch is
	// visible from its parent.
	rootDetail, _ := RenderSpanDetail(db, rootID)
	if !strings.Contains(rootDetail, "  "+failedID.String()+"  ERROR  check") {
		t.Errorf("root detail missing errored child:\n%s", rootDetail)
	}
}

func TestRenderSpanList(t *testing.T) {
	db := dagui.NewDB()
	start := time.Unix(100, 0).UTC()
	var snaps []dagui.SpanSnapshot
	for i := 1; i <= 5; i++ {
		snaps = append(snaps, dagui.SpanSnapshot{
			ID: prettyTestSpanID(byte(i)), TraceID: prettyTestTraceID(), Name: fmt.Sprintf("step %d", i),
			StartTime: start.Add(time.Duration(i) * time.Second), EndTime: start.Add(time.Duration(i+1) * time.Second), Final: true,
		})
	}
	snaps = append(snaps, dagui.SpanSnapshot{
		ID: prettyTestSpanID(9), TraceID: prettyTestTraceID(), Name: "exec redis", Service: true, ServiceName: "cache",
		StartTime: start, Final: true,
	})
	db.ImportSnapshots(snaps)

	all := RenderSpanList(db, "", 0)
	if n := strings.Count(all, "\n"); n != 6 {
		t.Errorf("unlimited listing has %d lines, want 6:\n%s", n, all)
	}
	if !strings.Contains(all, prettyTestSpanID(9).String()+"  run    exec redis  [service cache]") {
		t.Errorf("service span not tagged:\n%s", all)
	}
	// A service is findable by its hostname as well as its span name.
	if byHost := RenderSpanList(db, "cache", 0); !strings.Contains(byHost, "exec redis") {
		t.Errorf("service not found by hostname:\n%s", byHost)
	}
	// The limit keeps the newest matches and counts what it dropped.
	limited := RenderSpanList(db, "step", 2)
	for _, want := range []string{
		"... 3 earlier matching spans omitted",
		"step 4", "step 5",
	} {
		if !strings.Contains(limited, want) {
			t.Errorf("limited listing missing %q:\n%s", want, limited)
		}
	}
	if strings.Contains(limited, "step 3") {
		t.Errorf("limited listing kept an older match:\n%s", limited)
	}
	// A placeholder parent (referenced, never received) is not listed.
	db.ImportSnapshots([]dagui.SpanSnapshot{{
		ID: prettyTestSpanID(10), TraceID: prettyTestTraceID(), ParentID: prettyTestSpanID(11), Name: "orphan", Final: true,
	}})
	if got := RenderSpanList(db, "", 0); strings.Contains(got, prettyTestSpanID(11).String()) {
		t.Errorf("placeholder span listed:\n%s", got)
	}
}
