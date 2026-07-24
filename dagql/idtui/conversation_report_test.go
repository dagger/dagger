package idtui

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/muesli/termenv"
	"github.com/vito/tuist"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/dagql/dagui"
)

// TestConversationReportFlagsToolResultTokens verifies a tool call that fed a
// large result back into context is flagged inline with an estimated token
// count, so an outsized, context-bloating result is easy to spot in the report.
func TestConversationReportFlagsToolResultTokens(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	promptID := prettyTestSpanID(2)
	toolCallID := prettyTestSpanID(3)
	start := time.Unix(100, 0)
	end := start.Add(10 * time.Second)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "shell",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			ID:        promptID,
			TraceID:   prettyTestTraceID(),
			Name:      "LLM prompt",
			LLMRole:   "user",
			ParentID:  rootID,
			StartTime: start.Add(time.Second),
			EndTime:   start.Add(2 * time.Second),
			Final:     true,
		},
		{
			ID:                  toolCallID,
			TraceID:             prettyTestTraceID(),
			Name:                "read_file",
			LLMRole:             "assistant",
			LLMTool:             "read_file",
			LLMToolResultTokens: 12345,
			ParentID:            rootID,
			StartTime:           start.Add(2 * time.Second),
			EndTime:             start.Add(3 * time.Second),
			Final:               true,
		},
	})
	db.SetPrimarySpan(rootID)

	fe := NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()

	r := newRenderer(fe.db, 0, fe.FrontendOpts, true)
	lines := fe.conversationReport(tuist.Context{Width: 120}, r, false)
	joined := strings.Join(lines, "\n")

	// 12345 tokens formats compactly (~12.3k tok) so it fits inline on the row.
	if !strings.Contains(joined, "~12.3k tok") {
		t.Fatalf("tool-call row missing result-token badge:\n%s", joined)
	}
}

// TestConversationReportNestsSubAgent verifies the final report surfaces the LLM
// conversation under a CONVERSATION heading, in start-time order, with a
// sub-agent's turns nested one level under the tool call that spawned them.
func TestConversationReportNestsSubAgent(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	promptID := prettyTestSpanID(2)
	toolCallID := prettyTestSpanID(3)
	subPromptID := prettyTestSpanID(4)
	subResponseID := prettyTestSpanID(5)
	start := time.Unix(100, 0)
	end := start.Add(time.Second)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "shell",
			StartTime: start,
			EndTime:   end.Add(5 * time.Second),
			Final:     true,
		},
		{
			ID:        promptID,
			TraceID:   prettyTestTraceID(),
			Name:      "LLM prompt",
			LLMRole:   "user",
			ParentID:  rootID,
			StartTime: start.Add(time.Second),
			EndTime:   start.Add(2 * time.Second),
			Final:     true,
		},
		{
			ID:        toolCallID,
			TraceID:   prettyTestTraceID(),
			Name:      "spawn-agent",
			LLMRole:   "assistant",
			LLMTool:   "spawn-agent",
			ParentID:  rootID,
			StartTime: start.Add(2 * time.Second),
			EndTime:   start.Add(3 * time.Second),
			Final:     true,
		},
		{
			ID:        subPromptID,
			TraceID:   prettyTestTraceID(),
			Name:      "sub-prompt",
			LLMRole:   "user",
			ParentID:  toolCallID,
			StartTime: start.Add(3 * time.Second),
			EndTime:   start.Add(4 * time.Second),
			Final:     true,
		},
		{
			ID:        subResponseID,
			TraceID:   prettyTestTraceID(),
			Name:      "sub-response",
			LLMRole:   "assistant",
			ParentID:  toolCallID,
			StartTime: start.Add(4 * time.Second),
			EndTime:   start.Add(5 * time.Second),
			Final:     true,
		},
	})
	db.SetPrimarySpan(rootID)

	fe := NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()

	r := newRenderer(fe.db, 0, fe.FrontendOpts, true)
	lines := fe.conversationReport(tuist.Context{Width: 120}, r, false)
	if len(lines) == 0 {
		t.Fatal("conversationReport returned no lines")
	}
	joined := strings.Join(lines, "\n")

	if !strings.HasPrefix(lines[0], "CONVERSATION") {
		t.Fatalf("top header = %q, want CONVERSATION heading\n%s", lines[0], joined)
	}

	// Tool-call names are humanized by renderSpan (spawn-agent -> SpawnAgent), so
	// match case-insensitively.
	promptIdx, spawnIdx, subPromptIdx, subResponseIdx := -1, -1, -1, -1
	for i, l := range lines {
		ll := strings.ToLower(l)
		switch {
		case promptIdx == -1 && strings.Contains(ll, "llm prompt"):
			promptIdx = i
		case spawnIdx == -1 && strings.Contains(ll, "spawn"):
			spawnIdx = i
		case subPromptIdx == -1 && strings.Contains(ll, "sub-prompt"):
			subPromptIdx = i
		case subResponseIdx == -1 && strings.Contains(ll, "sub-response"):
			subResponseIdx = i
		}
	}
	if promptIdx == -1 || spawnIdx == -1 || subPromptIdx == -1 || subResponseIdx == -1 {
		t.Fatalf("missing rows (prompt=%d spawn=%d subPrompt=%d subResponse=%d):\n%s",
			promptIdx, spawnIdx, subPromptIdx, subResponseIdx, joined)
	}
	// Conversation order (start time): prompt, then the tool call, then its
	// sub-agent's turns beneath it.
	if promptIdx >= spawnIdx || spawnIdx >= subPromptIdx || subPromptIdx >= subResponseIdx {
		t.Fatalf("rows out of order (prompt=%d spawn=%d subPrompt=%d subResponse=%d):\n%s",
			promptIdx, spawnIdx, subPromptIdx, subResponseIdx, joined)
	}
	// The tool call sits at the margin; its sub-agent turns indent one level under
	// it (two spaces per depth).
	if strings.HasPrefix(lines[spawnIdx], " ") {
		t.Fatalf("tool-call line = %q, want no indent", lines[spawnIdx])
	}
	if !strings.HasPrefix(lines[subPromptIdx], "  ") {
		t.Fatalf("sub-agent line = %q, want two-space indent under the tool call", lines[subPromptIdx])
	}
}

// TestConversationReportWrapsMarkdownForDepth is a regression test for janky
// Markdown wrapping in the final report: renderMessageNode rendered each message
// at full width and only *then* prepended the depth indent to every line, so a
// nested sub-agent's wrapped Markdown overflowed the viewport by two columns per
// nesting level. The content width handed to the message logs must account for
// the indentation so no rendered line exceeds the terminal width.
func TestConversationReportWrapsMarkdownForDepth(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	const width = 80
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	promptID := prettyTestSpanID(2)
	spawn1ID := prettyTestSpanID(3)
	spawn2ID := prettyTestSpanID(4)
	subResponseID := prettyTestSpanID(5)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: rootID, TraceID: prettyTestTraceID(), Name: "shell", StartTime: start, EndTime: start.Add(10 * time.Second), Final: true},
		{ID: promptID, TraceID: prettyTestTraceID(), Name: "LLM prompt", LLMRole: "user", ParentID: rootID, StartTime: start.Add(time.Second), EndTime: start.Add(2 * time.Second), Final: true},
		// A sub-agent spawned by a tool call, which itself spawns a deeper
		// sub-agent: the deepest message nests two levels down in the report.
		{ID: spawn1ID, TraceID: prettyTestTraceID(), Name: "spawn", LLMRole: "assistant", LLMTool: "spawn", ParentID: rootID, StartTime: start.Add(2 * time.Second), EndTime: start.Add(3 * time.Second), Final: true},
		{ID: spawn2ID, TraceID: prettyTestTraceID(), Name: "spawn", LLMRole: "assistant", LLMTool: "spawn", ParentID: spawn1ID, StartTime: start.Add(3 * time.Second), EndTime: start.Add(4 * time.Second), Final: true},
		{ID: subResponseID, TraceID: prettyTestTraceID(), Name: "LLM response", Message: "received", LLMRole: "assistant", ParentID: spawn2ID, StartTime: start.Add(4 * time.Second), EndTime: start.Add(5 * time.Second), Final: true},
	})
	db.SetPrimarySpan(rootID)

	fe := NewWithDB(io.Discard, db)
	fe.setWindowSizeLocked(windowSize{Width: width, Height: 40})

	logs := NewVterm(termenv.Ascii)
	logs.SetWidth(width)
	// Dense single-character "words" pack right up to the wrap boundary, so a
	// continuation line reaches ~(width - gutter) and the depth indent prepended
	// afterwards pushes it past the viewport unless the wrap width accounts for
	// the indentation.
	_, _ = logs.WriteMarkdown([]byte(strings.TrimSpace(strings.Repeat("x ", 120)) + "\n"))
	fe.logs.Logs[subResponseID] = logs

	fe.recalculateViewLocked()

	r := newRenderer(fe.db, 0, fe.FrontendOpts, true)
	lines := fe.conversationReport(tuist.Context{Width: width}, r, false)
	if len(lines) == 0 {
		t.Fatal("conversationReport returned no lines")
	}
	for _, l := range lines {
		if w := len([]rune(strings.TrimRight(l, " "))); w > width {
			t.Errorf("line exceeds width %d (got %d): %q", width, w, l)
		}
	}
}

// TestConversationReportRendersToolCallArgsAndOutput verifies a tool call in the
// final report shows both its arguments (the display span's own logs, in full)
// and the result the LLM saw (the nested exec span's own logs, tail-capped).
// This is the content that made the bare-name report "sad": Cloud's descendant
// roll-up doesn't return the exec output, so the report fetches and renders each
// span's own logs directly.
func TestConversationReportRendersToolCallArgsAndOutput(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	promptID := prettyTestSpanID(2)
	toolCallID := prettyTestSpanID(3)
	execID := prettyTestSpanID(4)
	start := time.Unix(100, 0)
	end := start.Add(10 * time.Second)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "shell",
			StartTime: start,
			EndTime:   end,
			Final:     true,
		},
		{
			ID:        promptID,
			TraceID:   prettyTestTraceID(),
			Name:      "LLM prompt",
			LLMRole:   "user",
			ParentID:  rootID,
			StartTime: start.Add(time.Second),
			EndTime:   start.Add(2 * time.Second),
			Final:     true,
		},
		{
			// The tool-call display span: surfaced (LLMRole set), its own logs
			// carry the streamed arguments.
			ID:        toolCallID,
			TraceID:   prettyTestTraceID(),
			Name:      "dang_eval",
			LLMRole:   "assistant",
			LLMTool:   "dang_eval",
			ParentID:  rootID,
			StartTime: start.Add(2 * time.Second),
			EndTime:   start.Add(3 * time.Second),
			Final:     true,
		},
		{
			// The nested exec span: not surfaced (no LLMRole), its own logs carry
			// the result the LLM received.
			ID:        execID,
			TraceID:   prettyTestTraceID(),
			Name:      "dang_eval(script: ...)",
			LLMTool:   "dang_eval",
			ParentID:  toolCallID,
			StartTime: start.Add(2 * time.Second),
			EndTime:   start.Add(3 * time.Second),
			Final:     true,
		},
	})
	db.SetPrimarySpan(rootID)

	fe := NewWithDB(io.Discard, db)

	argLogs := NewVterm(termenv.Ascii)
	argLogs.SetWidth(120)
	_, _ = argLogs.Write([]byte(`{"script":"currentWorkspace.id"}` + "\n"))
	fe.logs.Logs[toolCallID] = argLogs

	// A tall result, so the cap kicks in and hides all but the last
	// llmLogsLastLines lines.
	outLogs := NewVterm(termenv.Ascii)
	outLogs.SetWidth(120)
	const outputLines = llmLogsLastLines + 12
	for i := 0; i < outputLines; i++ {
		_, _ = outLogs.Write([]byte(fmt.Sprintf("result line %02d\n", i)))
	}
	fe.logs.Logs[execID] = outLogs

	fe.recalculateViewLocked()

	r := newRenderer(fe.db, 0, fe.FrontendOpts, true)
	lines := fe.conversationReport(tuist.Context{Width: 120}, r, false)
	joined := strings.Join(lines, "\n")

	// The arguments render in full (they're short and worth reading verbatim).
	if !strings.Contains(joined, `{"script":"currentWorkspace.id"}`) {
		t.Fatalf("tool-call arguments missing from report:\n%s", joined)
	}
	// The result's tail renders...
	if !strings.Contains(joined, fmt.Sprintf("result line %02d", outputLines-1)) {
		t.Fatalf("tool-call output tail missing from report:\n%s", joined)
	}
	// ...capped, with the earliest lines hidden behind a trim header.
	if strings.Contains(joined, "result line 00") {
		t.Fatalf("tool-call output should be capped, but the first line is present:\n%s", joined)
	}
	if want := fmt.Sprintf("...%d lines hidden...", outputLines-llmLogsLastLines); !strings.Contains(joined, want) {
		t.Fatalf("missing trim header %q:\n%s", want, joined)
	}
}

// TestConversationLogsPreFetchedBeforeFailureFetch is a regression test for the
// interactive Pretty TUI rendering the conversation as bare tool-call names. The
// final report's message logs are pre-fetched in recalculateViewLocked; two bugs
// left a failed tool call's arguments unfetched:
//
//  1. the pre-fetch was report-only, so the interactive frontend (a plain TTY,
//     not AI_AGENT) never fetched them; and
//  2. a failed tool-call display span is also a failed row, and the report-only
//     failure fetch -- which ran first -- requested it with the roll-up its
//     RollUpLogs implies (descendants=true, which Cloud returns empty for),
//     latching the requestLogs dedup so the descendants=false arguments fetch
//     never fired.
//
// So: the conversation is fetched in BOTH modes, descendants=false, and wins the
// dedup over the failure fetch.
func TestConversationLogsPreFetchedBeforeFailureFetch(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	rootID := prettyTestSpanID(1)
	toolCallID := prettyTestSpanID(2)
	execID := prettyTestSpanID(3)
	start := time.Unix(100, 0)
	mkDB := func() *dagui.DB {
		db := dagui.NewDB()
		db.ImportSnapshots([]dagui.SpanSnapshot{
			{
				ID:        rootID,
				TraceID:   prettyTestTraceID(),
				Name:      "shell",
				StartTime: start,
				EndTime:   start.Add(3 * time.Second),
				Final:     true,
			},
			{
				// A failed tool-call display span: surfaced (Reveal + LLMRole), so
				// it's a failed row the failure fetch would also claim. RollUpLogs
				// mirrors the live display span, so requestLogs would roll it up.
				ID:         toolCallID,
				TraceID:    prettyTestTraceID(),
				Name:       "inspect",
				LLMRole:    "assistant",
				LLMTool:    "inspect",
				Reveal:     true,
				RollUpLogs: true,
				ParentID:   rootID,
				StartTime:  start.Add(time.Second),
				EndTime:    start.Add(2 * time.Second),
				Status:     sdktrace.Status{Code: codes.Error},
				Final:      true,
			},
			{
				// Its nested exec span: not surfaced, carries the error the LLM saw.
				ID:        execID,
				TraceID:   prettyTestTraceID(),
				Name:      "inspect(on: Query)",
				LLMTool:   "inspect",
				ParentID:  toolCallID,
				StartTime: start.Add(time.Second),
				EndTime:   start.Add(2 * time.Second),
				Status:    sdktrace.Status{Code: codes.Error},
				Final:     true,
			},
		})
		db.SetPrimarySpan(rootID)
		return db
	}

	// Record the descendants flag the log provider is asked for, per span. Only
	// the first (winning) request per span reaches the provider (requestLogs
	// dedups), so this captures which fetch won.
	run := func(reportOnly bool) map[dagui.SpanID]bool {
		fe := NewWithDB(io.Discard, mkDB())
		fe.reportOnly = reportOnly
		fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
		got := map[dagui.SpanID]bool{}
		fe.logProvider = func(id dagui.SpanID, descendants bool) { got[id] = descendants }
		fe.recalculateViewLocked()
		return got
	}

	for _, tc := range []struct {
		name       string
		reportOnly bool
	}{
		{"interactive", false},
		{"report", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := run(tc.reportOnly)
			desc, ok := got[toolCallID]
			if !ok {
				t.Fatalf("tool-call display span logs never requested (args would be missing); fetched=%v", got)
			}
			if desc {
				t.Fatalf("tool-call display span requested with descendants=true; the roll-up is empty and swallows the arguments (fetched=%v)", got)
			}
			if execDesc, ok := got[execID]; !ok || execDesc {
				t.Fatalf("exec span logs not requested own-only (ok=%v descendants=%v); the result/error would be missing", ok, execDesc)
			}
		})
	}
}

// TestPromoteConversationSurfacesMessages verifies the live path: a trace that
// ran an LLM auto-promotes its conversation to the top level (root marked
// passthrough, zoom defaulted to root), replacing the shell's old manual zoom,
// so the revealed message spans render as top-level rows instead of the root's
// setup children.
func TestPromoteConversationSurfacesMessages(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	setupID := prettyTestSpanID(2)
	promptID := prettyTestSpanID(3)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{
			ID:        rootID,
			TraceID:   prettyTestTraceID(),
			Name:      "shell",
			StartTime: start,
			EndTime:   start.Add(10 * time.Second),
		},
		{
			// bootstrap noise that the promotion should hide.
			ID:        setupID,
			TraceID:   prettyTestTraceID(),
			Name:      "connect",
			ParentID:  rootID,
			StartTime: start.Add(time.Second),
			EndTime:   start.Add(2 * time.Second),
		},
		{
			ID:        promptID,
			TraceID:   prettyTestTraceID(),
			Name:      "LLM prompt",
			LLMRole:   "user",
			ParentID:  rootID,
			StartTime: start.Add(3 * time.Second),
			EndTime:   start.Add(4 * time.Second),
		},
	})
	db.SetPrimarySpan(rootID)

	fe := NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()

	if !fe.db.RootSpan.Passthrough {
		t.Fatal("expected promoteConversationLocked to mark the root span passthrough")
	}
	if fe.ZoomedSpan != fe.db.PrimarySpan {
		t.Fatalf("expected zoom to default to the primary span, got %v", fe.ZoomedSpan)
	}
	// Top-level rows are the revealed message spans, not the root's setup children.
	var topSpanIDs []dagui.SpanID
	for _, tree := range fe.rowsView.Body {
		topSpanIDs = append(topSpanIDs, tree.Span.ID)
	}
	if len(topSpanIDs) != 1 || topSpanIDs[0] != promptID {
		t.Fatalf("top-level rows = %v, want just the surfaced message %v", topSpanIDs, promptID)
	}
}

func TestPromoteConversationUsesPrimarySpan(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	unrelatedRootID := prettyTestSpanID(1)
	primaryID := prettyTestSpanID(2)
	promptID := prettyTestSpanID(3)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: unrelatedRootID, TraceID: prettyTestTraceID(), Name: "remote root received first", StartTime: start, EndTime: start.Add(10 * time.Second)},
		{ID: primaryID, TraceID: prettyTestTraceID(), Name: "dagger agent", StartTime: start.Add(time.Second), EndTime: start.Add(10 * time.Second)},
		{ID: promptID, TraceID: prettyTestTraceID(), Name: "LLM prompt", LLMRole: "user", ParentID: primaryID, StartTime: start.Add(2 * time.Second), EndTime: start.Add(3 * time.Second)},
	})
	db.SetPrimarySpan(primaryID)

	fe := NewWithDB(io.Discard, db)
	fe.recalculateViewLocked()

	primary := db.Spans.Map[primaryID]
	if !primary.Passthrough {
		t.Fatal("expected conversation promotion to mark the primary CLI span passthrough")
	}
	if db.RootSpan.Passthrough {
		t.Fatal("expected unrelated first-received root to remain unchanged")
	}
	if fe.ZoomedSpan != primaryID {
		t.Fatalf("zoomed span = %v, want primary %v", fe.ZoomedSpan, primaryID)
	}
	if len(fe.rowsView.Body) != 1 || fe.rowsView.Body[0].Span.ID != promptID {
		t.Fatalf("top-level rows = %v, want surfaced prompt %v", fe.rowsView.Body, promptID)
	}
}

// TestConversationIndentsChainedToolCalls is a regression test for the live
// agent view. A turn that opens with a thinking/response nests its tool call
// beneath that reply (its span is parented under it), so the first tool call
// reads as a detail of the reply that introduced it. But a later round the
// model answers with tool calls alone -- no commentary -- parents those calls
// under the LLM step, so they surface as top-level conversation rows: the first
// call sat indented while every subsequent chained call hugged the margin.
// syncSpanTreeState now gives those orphan tool-call rows the same one-level
// indent, so a chain of tool calls reads consistently.
func TestConversationIndentsChainedToolCalls(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	db := dagui.NewDB()
	rootID := prettyTestSpanID(1)
	promptID := prettyTestSpanID(2)
	replyID := prettyTestSpanID(3)
	tool1ID := prettyTestSpanID(4) // opened the turn, nests under the reply
	tool2ID := prettyTestSpanID(5) // orphan: a tool-only round, parents under root
	tool3ID := prettyTestSpanID(6) // orphan chained after tool2
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: rootID, TraceID: prettyTestTraceID(), Name: "dagger agent", StartTime: start, EndTime: start.Add(10 * time.Second)},
		{ID: promptID, TraceID: prettyTestTraceID(), Name: "LLM prompt", LLMRole: "user", ParentID: rootID, StartTime: start.Add(1 * time.Second), EndTime: start.Add(2 * time.Second)},
		{ID: replyID, TraceID: prettyTestTraceID(), Name: "LLM response", LLMRole: "assistant", ParentID: rootID, StartTime: start.Add(3 * time.Second), EndTime: start.Add(4 * time.Second)},
		{ID: tool1ID, TraceID: prettyTestTraceID(), Name: "first_tool", LLMRole: "assistant", LLMTool: "first_tool", ParentID: replyID, StartTime: start.Add(4 * time.Second), EndTime: start.Add(5 * time.Second)},
		{ID: tool2ID, TraceID: prettyTestTraceID(), Name: "second_tool", LLMRole: "assistant", LLMTool: "second_tool", ParentID: rootID, StartTime: start.Add(6 * time.Second), EndTime: start.Add(7 * time.Second)},
		{ID: tool3ID, TraceID: prettyTestTraceID(), Name: "third_tool", LLMRole: "assistant", LLMTool: "third_tool", ParentID: rootID, StartTime: start.Add(8 * time.Second), EndTime: start.Add(9 * time.Second)},
	})
	db.SetPrimarySpan(rootID)

	fe := NewWithDB(io.Discard, db)
	fe.shell = stubShellHandler{}
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	fe.FrontendOpts.ExpandCompleted = true
	fe.FrontendOpts.GCThreshold = time.Hour
	// Focus the prompt, not a tool call: the focus cue ("❯ ") replaces the
	// leading indent on the focused row, which would skew the measurement.
	fe.autoFocus = false
	fe.FocusedSpan = promptID

	// The prompt and reply are messages (no tool): their rows only render once
	// content arrives, so give them some.
	for _, id := range []dagui.SpanID{promptID, replyID} {
		v := NewVterm(termenv.Ascii)
		v.SetWidth(120)
		_, _ = v.Write([]byte("hello\n"))
		fe.logs.Logs[id] = v
	}

	fe.recalculateViewLocked()

	lines := fe.tui.RenderLines()
	joined := strings.Join(lines, "\n")

	indentOf := func(needle string) (int, bool) {
		for _, l := range lines {
			if strings.Contains(l, needle) {
				return len(l) - len(strings.TrimLeft(l, " ")), true
			}
		}
		return 0, false
	}

	// Tool names are humanized by renderSpan (first_tool -> FirstTool).
	first, ok1 := indentOf("FirstTool")
	second, ok2 := indentOf("SecondTool")
	third, ok3 := indentOf("ThirdTool")
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("missing tool-call rows (first=%v second=%v third=%v):\n%s", ok1, ok2, ok3, joined)
	}
	// The first tool call indents under the reply that introduced it.
	if first == 0 {
		t.Fatalf("first tool call should be indented under its reply, got margin:\n%s", joined)
	}
	// The chained tool calls line up with it instead of hugging the margin.
	if second != first || third != first {
		t.Fatalf("chained tool calls not indented consistently: first=%d second=%d third=%d\n%s",
			first, second, third, joined)
	}
}
