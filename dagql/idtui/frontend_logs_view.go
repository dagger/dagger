package idtui

import (
	"strings"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// LogsView renders a single span's inline logs (its *Vterm) as a memoized
// child component. It fetches the span's logs on mount and re-renders only
// when its inputs change (the prefix/height the owner feeds it, or a pushed
// Update when the logs or search highlight change). This way the expensive
// Vterm.View() is skipped on unrelated parent repaints -- spinner ticks, focus
// moving to another row, sibling spans completing.
//
// The owner (SpanTreeView, via renderInlineLogs) decides structurally whether
// a row should show inline logs and pre-computes the prefixes; LogsView only
// owns the fetch and the Vterm render.
type LogsView struct {
	tuist.Compo

	fe     *frontendPretty
	spanID dagui.SpanID

	// descendants is passed to the log provider on mount: whether to roll up
	// descendant logs (a check/test whose output lives in a sub-operation).
	descendants bool

	// Inputs synced by the owner each frame. logPrefix/trimPrefix encode the
	// row indent, status colour and focus; a change to any of these (or to
	// height/finalRender) is folded into sig so a stale render is invalidated.
	logPrefix   string
	trimPrefix  string
	height      int
	finalRender bool

	// sig is the last-synced signature of the inputs above; the owner calls
	// sync() to bump the tuist generation only when it actually changes.
	sig string
}

var (
	_ tuist.Component = (*LogsView)(nil)
	_ tuist.Mounter   = (*LogsView)(nil)
)

// getOrCreateLogsView returns the persistent LogsView for a span, creating it
// on first use. Stable identity (the map) lets tuist memoize its render across
// frames and lets it hold fetch state.
func (fe *frontendPretty) getOrCreateLogsView(id dagui.SpanID) *LogsView {
	if fe.logsViews == nil {
		fe.logsViews = make(map[dagui.SpanID]*LogsView)
	}
	lv := fe.logsViews[id]
	if lv == nil {
		lv = &LogsView{fe: fe, spanID: id}
		fe.logsViews[id] = lv
	}
	return lv
}

func (lv *LogsView) Name() string {
	return "Logs(" + lv.spanID.String() + ")"
}

// OnMount drives the interactive lazy-fetch: the first time a row's logs are
// rendered (e.g. on expand), fetch them. It runs on the UI goroutine during the
// render pass, so requestLogsWith (which dedups via fe.requestedLogs) is safe to
// call directly.
//
// Report mode is skipped: its single final render can't wait for an async fetch
// dispatched mid-render, so report fetching is driven eagerly by the surfaced-
// failure pass before the render. (When report mode moves to a two-pass
// discovery render, this guard is where mount-driven report fetching turns on.)
func (lv *LogsView) OnMount(tuist.Context) {
	if lv.fe.reportOnly {
		return
	}
	lv.fe.requestLogsWith(lv.spanID, lv.descendants)
}

// sync updates the render inputs and bumps the tuist generation only when the
// signature changes, so a clean LogsView is served from cache.
func (lv *LogsView) sync(logPrefix, trimPrefix string, height int, finalRender, descendants bool) {
	lv.descendants = descendants
	sig := logPrefix + "\x00" + trimPrefix + "\x00" + boolStr(finalRender)
	if height != lv.height || sig != lv.sig {
		lv.logPrefix = logPrefix
		lv.trimPrefix = trimPrefix
		lv.height = height
		lv.finalRender = finalRender
		lv.sig = sig
		lv.Update()
	}
}

// renderInlineLogs renders a row's own inline logs, the expanded-step-logs case
// previously handled by renderRowContentRest -> renderStepLogs. It mounts the
// row's persistent LogsView, which fetches on mount and memoizes its render.
//
// The mount is structural -- it fires even when the Vterm isn't present yet --
// so OnMount can drive the fetch. In report mode this is what makes the
// two-pass work: the discovery render (RequestSurfacedLogs) mounts the views to
// trigger their fetches, which trace.go drains before the single final render.
func (s *SpanTreeView) renderInlineLogs(ctx tuist.Context, r *renderer, row *dagui.TraceRow, focused bool) []string {
	span := row.Span
	if span.Message != "" || (!row.Expanded && span.LLMTool == "") {
		return nil
	}
	if s.fe.claims.hasLog(span.ID) {
		return nil
	}
	// Size the inline log window to a third of the screen. Read it from
	// ctx.ScreenHeight() (not the imperatively-cached fe.window.Height) so the
	// owning SpanTreeView's render is marked height-dependent in tuist's cache --
	// otherwise a height-only resize cache-hits this row and the window sticks at
	// the height it first saw. The final report and size-unknown renders fall
	// back to fe.window.Height (0 => unbounded).
	limit := s.fe.window.Height / 3
	if !s.fe.finalRender {
		if sh := ctx.ScreenHeight(); sh > 0 {
			limit = sh / 3
		}
	}
	if span.LLMTool != "" && !row.Expanded {
		limit = llmLogsLastLines
	}

	styleOut := NewOutput(new(strings.Builder), termenv.WithProfile(s.fe.profile))
	r.indentFunc = s.indentFunc(styleOut)
	logPrefix, trimPrefix := s.fe.logLinePrefixes(styleOut, r, row, "", focused)

	lv := s.fe.getOrCreateLogsView(span.ID)
	lv.sync(logPrefix, trimPrefix, limit, s.fe.finalRender, s.fe.logDescendants(span.ID))
	return s.RenderChildResult(ctx, lv).Lines
}

func (lv *LogsView) Render(ctx tuist.Context) {
	logs := lv.fe.logs.Logs[lv.spanID]
	if logs == nil || logs.UsedHeight() == 0 {
		return
	}
	logs.SetPrefix(lv.logPrefix)
	height := lv.height
	if height <= 0 {
		height = logs.UsedHeight()
	}
	if trimmed := logs.UsedHeight() - height; trimmed > 0 {
		trimBuf := new(strings.Builder)
		trimOut := NewOutput(trimBuf, termenv.WithProfile(lv.fe.profile))
		lv.fe.writeLogTrimHeader(trimOut, lv.trimPrefix, trimmed)
		ctx.Lines(strings.Split(strings.TrimSuffix(trimBuf.String(), "\n"), "\n")...)
	}
	logs.SetHeight(height)
	view := logs.View()
	if view == "" {
		return
	}
	ctx.Lines(strings.Split(strings.TrimSuffix(view, "\n"), "\n")...)
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
