package idtui

import (
	"fmt"
	"strings"

	"github.com/muesli/termenv"
	"github.com/vito/tuist"

	"github.com/dagger/dagger/dagql/dagui"
)

// conversationReport renders the CONVERSATION heading plus the surfaced LLM
// message transcript for the root --full report. It returns nil when zoomed
// (the zoom views handle their own rendering) or when nothing surfaces (a
// non-LLM trace, or one whose only messages are boundary-contained fixtures), so
// the caller can fall back to the progress tree. It is the message analog of
// checksReport.
func (fe *frontendPretty) conversationReport(ctx tuist.Context, r *renderer, zoomed bool) []string {
	if zoomed {
		return nil
	}
	convLines := fe.renderConversationSection(ctx, r)
	if len(convLines) == 0 {
		return nil
	}
	return append(fe.renderConversationHeader(), convLines...)
}

// renderConversationHeader renders the top-level "CONVERSATION" heading. Unlike
// CHECKS it carries no pass/fail tally -- a conversation has no verdict.
func (fe *frontendPretty) renderConversationHeader() []string {
	out := NewOutput(new(strings.Builder), termenv.WithProfile(fe.profile))
	return []string{reportHeadingLine(out, "CONVERSATION")}
}

// renderConversationSection renders the trace's LLM conversation for the final
// --full report, independent of the `reveal` mechanism: every surfaced message
// (see DB.SurfacedConversation) is rendered in start-time order, with a sub-agent's
// turns nested under the tool call that spawned them.
func (fe *frontendPretty) renderConversationSection(ctx tuist.Context, r *renderer) []string {
	roots := fe.db.SurfacedConversation()
	if len(roots) == 0 {
		return nil
	}

	buf := new(strings.Builder)
	out := NewOutput(buf, termenv.WithProfile(fe.profile))
	for _, root := range roots {
		fe.renderMessageNode(ctx, out, r, root, 0)
	}
	return strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
}

// renderMessageNode renders one surfaced message at the given depth: its role
// marker and content (a prompt/thinking/response's text, or a tool call's name
// and rolled-up arguments/output), then -- for a tool call that spawned a
// sub-agent -- the child messages one level under it. It is the message analog
// of renderCheckNode, and reuses renderStep so the report matches the live tree.
func (fe *frontendPretty) renderMessageNode(ctx tuist.Context, out TermOutput, r *renderer, node *dagui.MessageNode, depth int) {
	// Render the message row into a buffer with no tree indentation (a detached
	// row has no parent chain for fancyIndent to walk), then indent every line by
	// depth so nested sub-agent turns sit under their tool call.
	buf := new(strings.Builder)
	rowOut := NewOutput(buf, termenv.WithProfile(fe.profile))
	row := &dagui.TraceRow{Span: node.Span, Expanded: true}
	_ = fe.renderStep(ctx, rowOut, r, row, "", fe, false)

	// Prompt/thinking/response spans (Message != "") render their content via
	// renderStep -> renderStepLogs. Tool-call display spans carry their arguments
	// and rolled-up execution output as logs with no Message, so render those
	// explicitly here (the same log block a failed check's cause uses).
	if node.Span.Message == "" {
		fe.renderMessageLogs(rowOut, node.Span)
	}

	indent := strings.Repeat("  ", depth)
	for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		if line == "" {
			fmt.Fprintln(out)
		} else {
			fmt.Fprintln(out, indent+line)
		}
	}

	for _, child := range node.Children {
		fe.renderMessageNode(ctx, out, r, child, depth+1)
	}
}

// renderMessageLogs renders a tool-call node's arguments and, beneath them, the
// result (or error) the LLM saw -- used for tool-call nodes, whose content
// isn't rendered by renderStep's Message path. The arguments are the tool-call
// display span's own logs (the streamed JSON call); the output is the nested
// execution span's own logs. Cloud's descendant roll-up doesn't return that
// execution output here (it crosses the tool call's RollUpLogs boundary), so the
// report fetches the exec span's own logs directly (recalculateViewLocked).
func (fe *frontendPretty) renderMessageLogs(out TermOutput, span *dagui.Span) {
	// Arguments in full -- a tool call's arguments are short and worth reading
	// verbatim. The execution output is tail-capped: a single inspect or
	// read_skill can dump hundreds of lines the model skimmed, which would bury
	// the transcript. llmLogsLastLines matches the tail the live tree shows for a
	// collapsed tool call (and what the model itself is shown).
	fe.renderSpanLogBlock(out, span, span, 0)
	if exec := toolCallExecSpan(span); exec != nil {
		fe.renderSpanLogBlock(out, exec, span, llmLogsLastLines)
	}
}

// renderSpanLogBlock renders logSpan's logs once, prefixed with colorSpan's
// status-colored pipe (so an errored call's output reads red). When maxHeight >
// 0 and the block is taller, only its last maxHeight lines are shown, above a
// "...N lines hidden..." header. No-op when the logs are absent or claimed.
func (fe *frontendPretty) renderSpanLogBlock(out TermOutput, logSpan, colorSpan *dagui.Span, maxHeight int) {
	fe.requestLogsOnRender(logSpan.ID)
	logs := fe.logs.Logs[logSpan.ID]
	if logs == nil || fe.claims.hasLog(logSpan.ID) {
		return
	}
	color := restrainedStatusColor(colorSpan)
	logs.SetPrefix(out.String(VertBoldBar).Foreground(color).String() + " ")
	height := logs.UsedHeight()
	if maxHeight > 0 && height > maxHeight {
		trimPrefix := out.String(VertBoldDash3).Foreground(color).String() + " "
		fe.writeLogTrimHeader(out, trimPrefix, height-maxHeight)
		height = maxHeight
	}
	logs.SetHeight(height)
	fmt.Fprint(out, logs.View())
	fe.claims.claimLog(logSpan)
}

// toolCallExecSpan returns the execution span nested directly beneath a
// tool-call display span -- the internal Call span (LLMTool set, no LLMRole)
// whose own logs carry the result, or error, the LLM received. Returns nil when
// no such child is loaded (a replayed session, or a subtree the incremental
// fetch hasn't pulled).
func toolCallExecSpan(display *dagui.Span) *dagui.Span {
	if display.LLMTool == "" {
		return nil
	}
	for _, child := range display.ChildSpans.Order {
		if child.LLMRole == "" && child.LLMTool != "" {
			return child
		}
	}
	return nil
}
