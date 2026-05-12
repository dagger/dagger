package idtui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// LogFocusHandle is a small focusable affordance for a span's logs. It keeps
// log focus/zoom discoverable without embedding a full pager inline.
type LogFocusHandle struct {
	tuist.Compo

	Profile termenv.Profile

	spanID dagui.SpanID
	title  string
	logs   *Vterm

	focused  bool
	inputSig string
}

var (
	_ tuist.Component  = (*LogFocusHandle)(nil)
	_ tuist.Focusable  = (*LogFocusHandle)(nil)
	_ tuist.Dismounter = (*LogFocusHandle)(nil)
)

func (h *LogFocusHandle) Name() string {
	if h.title != "" {
		return "LogFocusHandle(" + h.title + ")"
	}
	return "LogFocusHandle"
}

func (h *LogFocusHandle) SetInputs(span *dagui.Span, logs *Vterm, title string) {
	var spanID dagui.SpanID
	if span != nil {
		spanID = span.ID
	}
	sig := title + "|" + spanID.String()
	if logs != nil {
		sig += fmt.Sprintf("|%d", logs.UsedHeight())
	}
	if h.spanID == spanID && h.title == title && h.logs == logs && h.inputSig == sig {
		return
	}
	h.spanID = spanID
	h.title = title
	h.logs = logs
	h.inputSig = sig
	h.Update()
}

func (h *LogFocusHandle) SetFocused(_ tuist.Context, focused bool) {
	if h.focused != focused {
		h.focused = focused
		h.Update()
	}
}

func (h *LogFocusHandle) OnDismount() {
	h.focused = false
}

func (h *LogFocusHandle) Render(ctx tuist.Context) {
	outBuf := new(strings.Builder)
	out := NewOutput(outBuf, termenv.WithProfile(h.Profile))

	width := max(ctx.Width, 1)
	used := 0
	if h.logs != nil {
		used = h.logs.UsedHeight()
	}

	selector := " "
	if h.focused {
		selector = CaretRightFilled
	}
	label := "Logs"
	if h.title != "" {
		label += " · " + h.title
	}
	meta := fmt.Sprintf("%d lines", used)
	if used == 1 {
		meta = "1 line"
	}

	prefix := selector + " " + Diamond + " "
	available := max(width-lipgloss.Width(prefix), 1)
	text := clipPlain(label+" · "+meta+" · press L to open", available)

	if h.focused {
		fmt.Fprint(out, sidebarSelectedSegment(out, prefix, termenv.ANSIWhite, false, false))
		fmt.Fprint(out, sidebarSelectedSegment(out, text, termenv.ANSIWhite, true, false))
		visible := prefix + text
		if pad := width - lipgloss.Width(visible); pad > 0 {
			fmt.Fprint(out, sidebarSelectedSegment(out, strings.Repeat(" ", pad), nil, false, false))
		}
	} else {
		fmt.Fprint(out, out.String(selector).Foreground(termenv.ANSIBrightBlack))
		fmt.Fprint(out, " ")
		fmt.Fprint(out, out.String(Diamond).Foreground(termenv.ANSIBrightBlack))
		fmt.Fprint(out, " ")
		fmt.Fprint(out, out.String(text).Foreground(termenv.ANSIBrightBlack))
	}
	ctx.Line(outBuf.String())
}

// LogPagerView renders a single Vterm as a fullscreen, focusable log pager.
// Search is powered by Vterm/midterm's native search/highlight support.
type LogPagerView struct {
	tuist.Compo

	Profile termenv.Profile
	SpanID  dagui.SpanID
	Title   string
	Logs    *Vterm

	focused bool

	SearchQuery string
	SearchIndex int
	SearchCount int
}

var (
	_ tuist.Component  = (*LogPagerView)(nil)
	_ tuist.Focusable  = (*LogPagerView)(nil)
	_ tuist.Dismounter = (*LogPagerView)(nil)
)

func (p *LogPagerView) Name() string {
	if p.Title != "" {
		return "LogPager(" + p.Title + ")"
	}
	return "LogPager"
}

func (p *LogPagerView) SetFocused(_ tuist.Context, focused bool) {
	if p.focused != focused {
		p.focused = focused
		p.Update()
	}
}

func (p *LogPagerView) OnDismount() {
	p.focused = false
	if p.Logs != nil {
		p.Logs.SetSearchHighlight("", -1)
	}
}

func (p *LogPagerView) Render(ctx tuist.Context) {
	outBuf := new(strings.Builder)
	out := NewOutput(outBuf, termenv.WithProfile(p.Profile))

	width := max(ctx.Width, 1)
	height := ctx.Height
	if height <= 0 {
		height = max(ctx.ScreenHeight()-1, 1)
	}
	height = max(height, 1)

	title := "Logs"
	if p.Title != "" {
		title += " · " + p.Title
	}
	var meta []string
	if p.Logs != nil {
		used := p.Logs.UsedHeight()
		if used == 1 {
			meta = append(meta, "1 line")
		} else {
			meta = append(meta, fmt.Sprintf("%d lines", used))
		}
		meta = append(meta, fmt.Sprintf("%.0f%%", p.Logs.ScrollPercent()*100))
	}
	if p.SearchQuery != "" {
		if p.SearchCount > 0 {
			meta = append(meta, fmt.Sprintf("/%s %d/%d", p.SearchQuery, p.SearchIndex+1, p.SearchCount))
		} else {
			meta = append(meta, fmt.Sprintf("/%s 0", p.SearchQuery))
		}
	}
	if len(meta) > 0 {
		title += " · " + strings.Join(meta, " · ")
	}
	if p.focused {
		title = hl(out.String(clipPlain(title, width)).Foreground(termenv.ANSIWhite).Bold()).String()
	} else {
		title = out.String(clipPlain(title, width)).Foreground(termenv.ANSIWhite).Bold().String()
	}

	lines := []string{
		title,
		out.String(strings.Repeat(HorizBar, max(width, 0))).Foreground(termenv.ANSIBrightBlack).Faint().String(),
	}
	if len(lines) >= height {
		ctx.Lines(cropLines(lines, height)...)
		return
	}

	if p.Logs == nil || p.Logs.UsedHeight() == 0 {
		lines = append(lines, out.String("No logs.").Foreground(termenv.ANSIBrightBlack).String())
		ctx.Lines(cropLines(lines, height)...)
		return
	}

	logHeight := max(height-len(lines), 1)
	p.Logs.SetPrefix("")
	p.Logs.SetWidth(width)
	p.Logs.SetHeight(logHeight)
	view := strings.TrimSuffix(p.Logs.View(), "\n")
	if view == "" {
		lines = append(lines, out.String("No logs.").Foreground(termenv.ANSIBrightBlack).String())
	} else {
		lines = append(lines, strings.Split(view, "\n")...)
	}
	ctx.Lines(cropLines(lines, height)...)
}

func (p *LogPagerView) ScrollBy(delta int) {
	if p.Logs == nil {
		return
	}
	p.Logs.ScrollBy(delta)
	p.Update()
}

func (p *LogPagerView) ScrollPage(deltaPages int) {
	if p.Logs == nil {
		return
	}
	p.Logs.ScrollPage(deltaPages)
	p.Update()
}

func (p *LogPagerView) ScrollToTop() {
	if p.Logs == nil {
		return
	}
	p.Logs.ScrollToTop()
	p.Update()
}

func (p *LogPagerView) ScrollToBottom() {
	if p.Logs == nil {
		return
	}
	p.Logs.ScrollToBottom()
	p.Update()
}

func (p *LogPagerView) SetSearch(query string) {
	p.SearchQuery = strings.TrimSpace(query)
	p.SearchIndex = -1
	p.SearchCount = 0
	if p.Logs == nil || p.SearchQuery == "" {
		if p.Logs != nil {
			p.Logs.Search("", -1)
		}
		p.Update()
		return
	}
	count, row := p.Logs.Search(p.SearchQuery, 0)
	p.SearchCount = count
	if count > 0 {
		p.SearchIndex = 0
		p.Logs.ScrollToRow(row)
	}
	p.Update()
}

func (p *LogPagerView) RefreshSearch() {
	if p.Logs == nil || p.SearchQuery == "" {
		return
	}
	idx := p.SearchIndex
	if idx < 0 {
		idx = 0
	}
	count, row := p.Logs.Search(p.SearchQuery, idx)
	p.SearchCount = count
	if count == 0 {
		p.SearchIndex = -1
		return
	}
	if p.SearchIndex < 0 || p.SearchIndex >= count {
		p.SearchIndex = 0
		_, row = p.Logs.Search(p.SearchQuery, p.SearchIndex)
	}
	if row >= 0 {
		p.Logs.ScrollToRow(row)
	}
}

func (p *LogPagerView) SearchNext() {
	if p.Logs == nil || p.SearchQuery == "" || p.SearchCount == 0 {
		return
	}
	p.SearchIndex++
	if p.SearchIndex >= p.SearchCount {
		p.SearchIndex = 0
	}
	_, row := p.Logs.Search(p.SearchQuery, p.SearchIndex)
	if row >= 0 {
		p.Logs.ScrollToRow(row)
	}
	p.Update()
}

func (p *LogPagerView) SearchPrev() {
	if p.Logs == nil || p.SearchQuery == "" || p.SearchCount == 0 {
		return
	}
	p.SearchIndex--
	if p.SearchIndex < 0 {
		p.SearchIndex = p.SearchCount - 1
	}
	_, row := p.Logs.Search(p.SearchQuery, p.SearchIndex)
	if row >= 0 {
		p.Logs.ScrollToRow(row)
	}
	p.Update()
}

func (fe *frontendPretty) renderLogPager(ctx tuist.Context) {
	if fe.logPager == nil {
		return
	}
	height := ctx.ScreenHeight() - 1 // keymap sibling
	if fe.logSearchInput != nil {
		height--
	}
	if height <= 0 {
		height = ctx.Height
	}
	if height <= 0 {
		height = 1
	}
	fe.RenderChild(ctx.Resize(ctx.Width, height), fe.logPager)
}

func (fe *frontendPretty) spanHasLogs(span *dagui.Span) bool {
	if span == nil || fe.logs == nil {
		return false
	}
	logs := fe.logs.Logs[span.ID]
	return logs != nil && logs.UsedHeight() > 0
}

func (fe *frontendPretty) currentLogSpan() *dagui.Span {
	if fe.testsMode && fe.fullscreenTests != nil {
		return fe.fullscreenTests.CurrentActionSpan()
	}
	if fe.FocusedSpan.IsValid() {
		return fe.db.Spans.Map[fe.FocusedSpan]
	}
	return nil
}

func (fe *frontendPretty) openFocusedLogs() {
	span := fe.currentLogSpan()
	if !fe.spanHasLogs(span) {
		return
	}
	if fe.testsMode && fe.fullscreenTests != nil && fe.fullscreenTests.focusArea == testFocusSidebar {
		// Treat L from the tests sidebar as an explicit move to the reusable log
		// handle before opening the pager, so popping the pager restores that
		// immediate state.
		fe.fullscreenTests.focusSelectedLogHandle(fe, span)
	}
	fe.openLogPager(span, fe.makeLogPagerReturnFocus())
}

func (fe *frontendPretty) openLogPager(span *dagui.Span, restore func()) {
	if !fe.spanHasLogs(span) {
		return
	}
	logs := fe.logs.Logs[span.ID]
	fe.logPager = &LogPagerView{
		Profile: fe.profile,
		SpanID:  span.ID,
		Title:   span.Name,
		Logs:    logs,
	}
	fe.logPagerReturn = restore
	fe.applyTuistFocus()
	if fe.keymapBar != nil {
		fe.keymapBar.Update()
	}
	fe.Update()
}

func (fe *frontendPretty) closeLogPager() {
	if fe.logPager == nil {
		return
	}
	fe.exitLogPagerSearchMode()
	restore := fe.logPagerReturn
	fe.logPager = nil
	fe.logPagerReturn = nil
	if restore != nil {
		restore()
	} else {
		fe.applyTuistFocus()
	}
	if fe.keymapBar != nil {
		fe.keymapBar.Update()
	}
	fe.Update()
}

func (fe *frontendPretty) makeLogPagerReturnFocus() func() {
	if fe.testsMode && fe.fullscreenTests != nil {
		return fe.fullscreenTests.makeReturnFocus(fe)
	}
	spanID := fe.FocusedSpan
	return func() {
		if spanID.IsValid() {
			fe.FocusedSpan = spanID
		}
		fe.applyTuistFocus()
	}
}

func (fe *frontendPretty) enterLogPagerSearchMode() {
	if fe.logPager == nil || fe.logSearchInput != nil {
		return
	}
	fe.logSearchInput = tuist.NewTextInput("")
	fe.logSearchInput.Prompt = "/"
	fe.logSearchInput.OnSubmit = func(ctx tuist.Context, value string) bool {
		fe.confirmLogPagerSearch(value)
		return true
	}
	fe.logSearchInput.KeyInterceptor = fe.interceptLogPagerSearchKey

	fe.tui.RemoveChild(fe.keymapBar)
	fe.tui.AddChild(fe.logSearchInput)
	fe.tui.AddChild(fe.keymapBar)
	fe.tui.SetFocus(fe.logSearchInput)
	fe.tui.SetShowHardwareCursor(true)
	if fe.keymapBar != nil {
		fe.keymapBar.Update()
	}
	fe.Update()
}

func (fe *frontendPretty) exitLogPagerSearchMode() {
	if fe.logSearchInput == nil {
		return
	}
	fe.tui.RemoveChild(fe.logSearchInput)
	fe.logSearchInput = nil
	fe.tui.SetShowHardwareCursor(fe.textInput != nil && fe.editlineFocused)
	if fe.logPager != nil {
		fe.tui.SetFocus(fe.logPager)
	}
	if fe.keymapBar != nil {
		fe.keymapBar.Update()
	}
}

func (fe *frontendPretty) confirmLogPagerSearch(query string) {
	fe.exitLogPagerSearchMode()
	if fe.logPager == nil {
		return
	}
	fe.logPager.SetSearch(query)
	if fe.keymapBar != nil {
		fe.keymapBar.Update()
	}
	fe.Update()
}

func (fe *frontendPretty) interceptLogPagerSearchKey(_ tuist.Context, ev uv.KeyPressEvent) bool {
	keyStr := uv.Key(ev).String()
	if keyStr == "esc" {
		fe.exitLogPagerSearchMode()
		fe.Update()
		return true
	}
	return false
}

func (fe *frontendPretty) updateLogPagerForLogs(spanID dagui.SpanID) {
	if fe.logPager == nil || fe.logPager.SpanID != spanID {
		return
	}
	fe.logPager.RefreshSearch()
	fe.logPager.Update()
	if fe.keymapBar != nil {
		fe.keymapBar.Update()
	}
}
