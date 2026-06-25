package idtui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

type testSidebarRowKind uint8

type testFocusArea uint8

const (
	testSidebarNode testSidebarRowKind = iota
	testSidebarPassedGroup
)

const (
	testFocusSidebar testFocusArea = iota
	testFocusChildren
)

type testSidebarRow struct {
	kind  testSidebarRowKind
	node  *dagui.TestNode
	depth int

	key      string
	counts   dagui.TestCounts
	expanded bool
}

func (row testSidebarRow) id() string {
	if row.kind == testSidebarPassedGroup {
		return "passed:" + row.key
	}
	if row.node == nil {
		return ""
	}
	return "node:" + string(row.node.ID)
}

func (row testSidebarRow) testCount() int {
	if row.kind == testSidebarPassedGroup {
		if row.expanded {
			return 0
		}
		return row.counts.Total()
	}
	if row.node != nil && row.node.Kind == dagui.TestNodeCase {
		return 1
	}
	return 0
}

type TestView struct {
	tuist.Compo

	Profile      termenv.Profile
	View         func() *dagui.TestView
	Logs         map[dagui.SpanID]*Vterm
	SpanChildren func(*dagui.Span) tuist.Component

	// TraceID, when set (by 'dagger trace'), lets a failing entry's capped log
	// tail point at 'dagger cloud logs <trace> <span>' for the full output.
	TraceID string

	sidebar *testSidebarView

	// MaxHeight caps the rendered height. A zero value means fullscreen mode:
	// use the terminal height, leaving room for the keymap sibling.
	MaxHeight int
	ScopeName string

	// ListOnly forces passive embedded rendering: no selected row and no detail
	// pane, even if focus were accidentally routed to this component.
	ListOnly bool
	// SummaryIndent is used by ListOnly test summaries. Anchored inline reports
	// use it to offset beneath a trace row; global reports keep it at zero.
	SummaryIndent int
	// SummaryLogLines caps inline logs per failing/skipped summary entry.
	SummaryLogLines int
	// ShowTestViewerHint renders the pretty-live "T inspect" affordance next to
	// the TESTS summary heading. Final/non-pretty reports leave it disabled.
	ShowTestViewerHint bool

	OnFocusSpan func(*dagui.Span)

	// ForceInteractive keeps fullscreen tests interactive while Tuist focus is on
	// a descendant in the detail pane. Embedded test views remain passive through
	// ListOnly.
	ForceInteractive bool

	// focused is true only while Tuist has keyboard focus directly on this view.
	// Fullscreen tests also render interactively via ForceInteractive while a
	// child detail component has focus.
	focused bool

	focusArea       testFocusArea
	focusedChildren *TestSpanChildrenView

	focusedRow           string
	expandedPassedGroups map[string]bool
}

var (
	_ tuist.Component  = (*TestView)(nil)
	_ tuist.Focusable  = (*TestView)(nil)
	_ tuist.Dismounter = (*TestView)(nil)
)

const testSidebarIndent = 2

var testSidebarRowBG termenv.Color = termenv.ANSIBrightBlack

type testSidebarView struct {
	tuist.Compo

	tv          *TestView
	view        *dagui.TestView
	rows        []testSidebarRow
	selectedIdx int

	rowByLine  map[int]int
	inputSig   string
	hovered    bool
	hoveredIdx int
}

var (
	_ tuist.Component    = (*testSidebarView)(nil)
	_ tuist.MouseEnabled = (*testSidebarView)(nil)
	_ tuist.Hoverable    = (*testSidebarView)(nil)
)

func (tv *TestView) Name() string {
	if tv.ScopeName != "" {
		return "TestView(" + tv.ScopeName + ")"
	}
	return "TestView"
}

func (tv *TestView) currentView() *dagui.TestView {
	if tv == nil || tv.View == nil {
		return nil
	}
	return tv.View()
}

func (tv *TestView) SetFocused(_ tuist.Context, focused bool) {
	if tv.focused != focused {
		tv.focused = focused
		tv.Update()
	}
}

func (tv *TestView) OnDismount() {
	tv.focused = false
}

func (tv *TestView) Render(ctx tuist.Context) {
	view := tv.currentView()
	interactive := !tv.ListOnly && (tv.focused || tv.ForceInteractive)
	rows := tv.flattenTestRows(view)
	selectedIdx := -1
	if interactive {
		rows, selectedIdx = tv.ensureFocusedTest(view)
	} else if len(rows) == 0 {
		tv.focusedRow = ""
	}

	width := max(ctx.Width, 1)
	leftWidth := width / 3
	if leftWidth < 28 {
		leftWidth = min(width, 28)
	}
	if leftWidth > 44 {
		leftWidth = 44
	}
	rightWidth := width - leftWidth - 3
	if rightWidth < 20 && width > 24 {
		leftWidth = max(width-23, 12)
		rightWidth = width - leftWidth - 3
	}
	if rightWidth < 1 {
		rightWidth = 1
	}

	viewportHeight := ctx.Height
	if viewportHeight <= 0 {
		if tv.MaxHeight > 0 {
			viewportHeight = tv.MaxHeight
		} else {
			// Leave room for the keymap sibling. Filling the rest of the
			// screen keeps Tuist's mouse coordinates aligned with rows.
			viewportHeight = max(ctx.ScreenHeight()-1, 1)
		}
	} else if tv.MaxHeight > 0 {
		viewportHeight = min(viewportHeight, tv.MaxHeight)
	}
	viewportHeight = max(viewportHeight, 1)

	outBuf := new(strings.Builder)
	out := NewOutput(outBuf, termenv.WithProfile(tv.Profile))

	if len(rows) == 0 {
		ctx.Line(out.String("No tests discovered yet").Foreground(termenv.ANSIBrightBlack).String())
		return
	}

	if !interactive {
		ctx.Lines(tv.renderSidebarLines(out, view, rows, -1, width, viewportHeight)...)
		return
	}

	selected := rows[selectedIdx]
	left := tv.renderInteractiveSidebar(ctx.Resize(leftWidth, viewportHeight), view, rows, selectedIdx)
	right := tv.renderDetailLines(ctx, out, selected, rightWidth, viewportHeight)

	for i := range viewportHeight {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		ctx.Line(renderTestPaneLine(out, l, r, leftWidth))
	}
}

func (tv *TestView) renderInteractiveSidebar(ctx tuist.Context, view *dagui.TestView, rows []testSidebarRow, selectedIdx int) []string {
	if tv.sidebar == nil {
		tv.sidebar = &testSidebarView{tv: tv, hoveredIdx: -1}
	}
	tv.sidebar.setInputs(view, rows, selectedIdx, ctx.Height)
	return tv.RenderChildResult(ctx, tv.sidebar).Lines
}

func (s *testSidebarView) setInputs(view *dagui.TestView, rows []testSidebarRow, selectedIdx, height int) {
	s.view = view
	s.rows = rows
	s.selectedIdx = selectedIdx
	sig := testSidebarInputSignature(view, rows, selectedIdx, height)
	if sig != s.inputSig {
		s.inputSig = sig
		s.Update()
	}
}

func testSidebarInputSignature(view *dagui.TestView, rows []testSidebarRow, selectedIdx, height int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "h=%d selected=%d", height, selectedIdx)
	if view != nil {
		fmt.Fprintf(&b, " total=%d/%d/%d/%d", view.Counts.Failing, view.Counts.Running, view.Counts.Passing, view.Counts.Skipped)
	}
	for _, row := range rows {
		fmt.Fprintf(&b, "|%s d=%d e=%t c=%d/%d/%d/%d", row.id(), row.depth, row.expanded, row.counts.Failing, row.counts.Running, row.counts.Passing, row.counts.Skipped)
		if row.node != nil {
			fmt.Fprintf(&b, " k=%d cat=%d name=%s n=%d/%d/%d/%d", row.node.Kind, row.node.Category, testNodeDisplayName(row.node), row.node.Counts.Failing, row.node.Counts.Running, row.node.Counts.Passing, row.node.Counts.Skipped)
		}
	}
	return b.String()
}

func (s *testSidebarView) Render(ctx tuist.Context) {
	if s.tv == nil {
		return
	}

	outBuf := new(strings.Builder)
	out := NewOutput(outBuf, termenv.WithProfile(s.tv.Profile))
	hoveredIdx := -1
	if s.hovered {
		hoveredIdx = s.hoveredIdx
	}
	lines, rowByLine := s.tv.renderSidebarLinesWithHover(out, s.view, s.rows, s.selectedIdx, hoveredIdx, ctx.Width, ctx.Height)
	s.rowByLine = rowByLine
	for i := range lines {
		lines[i] = padANSI(lines[i], ctx.Width)
	}
	ctx.Lines(lines...)
}

func (s *testSidebarView) HandleMouse(ctx tuist.Context, ev tuist.MouseEvent) bool {
	if s.tv == nil || (!s.tv.focused && !s.tv.ForceInteractive) || s.tv.ListOnly {
		return false
	}

	switch ev.MouseEvent.(type) {
	case uv.MouseMotionEvent:
		idx := s.rowIndexAt(ev.Row)
		if s.hoveredIdx != idx {
			s.hoveredIdx = idx
			s.Update()
		}
		return true

	case uv.MouseClickEvent:
		if ev.Mouse().Button != uv.MouseLeft {
			return true
		}
		idx := s.rowIndexAt(ev.Row)
		if idx < 0 || idx >= len(s.rows) {
			return true
		}
		row := s.rows[idx]
		s.tv.focusArea = testFocusSidebar
		s.tv.focusedChildren = nil
		s.tv.focusSidebarRow(row)
		ctx.SetFocus(s.tv)
		if row.kind == testSidebarPassedGroup {
			if s.tv.expandedPassedGroups == nil {
				s.tv.expandedPassedGroups = make(map[string]bool)
			}
			s.tv.expandedPassedGroups[row.key] = !row.expanded
		}
		s.tv.Update()
		s.Update()
		return true

	case uv.MouseWheelEvent:
		switch ev.Mouse().Button {
		case uv.MouseWheelUp:
			s.tv.GoUp()
		case uv.MouseWheelDown:
			s.tv.GoDown()
		}
		return true
	}

	return false
}

func (s *testSidebarView) SetHovered(_ tuist.Context, hovered bool) {
	if s.hovered == hovered {
		return
	}
	s.hovered = hovered
	if !hovered {
		s.hoveredIdx = -1
	}
	s.Update()
}

func (s *testSidebarView) rowIndexAt(line int) int {
	if s.rowByLine == nil {
		return -1
	}
	idx, ok := s.rowByLine[line]
	if !ok {
		return -1
	}
	return idx
}

func (tv *TestView) FocusedNode() *dagui.TestNode {
	rows, idx := tv.ensureFocusedTest(tv.currentView())
	if idx < 0 || idx >= len(rows) || rows[idx].kind != testSidebarNode {
		return nil
	}
	return rows[idx].node
}

func (tv *TestView) FocusedSpan() *dagui.Span {
	return testTUISpan(tv.FocusedNode())
}

func (tv *TestView) FocusedPassedGroupExpanded() (bool, bool) {
	rows, idx := tv.ensureFocusedTest(tv.currentView())
	if idx < 0 || idx >= len(rows) || rows[idx].kind != testSidebarPassedGroup {
		return false, false
	}
	return rows[idx].expanded, true
}

func (tv *TestView) GoStart() {
	rows, _ := tv.ensureFocusedTest(tv.currentView())
	if len(rows) > 0 {
		tv.focusSidebarRow(rows[0])
		tv.Update()
	}
}

func (tv *TestView) GoEnd() {
	rows, _ := tv.ensureFocusedTest(tv.currentView())
	if len(rows) > 0 {
		tv.focusSidebarRow(rows[len(rows)-1])
		tv.Update()
	}
}

func (tv *TestView) GoUp() {
	rows, idx := tv.ensureFocusedTest(tv.currentView())
	if idx > 0 {
		tv.focusSidebarRow(rows[idx-1])
		tv.Update()
	}
}

func (tv *TestView) GoDown() {
	rows, idx := tv.ensureFocusedTest(tv.currentView())
	if idx >= 0 && idx+1 < len(rows) {
		tv.focusSidebarRow(rows[idx+1])
		tv.Update()
	}
}

func (tv *TestView) ToggleFocusedGroup() bool {
	rows, idx := tv.ensureFocusedTest(tv.currentView())
	if idx < 0 || idx >= len(rows) || rows[idx].kind != testSidebarPassedGroup {
		return false
	}
	if tv.expandedPassedGroups == nil {
		tv.expandedPassedGroups = make(map[string]bool)
	}
	tv.expandedPassedGroups[rows[idx].key] = !tv.expandedPassedGroups[rows[idx].key]
	tv.focusedRow = rows[idx].id()
	tv.Update()
	return true
}

func (tv *TestView) ensureFocusedTest(view *dagui.TestView) ([]testSidebarRow, int) {
	rows := tv.flattenTestRows(view)
	if len(rows) == 0 {
		tv.focusedRow = ""
		return rows, -1
	}
	for i, row := range rows {
		if row.id() == tv.focusedRow {
			tv.focusSidebarRow(row)
			return rows, i
		}
	}
	tv.focusSidebarRow(rows[0])
	return rows, 0
}

func (tv *TestView) focusSidebarRow(row testSidebarRow) {
	tv.focusedRow = row.id()
	if row.kind == testSidebarNode && tv.OnFocusSpan != nil {
		tv.OnFocusSpan(testTUISpan(row.node))
	}
}

func (tv *TestView) focusSidebar(fe *frontendPretty) {
	tv.focusArea = testFocusSidebar
	tv.focusedChildren = nil
	if span := tv.FocusedSpan(); span != nil && tv.OnFocusSpan != nil {
		tv.OnFocusSpan(span)
	}
	if fe != nil && fe.tui != nil {
		fe.tui.SetFocus(tv)
	}
	tv.Update()
}

func (tv *TestView) FocusedNodeCanFocusDetail() bool {
	if tv.focusArea != testFocusSidebar {
		return tv.focusArea == testFocusChildren
	}
	rows, idx := tv.ensureFocusedTest(tv.currentView())
	if idx < 0 || idx >= len(rows) || rows[idx].kind != testSidebarNode {
		return false
	}
	node := rows[idx].node
	if node == nil || node.Kind == dagui.TestNodeVirtualSuite || node.Span == nil {
		return false
	}
	return node.Span.ChildSpans != nil && len(node.Span.ChildSpans.Order) > 0
}

func (tv *TestView) CurrentActionSpan() *dagui.Span {
	if tv.focusArea == testFocusChildren && tv.focusedChildren != nil {
		if span := tv.focusedChildren.FocusedSpan(); span != nil {
			return span
		}
	}
	node := tv.FocusedNode()
	if node == nil || node.Kind == dagui.TestNodeVirtualSuite {
		return nil
	}
	return node.Span
}

func (tv *TestView) CurrentActionTitle() (string, dagui.TestCategory, bool) {
	if tv.focusArea == testFocusChildren && tv.focusedChildren != nil {
		if span := tv.focusedChildren.FocusedSpan(); span != nil {
			return span.Name, dagui.TestCategoryPassing, false
		}
	}
	node := tv.FocusedNode()
	if node == nil || node.Kind == dagui.TestNodeVirtualSuite {
		return "", dagui.TestCategoryPassing, false
	}
	return testNodeTitleName(node), node.Category, true
}

func (tv *TestView) makeReturnFocus(fe *frontendPretty) func() {
	area := tv.focusArea
	rowID := tv.focusedRow
	children := tv.focusedChildren
	var childSpan dagui.SpanID
	if children != nil {
		childSpan = children.focusedSpan
	}
	return func() {
		tv.focusedRow = rowID
		if area == testFocusChildren {
			if children != nil && childSpan.IsValid() && children.FocusSpan(fe, childSpan) {
				tv.focusArea = testFocusChildren
				tv.focusedChildren = children
				tv.Update()
				return
			}
		}
		tv.focusSidebar(fe)
	}
}

func (tv *TestView) childViewForSpan(span *dagui.Span) *TestSpanChildrenView {
	if span == nil || tv.SpanChildren == nil {
		return nil
	}
	child := tv.SpanChildren(span)
	view, _ := child.(*TestSpanChildrenView)
	return view
}

func (tv *TestView) renderSidebarLines(out *termenv.Output, view *dagui.TestView, rows []testSidebarRow, selectedIdx, width, height int) []string {
	lines, _ := tv.renderSidebarLinesWithHover(out, view, rows, selectedIdx, -1, width, height)
	return lines
}

func (tv *TestView) renderSidebarLinesWithHover(out *termenv.Output, view *dagui.TestView, rows []testSidebarRow, selectedIdx, hoveredIdx, width, height int) ([]string, map[int]int) {
	var lines []string
	rowByLine := make(map[int]int)
	if tv.ListOnly {
		return tv.renderTestSummaryLines(out, view, width, height), rowByLine
	}
	lines = append(lines, renderTestInspectorHeader(out, view, width))
	lines = append(lines, out.String(strings.Repeat(HorizBar, max(width, 0))).Foreground(termenv.ANSIBrightBlack).Faint().String())

	listHeight := max(height-len(lines), 0)
	if listHeight == 0 || len(rows) == 0 {
		return cropLines(lines, height), rowByLine
	}

	start := 0
	if selectedIdx >= listHeight {
		start = selectedIdx - listHeight/2
	}
	if start+listHeight > len(rows) {
		start = max(len(rows)-listHeight, 0)
	}

	var end int
	var topMarker, bottomMarker bool
	for {
		topMarker = start > 0
		slots := listHeight
		if topMarker {
			slots--
		}
		bottomMarker = start+max(slots, 0) < len(rows)
		if bottomMarker {
			slots--
		}
		if slots < 1 {
			if topMarker {
				topMarker = false
				slots++
			}
			if slots < 1 && bottomMarker {
				bottomMarker = false
				slots++
			}
		}
		slots = max(slots, 1)
		end = min(start+slots, len(rows))
		if selectedIdx < start && start > 0 {
			start--
			continue
		}
		if selectedIdx >= end && end < len(rows) {
			start++
			continue
		}
		break
	}

	if topMarker && len(lines) < height {
		lines = append(lines, out.String(fmt.Sprintf("… %d above", start)).Foreground(termenv.ANSIBrightBlack).Faint().String())
	}
	for i := start; i < end && len(lines) < height; i++ {
		rowByLine[len(lines)] = i
		lines = append(lines, tv.renderSidebarRow(out, rows[i], i == selectedIdx, i == hoveredIdx, width))
	}
	if bottomMarker && len(lines) < height {
		hiddenTests := countSidebarTests(rows[end:])
		label := fmt.Sprintf("... %d more tests ...", hiddenTests)
		if hiddenTests == 0 {
			label = fmt.Sprintf("... %d more items ...", len(rows)-end)
		}
		lines = append(lines, out.String(clipPlain(label, width)).Foreground(termenv.ANSIBrightBlack).Faint().String())
	}
	return cropLines(lines, height), rowByLine
}

func renderTestInspectorHeader(out TermOutput, view *dagui.TestView, width int) string {
	heading := out.String("TESTS").Bold().String()
	if view == nil {
		return clipTestSummaryLine(heading, width)
	}
	for _, part := range renderTestCountParts(out, view.Counts) {
		candidate := heading + "  " + part
		if width > 0 && lipgloss.Width(candidate) > width {
			break
		}
		heading = candidate
	}
	return clipTestSummaryLine(heading, width)
}

func renderTestCountParts(out TermOutput, counts dagui.TestCounts) []string {
	var parts []string
	add := func(count int, icon string, color termenv.Color, label string) {
		if count == 0 {
			return
		}
		parts = append(parts, out.String(fmt.Sprintf("%s %d %s", icon, count, label)).Foreground(color).String())
	}
	add(counts.Failing, IconFailure, termenv.ANSIRed, "failed")
	add(counts.Skipped, IconSkipped, termenv.ANSIBrightBlack, "skipped")
	add(counts.Passing, IconSuccess, termenv.ANSIGreen, "passed")
	add(counts.Running, DotHalf, termenv.ANSIYellow, "running")
	return parts
}

func (tv *TestView) renderSidebarRow(out *termenv.Output, row testSidebarRow, selected, hovered bool, width int) string {
	if row.kind == testSidebarPassedGroup {
		return tv.renderPassedGroupSidebarRow(out, row, selected, hovered, width)
	}
	if selected || hovered {
		return tv.renderHighlightedSidebarRow(out, row, selected, width)
	}
	node := row.node
	color := testCategoryColor(node.Category)
	selector := " "
	iconStyle := out.String(testCategoryIcon(node.Category)).Foreground(color)
	indent := testSidebarIndentString(row.depth)
	count := ""
	countWidth := 0
	if node.Kind != dagui.TestNodeCase || len(node.Children) > 0 {
		count = out.String(fmt.Sprintf(" %d", node.Counts.Total())).Foreground(termenv.ANSIBrightBlack).Faint().String()
		countWidth = lipgloss.Width(fmt.Sprintf(" %d", node.Counts.Total()))
	}
	nameWidth := max(width-4-lipgloss.Width(indent)-countWidth, 1)
	nameStyle := out.String(clipPlain(testNodeDisplayName(node), nameWidth)).Foreground(testNodeNameColor(node))
	return selector + " " + iconStyle.String() + " " + indent + nameStyle.String() + count
}

func (tv *TestView) renderTestSummaryLines(out TermOutput, view *dagui.TestView, width, height int) []string {
	if tv.testSummaryFinal() {
		width = 0
	}
	prefix := strings.Repeat(" ", max(tv.SummaryIndent, 0))
	lines := []string{tv.renderTestSummaryHeader(out, prefix, width)}
	if view == nil {
		return cropLines(lines, height)
	}
	entries := collectTestSummaryEntries(view)
	addedContent := false
	lastMultiline := false
	appendEntry := func(entry testSummaryEntry) {
		entryLines := tv.renderTestSummaryEntry(out, entry, width)
		multiline := len(entryLines) > 1
		if addedContent && (lastMultiline || multiline) {
			lines = append(lines, "")
		}
		lines = append(lines, entryLines...)
		addedContent = true
		lastMultiline = multiline
	}
	for _, entry := range entries.failing {
		appendEntry(entry)
	}
	for _, entry := range entries.skipped {
		appendEntry(entry)
	}
	for _, entry := range entries.running {
		appendEntry(entry)
	}
	if len(entries.passing) > 0 {
		if addedContent {
			lines = append(lines, "")
		}
		for _, entry := range entries.passing {
			lines = append(lines, tv.renderTestSummaryPassingSuite(out, entry, width))
		}
		addedContent = true
	}
	if counts := renderTestSummaryCounts(out, view.Counts, tv.SummaryIndent, width); len(counts) > 0 {
		if addedContent {
			lines = append(lines, "")
		}
		lines = append(lines, counts...)
	}
	return cropLines(lines, height)
}

func (tv *TestView) renderTestSummaryHeader(out TermOutput, prefix string, width int) string {
	heading := prefix + reportHeadingLine(out, "TESTS")
	if tv.ShowTestViewerHint && !tv.testSummaryFinal() {
		heading += " " + renderTestViewerHint(out)
	}
	return clipTestSummaryLine(heading, width)
}

func renderTestViewerHint(out TermOutput) string {
	return renderInspectKeyHint(out, "T")
}

func renderInspectKeyHint(out TermOutput, key string) string {
	return out.String(key).Foreground(termenv.ANSIBrightBlack).Bold().String() +
		out.String(" inspect").Foreground(termenv.ANSIBrightBlack).String()
}

func (tv *TestView) renderTestSummaryEntry(out TermOutput, entry testSummaryEntry, width int) []string {
	indent := strings.Repeat(" ", max(tv.SummaryIndent, 0)+2)
	icon := out.String(testCategoryIcon(entry.category)).Foreground(testCategoryColor(entry.category)).String()
	statusLabel := testSummaryStatus(entry)
	status := out.String(statusLabel).Foreground(testCategoryColor(entry.category)).String()
	label := entry.label
	if width > 0 {
		labelWidth := max(width-lipgloss.Width(indent)-lipgloss.Width(icon)-lipgloss.Width(status)-2, 1)
		label = clipPlain(label, labelWidth)
	}
	lines := []string{clipTestSummaryLine(indent+icon+" "+label+" "+status, width)}
	lines = append(lines, tv.renderTestSummaryLogs(out, entry, width)...)
	return lines
}

func (tv *TestView) renderTestSummaryPassingSuite(out TermOutput, entry testSummaryEntry, width int) string {
	indent := strings.Repeat(" ", max(tv.SummaryIndent, 0)+2)
	icon := out.String(IconSuccess).Foreground(termenv.ANSIGreen).String()
	count := out.String(fmt.Sprintf("%d passed", entry.counts.Passing)).Foreground(termenv.ANSIGreen).String()
	label := entry.label
	if width > 0 {
		labelWidth := max(width-lipgloss.Width(indent)-lipgloss.Width(icon)-lipgloss.Width(count)-3, 1)
		label = clipPlain(label, labelWidth)
	}
	return clipTestSummaryLine(indent+icon+" "+label+"  "+count, width)
}

func (tv *TestView) renderTestSummaryLogs(out TermOutput, entry testSummaryEntry, width int) []string {
	if entry.span == nil || tv.Logs == nil || tv.SummaryLogLines == 0 {
		return nil
	}
	if entry.category != dagui.TestCategoryFailing && entry.category != dagui.TestCategorySkipped {
		return nil
	}
	logs := tv.Logs[entry.span.ID]
	if logs == nil {
		return nil
	}
	final := tv.testSummaryFinal()
	indent := strings.Repeat(" ", max(tv.SummaryIndent, 0)+8)
	if !final {
		logs.SetWidth(max(width-lipgloss.Width(indent), 1))
	}
	usedHeight := logs.UsedHeight()
	if usedHeight == 0 {
		return nil
	}
	limit := tv.SummaryLogLines
	if limit < 0 || limit > usedHeight {
		limit = usedHeight
	}
	var buf strings.Builder
	var err error
	if final {
		err = logs.PrintRaw(&buf)
	} else {
		err = logs.Print(&buf)
	}
	if err != nil {
		return nil
	}
	rawLines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if final {
		// Anchor on the failure rather than an arbitrary tail, and point at the
		// full logs -- the same treatment the zoomed (--test) view uses.
		return errorWindowLines(out, rawLines, indent, tv.TraceID, cloudLogsTarget(entry.span))
	}

	// Live (interactive) summary: a small tail with a "more lines" footer.
	hidden := 0
	if len(rawLines) > limit {
		hidden = len(rawLines) - limit
		rawLines = rawLines[len(rawLines)-limit:]
	}
	textWidth := max(width-lipgloss.Width(indent), 1)
	lines := make([]string, 0, len(rawLines)+1)
	for _, line := range rawLines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, clipTestSummaryLine(indent+clipPlain(line, textWidth), width))
	}
	if len(lines) == 0 {
		return nil
	}
	if hidden > 0 {
		marker := out.String(fmt.Sprintf("... %d more log lines ...", hidden)).Foreground(termenv.ANSIBrightBlack).Faint().String()
		lines = append(lines, clipTestSummaryLine(indent+marker, width))
	}
	return lines
}

func renderTestSummaryCounts(out TermOutput, counts dagui.TestCounts, summaryIndent, width int) []string {
	indent := strings.Repeat(" ", max(summaryIndent, 0)+2)
	var lines []string
	for _, part := range renderTestCountParts(out, counts) {
		lines = append(lines, clipTestSummaryLine(indent+part, width))
	}
	if len(lines) == 0 {
		lines = append(lines, clipTestSummaryLine(indent+out.String("0 tests").Foreground(termenv.ANSIBrightBlack).Faint().String(), width))
	}
	return lines
}

func (tv *TestView) testSummaryFinal() bool {
	return tv.SummaryLogLines < 0
}

func clipTestSummaryLine(s string, width int) string {
	if width <= 0 {
		return s
	}
	return clipANSI(s, width)
}

func testSummaryStatus(entry testSummaryEntry) string {
	switch entry.category {
	case dagui.TestCategoryFailing, dagui.TestCategoryMixed:
		return "FAIL"
	case dagui.TestCategoryRunning:
		return "RUNNING"
	case dagui.TestCategorySkipped:
		return "SKIP"
	default:
		return "PASS"
	}
}

type testSummaryEntry struct {
	category dagui.TestCategory
	label    string
	span     *dagui.Span
	counts   dagui.TestCounts
}

type testSummaryEntries struct {
	failing []testSummaryEntry
	running []testSummaryEntry
	skipped []testSummaryEntry
	passing []testSummaryEntry
}

func collectTestSummaryEntries(view *dagui.TestView) testSummaryEntries {
	var entries testSummaryEntries
	addNonPassing := func(entry testSummaryEntry) {
		switch entry.category {
		case dagui.TestCategoryFailing:
			entries.failing = append(entries.failing, entry)
		case dagui.TestCategoryRunning:
			entries.running = append(entries.running, entry)
		case dagui.TestCategorySkipped:
			entries.skipped = append(entries.skipped, entry)
		}
	}
	var walk func(*dagui.TestNode)
	walk = func(node *dagui.TestNode) {
		if node == nil {
			return
		}
		if testSummaryPassingSuite(node) {
			entries.passing = append(entries.passing, testSummaryEntry{
				category: node.Category,
				label:    testSummarySuiteLabel(node),
				span:     testTUISpan(node),
				counts:   node.Counts,
			})
			return
		}
		switch node.Kind {
		case dagui.TestNodeCase:
			addNonPassing(testSummaryEntry{category: node.SelfCategory, label: testSummarySpanHierarchyLabel(node), span: node.Span})
		case dagui.TestNodeSuite:
			if node.Span != nil {
				category := node.Span.TestCategory()
				// A skipped suite with no test cases (e.g. a package with
				// nothing to run) is pure noise in the summary.
				if category != dagui.TestCategorySkipped || node.Counts.Total() > 0 {
					addNonPassing(testSummaryEntry{category: category, label: testSummarySuiteLabel(node), span: node.Span})
				}
			}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	for _, root := range view.Roots {
		walk(root)
	}
	return entries
}

func testSummaryPassingSuite(node *dagui.TestNode) bool {
	if node == nil {
		return false
	}
	if node.Kind != dagui.TestNodeSuite && node.Kind != dagui.TestNodeVirtualSuite {
		return false
	}
	return node.Category == dagui.TestCategoryPassing && node.Counts.Passing > 0
}

func testSummarySuiteLabel(node *dagui.TestNode) string {
	if node == nil {
		return ""
	}
	if node.FullName != "" {
		return node.FullName
	}
	return testSummarySpanHierarchyLabel(node)
}

func testSummarySpanHierarchyLabel(node *dagui.TestNode) string {
	var parts []string
	for current := node; current != nil; current = current.Parent {
		if current.Kind == dagui.TestNodeVirtualSuite {
			continue
		}
		name := testNodeDisplayName(current)
		if name == "" {
			continue
		}
		parts = append([]string{name}, parts...)
	}
	return strings.Join(parts, " › ")
}

func (tv *TestView) renderPassedGroupSidebarRow(out *termenv.Output, row testSidebarRow, selected, hovered bool, width int) string {
	if selected || hovered {
		return tv.renderHighlightedPassedGroupSidebarRow(out, row, selected, width)
	}
	selector := " "
	caret := CaretRightFilled
	if row.expanded {
		caret = CaretDownFilled
	}
	caretStyle := out.String(caret).Foreground(termenv.ANSIBrightBlack)
	iconStyle := out.String(IconSuccess).Foreground(termenv.ANSIGreen)
	indent := testSidebarIndentString(row.depth)
	label := fmt.Sprintf("%d passed", row.counts.Total())
	labelStyle := out.String(clipPlain(label, max(width-6-lipgloss.Width(indent), 1))).Foreground(termenv.ANSIGreen)
	return selector + " " + caretStyle.String() + " " + iconStyle.String() + " " + indent + labelStyle.String()
}

func (tv *TestView) renderHighlightedSidebarRow(out *termenv.Output, row testSidebarRow, selected bool, width int) string {
	node := row.node
	if node == nil {
		return sidebarSelectedSegment(out, strings.Repeat(" ", max(width, 0)), nil, false, false)
	}
	selector := " "
	if selected {
		selector = CaretRightFilled
	}
	icon := testCategoryIcon(node.Category)
	indent := testSidebarIndentString(row.depth)
	count := ""
	if node.Kind != dagui.TestNodeCase || len(node.Children) > 0 {
		count = fmt.Sprintf(" %d", node.Counts.Total())
	}
	nameWidth := max(width-4-lipgloss.Width(indent)-lipgloss.Width(count), 1)
	name := clipPlain(testNodeDisplayName(node), nameWidth)

	var b strings.Builder
	b.WriteString(sidebarSelectedSegment(out, selector, termenv.ANSIWhite, selected, false))
	b.WriteString(sidebarSelectedSegment(out, " ", nil, false, false))
	b.WriteString(sidebarSelectedSegment(out, icon, highlightedTestCategoryColor(node.Category), false, false))
	b.WriteString(sidebarSelectedSegment(out, " "+indent, nil, false, false))
	b.WriteString(sidebarSelectedSegment(out, name, termenv.ANSIWhite, selected, false))
	if count != "" {
		b.WriteString(sidebarSelectedSegment(out, count, termenv.ANSIWhite, false, true))
	}
	visible := selector + " " + icon + " " + indent + name + count
	if pad := width - lipgloss.Width(visible); pad > 0 {
		b.WriteString(sidebarSelectedSegment(out, strings.Repeat(" ", pad), nil, false, false))
	}
	return b.String()
}

func (tv *TestView) renderHighlightedPassedGroupSidebarRow(out *termenv.Output, row testSidebarRow, selected bool, width int) string {
	selector := " "
	if selected {
		selector = CaretRightFilled
	}
	caret := CaretRightFilled
	if row.expanded {
		caret = CaretDownFilled
	}
	icon := IconSuccess
	indent := testSidebarIndentString(row.depth)
	label := clipPlain(fmt.Sprintf("%d passed", row.counts.Total()), max(width-6-lipgloss.Width(indent), 1))

	var b strings.Builder
	b.WriteString(sidebarSelectedSegment(out, selector, termenv.ANSIWhite, selected, false))
	b.WriteString(sidebarSelectedSegment(out, " ", nil, false, false))
	b.WriteString(sidebarSelectedSegment(out, caret, termenv.ANSIWhite, false, false))
	b.WriteString(sidebarSelectedSegment(out, " ", nil, false, false))
	b.WriteString(sidebarSelectedSegment(out, icon, termenv.ANSIGreen, false, false))
	b.WriteString(sidebarSelectedSegment(out, " "+indent, nil, false, false))
	b.WriteString(sidebarSelectedSegment(out, label, termenv.ANSIGreen, selected, false))
	visible := selector + " " + caret + " " + icon + " " + indent + label
	if pad := width - lipgloss.Width(visible); pad > 0 {
		b.WriteString(sidebarSelectedSegment(out, strings.Repeat(" ", pad), nil, false, false))
	}
	return b.String()
}

func highlightedTestCategoryColor(category dagui.TestCategory) termenv.Color {
	if category == dagui.TestCategorySkipped {
		return termenv.ANSIWhite
	}
	return testCategoryColor(category)
}

func sidebarSelectedSegment(out *termenv.Output, text string, fg termenv.Color, bold, faint bool) string {
	st := out.String(text).Background(testSidebarRowBG)
	if fg != nil {
		st = st.Foreground(fg)
	}
	if bold {
		st = st.Bold()
	}
	if faint {
		st = st.Faint()
	}
	return st.String()
}

func testSidebarIndentString(depth int) string {
	if depth <= 0 {
		return ""
	}
	return strings.Repeat(" ", depth*testSidebarIndent)
}

func countSidebarTests(rows []testSidebarRow) int {
	var count int
	for _, row := range rows {
		count += row.testCount()
	}
	return count
}

func (tv *TestView) renderDetailLines(ctx tuist.Context, out *termenv.Output, row testSidebarRow, width, height int) []string {
	if row.kind == testSidebarPassedGroup {
		return tv.renderPassedGroupDetailLines(out, row, width, height)
	}
	node := row.node
	if node == nil {
		return []string{out.String("No test selected").Foreground(termenv.ANSIBrightBlack).String()}
	}
	span := node.Span
	representative := testTUISpan(node)
	color := testCategoryColor(node.Category)
	var lines []string

	duration := ""
	if representative != nil {
		duration = " " + out.String(dagui.FormatDuration(testSpanDuration(representative))).Foreground(termenv.ANSIBrightBlack).Faint().String()
	}
	icon := out.String(testCategoryIcon(node.Category)).Foreground(color).String()
	name := out.String(clipPlain(testNodeTitleName(node), max(width-lipgloss.Width(icon)-lipgloss.Width(duration)-1, 1))).Foreground(termenv.ANSIWhite).Bold().String()
	lines = append(lines, clipANSI(icon+" "+name+duration, width))
	lines = append(lines, out.String(strings.Repeat(HorizBar, max(width, 0))).Foreground(termenv.ANSIBrightBlack).Faint().String())

	if node.Kind == dagui.TestNodeVirtualSuite {
		lines = append(lines, out.String(clipPlain("virtual suite · no backing span", width)).Foreground(termenv.ANSIBrightBlack).Faint().String())
	} else if span == nil {
		lines = append(lines, out.String("no backing span").Foreground(termenv.ANSIBrightBlack).Faint().String())
	}

	contentHeight := max(height-len(lines), 0)
	if contentHeight == 0 {
		return cropLines(lines, height)
	}

	var childLines []string
	var childView *TestSpanChildrenView
	if span != nil && tv.SpanChildren != nil {
		if childSpans := tv.SpanChildren(span); childSpans != nil {
			result := tv.RenderChildResult(ctx.Resize(width, contentHeight), childSpans)
			if len(result.Lines) > 0 {
				childLines = result.Lines
				childView, _ = childSpans.(*TestSpanChildrenView)
			}
		}
	}

	logNeed := tv.detailLogLineCount(span)
	hasSplitter := logNeed > 0 && len(childLines) > 0 && contentHeight >= 3
	sectionHeight := contentHeight
	if hasSplitter {
		sectionHeight--
	}
	childHeight, logHeight := allocateDetailSectionHeights(len(childLines), logNeed, sectionHeight)
	if logHeight > 0 {
		logLines := tv.renderDetailLogLines(out, span, width, logHeight)
		lines = append(lines, cropLines(logLines, logHeight)...)
	}
	if hasSplitter {
		lines = append(lines, renderDetailSplitter(out, width))
	}
	if childHeight > 0 && len(childLines) > 0 {
		if childView != nil {
			childLines = childView.cropRenderedLines(childLines, childHeight)
		} else {
			childLines = cropLines(childLines, childHeight)
		}
		lines = append(lines, childLines...)
	}
	return cropLines(lines, height)
}

func renderDetailSplitter(out TermOutput, width int) string {
	return out.String(strings.Repeat(HorizBar, max(width, 0))).Foreground(termenv.ANSIBrightBlack).Faint().String()
}

func renderTestPaneLine(out TermOutput, left, right string, leftWidth int) string {
	leftJoin := visibleLineIsHorizontalRule(left) && visibleCellAt(left, leftWidth-1) == HorizBar
	rightJoin := visibleLineIsHorizontalRule(right) && visibleCellAt(right, 0) == HorizBar
	return padANSI(left, leftWidth) +
		testPaneGap(out, leftJoin) +
		testPaneJunction(out, leftJoin, rightJoin) +
		testPaneGap(out, rightJoin) +
		right
}

func testPaneGap(out TermOutput, join bool) string {
	if !join {
		return " "
	}
	return out.String(HorizBar).Foreground(termenv.ANSIBrightBlack).Faint().String()
}

func testPaneJunction(out TermOutput, leftJoin, rightJoin bool) string {
	sym := VertBar
	switch {
	case leftJoin && rightJoin:
		sym = CrossBar
	case leftJoin:
		sym = VertLeftBar
	case rightJoin:
		sym = VertRightBar
	}
	return out.String(sym).Foreground(termenv.ANSIBrightBlack).Faint().String()
}

func visibleLineIsHorizontalRule(s string) bool {
	seenBar := false
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			i = skipANSI(s, i)
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		if lipgloss.Width(string(r)) > 0 {
			switch string(r) {
			case HorizBar:
				seenBar = true
			case " ":
				// Ignore padding.
			default:
				return false
			}
		}
		i += size
	}
	return seenBar
}

func visibleCellAt(s string, col int) string {
	if col < 0 {
		return ""
	}
	visibleCol := 0
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			i = skipANSI(s, i)
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		cellWidth := lipgloss.Width(string(r))
		if cellWidth > 0 {
			if col >= visibleCol && col < visibleCol+cellWidth {
				return string(r)
			}
			visibleCol += cellWidth
		}
		i += size
	}
	return ""
}

func (tv *TestView) detailLogLineCount(span *dagui.Span) int {
	if span == nil {
		return 0
	}
	logs := tv.Logs[span.ID]
	if logs == nil || logs.UsedHeight() == 0 {
		return 0
	}
	return logs.UsedHeight() + 1
}

func (tv *TestView) renderDetailLogLines(out *termenv.Output, span *dagui.Span, width, height int) []string {
	if height <= 0 {
		return nil
	}
	if span == nil {
		return nil
	}
	logs := tv.Logs[span.ID]
	if logs == nil || logs.UsedHeight() == 0 {
		return nil
	}
	lines := []string{renderLogSectionHeader(out, width, true)}
	logs.SetWidth(width)
	logs.SetHeight(max(height-len(lines), 1))
	view := strings.TrimSuffix(logs.View(), "\n")
	if view == "" {
		lines = append(lines, out.String("No logs for selected test span.").Foreground(termenv.ANSIBrightBlack).Faint().String())
		return lines
	}
	lines = append(lines, strings.Split(view, "\n")...)
	return lines
}

func allocateDetailSectionHeights(childNeed, logNeed, height int) (int, int) {
	if height <= 0 {
		return 0, 0
	}
	if childNeed == 0 {
		return 0, min(logNeed, height)
	}
	if logNeed == 0 {
		return min(childNeed, height), 0
	}
	logMax := height / 2
	childMax := height - logMax
	childHeight := min(childNeed, childMax)
	logHeight := min(logNeed, logMax)
	remaining := height - childHeight - logHeight
	if remaining > 0 && childNeed > childHeight {
		grow := min(remaining, childNeed-childHeight)
		childHeight += grow
		remaining -= grow
	}
	if remaining > 0 && logNeed > logHeight {
		grow := min(remaining, logNeed-logHeight)
		logHeight += grow
	}
	return childHeight, logHeight
}

func (tv *TestView) renderPassedGroupDetailLines(out *termenv.Output, row testSidebarRow, width, height int) []string {
	state := "collapsed"
	if row.expanded {
		state = "expanded"
	}
	icon := out.String(IconSuccess).Foreground(termenv.ANSIGreen).String()
	name := out.String(fmt.Sprintf("%d passed", row.counts.Total())).Foreground(termenv.ANSIWhite).Bold().String()
	lines := []string{
		clipANSI(icon+" "+name, width),
		out.String(strings.Repeat(HorizBar, max(width, 0))).Foreground(termenv.ANSIBrightBlack).Faint().String(),
		out.String(clipPlain(fmt.Sprintf("%d passing tests · %s", row.counts.Total(), state), width)).Foreground(termenv.ANSIGreen).String(),
		out.String(clipPlain("Press enter to expand or collapse this group.", width)).Foreground(termenv.ANSIBrightBlack).Faint().String(),
	}
	return cropLines(lines, height)
}

func (fe *frontendPretty) toggleTestsMode() {
	if fe.testsMode {
		fe.closeTestsMode()
		return
	}

	tv := fe.fullscreenTestViewForFocus()
	if tv == nil || !tv.currentView().HasTests() {
		return
	}
	fe.fullscreenTests = tv
	fe.testsReturnSpan = fe.FocusedSpan
	fe.testsMode = true
	tv.ensureFocusedTest(tv.currentView())
	fe.applyTuistFocus()
	if fe.keymapBar != nil {
		fe.keymapBar.Update()
	}
	fe.Update()
}

func (fe *frontendPretty) fullscreenTestViewForFocus() *TestView {
	if span := fe.focusedCheckWithTests(); span != nil {
		return fe.newFullscreenTestView(span.ID, span.CheckName)
	}
	if fe.db == nil || !fe.db.HasTests() {
		return nil
	}
	return fe.newFullscreenTestView(dagui.SpanID{}, "")
}

func (fe *frontendPretty) focusedCheckWithTests() *dagui.Span {
	if fe.db == nil || !fe.FocusedSpan.IsValid() {
		return nil
	}
	for span := fe.db.Spans.Map[fe.FocusedSpan]; span != nil; span = span.ParentSpan {
		if span.CheckName != "" && fe.db.TestViewForSpan(span).HasTests() {
			return span
		}
	}
	return nil
}

func (fe *frontendPretty) newFullscreenTestView(root dagui.SpanID, scopeName string) *TestView {
	tv := fe.newTestView(root, scopeName)
	tv.ForceInteractive = true
	tv.focusArea = testFocusSidebar
	tv.OnFocusSpan = func(span *dagui.Span) {
		if span != nil {
			fe.FocusedSpan = span.ID
		}
	}
	return tv
}

func (fe *frontendPretty) inlineTestView(root dagui.SpanID) *TestView {
	if fe.testViews == nil {
		fe.testViews = make(map[dagui.SpanID]*TestView)
	}
	tv := fe.testViews[root]
	if tv == nil {
		tv = fe.newTestView(root, "")
		tv.ListOnly = true
		fe.testViews[root] = tv
	}
	return tv
}

func (fe *frontendPretty) newTestView(root dagui.SpanID, scopeName string) *TestView {
	tv := &TestView{
		Profile:      fe.profile,
		Logs:         fe.logs.Logs,
		ScopeName:    scopeName,
		SpanChildren: fe.testSpanChildrenView,
		TraceID:      fe.traceID,
	}
	if root.IsValid() {
		tv.View = func() *dagui.TestView {
			return fe.db.TestViewForSpan(fe.db.Spans.Map[root])
		}
	} else {
		tv.View = func() *dagui.TestView {
			return fe.db.TestView()
		}
	}
	return tv
}

func (fe *frontendPretty) updateTestViews() {
	if fe.fullscreenTests != nil {
		fe.fullscreenTests.Update()
	}
	for _, tv := range fe.testViews {
		tv.Update()
	}
	for _, view := range fe.testSpanChildren {
		view.UpdateAll()
	}
	for id, st := range fe.spanTrees {
		span := fe.db.Spans.Map[id]
		if span != nil && span.CheckName != "" {
			st.Update()
		}
	}
}

func (fe *frontendPretty) closeTestsMode() {
	fe.testsMode = false
	fe.fullscreenTests = nil
	if fe.testsReturnSpan.IsValid() {
		fe.FocusedSpan = fe.testsReturnSpan
	}
	fe.testsReturnSpan = dagui.SpanID{}
	fe.recalculateViewLocked()
	if fe.keymapBar != nil {
		fe.keymapBar.Update()
	}
	fe.Update()
}

func (fe *frontendPretty) goTestStart() {
	if fe.fullscreenTests == nil {
		return
	}
	if fe.fullscreenTests.focusArea == testFocusChildren && fe.fullscreenTests.focusedChildren != nil {
		fe.fullscreenTests.focusedChildren.GoStart(fe)
	} else if fe.fullscreenTests.focusArea == testFocusSidebar {
		fe.fullscreenTests.GoStart()
	}
	fe.Update()
}

func (fe *frontendPretty) goTestEnd() {
	if fe.fullscreenTests == nil {
		return
	}
	if fe.fullscreenTests.focusArea == testFocusChildren && fe.fullscreenTests.focusedChildren != nil {
		fe.fullscreenTests.focusedChildren.GoEnd(fe)
	} else if fe.fullscreenTests.focusArea == testFocusSidebar {
		fe.fullscreenTests.GoEnd()
	}
	fe.Update()
}

func (fe *frontendPretty) goTestUp() {
	if fe.fullscreenTests == nil {
		return
	}
	if fe.fullscreenTests.focusArea == testFocusChildren && fe.fullscreenTests.focusedChildren != nil {
		fe.fullscreenTests.focusedChildren.GoUp(fe)
	} else if fe.fullscreenTests.focusArea == testFocusSidebar {
		fe.fullscreenTests.GoUp()
	}
	fe.Update()
}

func (fe *frontendPretty) goTestDown() {
	if fe.fullscreenTests == nil {
		return
	}
	if fe.fullscreenTests.focusArea == testFocusChildren && fe.fullscreenTests.focusedChildren != nil {
		fe.fullscreenTests.focusedChildren.GoDown(fe)
	} else if fe.fullscreenTests.focusArea == testFocusSidebar {
		fe.fullscreenTests.GoDown()
	}
	fe.Update()
}

func (fe *frontendPretty) testFocusLeft() {
	if fe.fullscreenTests == nil {
		return
	}
	switch fe.fullscreenTests.focusArea {
	case testFocusChildren:
		if fe.fullscreenTests.focusedChildren != nil && fe.fullscreenTests.focusedChildren.CloseOrGoOut(fe) {
			fe.Update()
			return
		}
		fe.fullscreenTests.focusSidebar(fe)
	default:
		fe.closeTestsMode()
	}
}

func (fe *frontendPretty) focusFocusedTestDetail() {
	if fe.fullscreenTests == nil {
		return
	}
	if fe.fullscreenTests.focusArea == testFocusChildren {
		if fe.fullscreenTests.focusedChildren != nil {
			fe.fullscreenTests.focusedChildren.OpenOrGoIn(fe)
			fe.Update()
		}
		return
	}
	if fe.fullscreenTests.ToggleFocusedGroup() {
		fe.Update()
		return
	}
	node := fe.fullscreenTests.FocusedNode()
	if node == nil || node.Kind == dagui.TestNodeVirtualSuite || node.Span == nil {
		return
	}
	span := node.Span
	if childView := fe.fullscreenTests.childViewForSpan(span); childView != nil && childView.FocusFirst(fe) {
		fe.fullscreenTests.focusArea = testFocusChildren
		fe.fullscreenTests.focusedChildren = childView
		fe.fullscreenTests.Update()
	}
}

func (fe *frontendPretty) renderTestsView(ctx tuist.Context) {
	if fe.fullscreenTests == nil {
		return
	}
	fe.RenderChild(ctx, fe.fullscreenTests)
}

// finalRenderTestsWidth is used when report mode has no live terminal width.
const finalRenderTestsWidth = 80

func (fe *frontendPretty) shouldRenderInlineTests(row *dagui.TraceRow) bool {
	if row == nil || row.Span == nil || row.Span.CheckName == "" {
		return false
	}
	if row.Expanded && !fe.finalRender {
		return false
	}
	return fe.db.TestViewForSpan(row.Span).HasTests()
}

func (s *SpanTreeView) renderInlineTests(ctx tuist.Context, r *renderer, row *dagui.TraceRow) []string {
	if !s.fe.shouldRenderInlineTests(row) {
		return nil
	}
	if s.fe.reportOnly && s.fe.finalRender {
		view := s.fe.db.TestViewForSpan(row.Span)
		if !view.HasTests() {
			return nil
		}
		tv := &TestView{
			Profile:         s.fe.profile,
			Logs:            s.fe.logs.Logs,
			SummaryIndent:   2,
			SummaryLogLines: -1,
			TraceID:         s.fe.traceID,
		}
		width := ctx.Width
		if width <= 0 {
			width = finalRenderTestsWidth
		}
		out := NewOutput(new(strings.Builder), termenv.WithProfile(s.fe.profile))
		lines := tv.renderTestSummaryLines(out, view, max(width, finalRenderTestsWidth), finalTestViewHeight(tv))
		if len(lines) == 0 {
			return nil
		}
		s.fe.claims.claimTestReport(row.Span, view)
		return append([]string{""}, lines...)
	}
	tv := s.fe.inlineTestView(row.Span.ID)
	summaryIndent := 2
	if tv.SummaryIndent != summaryIndent {
		tv.SummaryIndent = summaryIndent
		tv.Update()
	}
	summaryLogLines := -1
	if !s.fe.finalRender {
		summaryLogLines = 8
	}
	if tv.SummaryLogLines != summaryLogLines {
		tv.SummaryLogLines = summaryLogLines
		tv.Update()
	}
	showHint := !s.fe.finalRender
	if tv.ShowTestViewerHint != showHint {
		tv.ShowTestViewerHint = showHint
		tv.Update()
	}
	limit := finalTestViewHeight(tv)
	if tv.MaxHeight != limit {
		tv.MaxHeight = limit
		tv.Update()
	}

	var prefix string
	if !s.fe.finalRender {
		prefixBuf := new(strings.Builder)
		prefixOut := NewOutput(prefixBuf, termenv.WithProfile(s.fe.profile))
		r.indentFunc = s.indentFunc(prefixOut)
		r.fancyIndent(prefixOut, row, false, false)
		pipe := prefixOut.String(VertBoldBar).Foreground(restrainedStatusColor(row.Span))
		if s.focused {
			pipe = hl(pipe)
		}
		fmt.Fprint(prefixOut, pipe.String())
		fmt.Fprint(prefixOut, " ")
		prefix = prefixBuf.String()
	}

	ctxWidth := ctx.Width
	if s.fe.finalRender && ctxWidth <= 0 {
		ctxWidth = finalRenderTestsWidth + lipgloss.Width(prefix)
	}
	width := max(ctxWidth-lipgloss.Width(prefix), 1)
	if s.fe.finalRender {
		width = max(width, finalRenderTestsWidth)
	}
	result := s.RenderChildResult(ctx.Resize(width, limit), tv)
	if len(result.Lines) > 0 {
		s.fe.claims.claimTestReport(row.Span, tv.currentView())
	}
	lines := make([]string, 0, len(result.Lines)+1)
	if s.fe.finalRender {
		lines = append(lines, "")
	} else if prefix != "" {
		lines = append(lines, strings.TrimRight(prefix, " "))
	}
	for _, line := range result.Lines {
		lines = append(lines, prefix+line)
	}
	return lines
}

func (fe *frontendPretty) renderLiveGlobalTests(ctx tuist.Context) []string {
	if fe.db == nil {
		return nil
	}
	view := fe.db.TestView()
	if !view.HasTests() || testViewAllReportEntriesUnderChecks(view) {
		return nil
	}
	tv := fe.inlineTestView(dagui.SpanID{})
	if tv.SummaryIndent != 0 {
		tv.SummaryIndent = 0
		tv.Update()
	}
	if tv.SummaryLogLines != 8 {
		tv.SummaryLogLines = 8
		tv.Update()
	}
	if !tv.ShowTestViewerHint {
		tv.ShowTestViewerHint = true
		tv.Update()
	}
	limit := liveTestViewHeight(ctx)
	if tv.MaxHeight != limit {
		tv.MaxHeight = limit
		tv.Update()
	}
	width := ctx.Width
	if width <= 0 {
		width = finalRenderTestsWidth
	}
	lines := fe.RenderChildResult(ctx.Resize(max(width, 1), limit), tv).Lines
	if len(lines) > 0 {
		fe.claims.claimTestReport(nil, view)
	}
	return lines
}

func (fe *frontendPretty) renderFinalGlobalTests(ctx tuist.Context) []string {
	if fe.db == nil {
		return nil
	}
	view := fe.db.TestView()
	if !view.HasTests() || testViewAllReportEntriesUnderChecks(view) {
		return nil
	}
	tv := fe.inlineTestView(dagui.SpanID{})
	if tv.SummaryIndent != 0 {
		tv.SummaryIndent = 0
		tv.Update()
	}
	if tv.SummaryLogLines != -1 {
		tv.SummaryLogLines = -1
		tv.Update()
	}
	if tv.ShowTestViewerHint {
		tv.ShowTestViewerHint = false
		tv.Update()
	}
	limit := finalTestViewHeight(tv)
	if tv.MaxHeight != limit {
		tv.MaxHeight = limit
		tv.Update()
	}
	width := ctx.Width
	if width <= 0 {
		width = finalRenderTestsWidth
	}
	width = max(width, finalRenderTestsWidth)
	return fe.RenderChildResult(ctx.Resize(width, limit), tv).Lines
}

func liveTestViewHeight(ctx tuist.Context) int {
	height := ctx.ScreenHeight()
	if height <= 0 {
		return 12
	}
	return max(height/3, 4)
}

func finalTestViewHeight(tv *TestView) int {
	return 10000
}

func testViewAllReportEntriesUnderChecks(view *dagui.TestView) bool {
	if view == nil {
		return false
	}
	seenEntry := false
	allUnderChecks := true
	var walk func(*dagui.TestNode)
	walk = func(node *dagui.TestNode) {
		if node == nil {
			return
		}
		switch {
		case node.Kind == dagui.TestNodeCase:
			seenEntry = true
			if !testSpanUnderCheck(node.Span) {
				allUnderChecks = false
			}
		case node.Counts.Total() == 0 && node.Category != dagui.TestCategoryPassing:
			seenEntry = true
			if !testSpanUnderCheck(testTUISpan(node)) {
				allUnderChecks = false
			}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	for _, root := range view.Roots {
		walk(root)
	}
	return seenEntry && allUnderChecks
}

func testSpanUnderCheck(span *dagui.Span) bool {
	for cur := span; cur != nil; cur = cur.ParentSpan {
		if cur.CheckName != "" {
			return true
		}
	}
	return false
}

// isFailingLeafTestCase reports whether node is a failing test case with no
// failing descendant test case -- the leaf whose sub-operation logs carry the
// real failure (a build/setup error, panic, etc.). A parent case is excluded
// because rolling up its logs would just repeat its failing child's.
func isFailingLeafTestCase(node *dagui.TestNode) bool {
	if node == nil || node.Kind != dagui.TestNodeCase ||
		node.SelfCategory != dagui.TestCategoryFailing {
		return false
	}
	return !hasFailingDescendantCase(node)
}

// failingLeafTestCases collects the failing leaf test cases in a view -- the
// nodes whose own sub-operation carries the real failure -- in document order.
// These back the --test drill-in suggestions.
func failingLeafTestCases(view *dagui.TestView) []*dagui.TestNode {
	if view == nil {
		return nil
	}
	var out []*dagui.TestNode
	var walk func(*dagui.TestNode)
	walk = func(node *dagui.TestNode) {
		if node == nil {
			return
		}
		if isFailingLeafTestCase(node) {
			out = append(out, node)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	for _, root := range view.Roots {
		walk(root)
	}
	return out
}

// errorTailStart returns the line index to start rendering a failed test's
// rolled-up logs at: a few context lines before the trailing run of fail/error
// lines (case-insensitive). It anchors on the last fail/error mention, then
// walks up through nearby ones -- bridging gaps up to context lines -- to the
// start of that trailing cluster, so the whole failure region shows, not just
// its tail. Leading noise (dependency downloads, an incidental "errors" in a
// package name) is trimmed: those matches sit far above the failure cluster and
// the gap stops the walk. Returns 0 (render everything) when nothing matches.
func errorTailStart(lines []string, context int) int {
	match := func(s string) bool {
		lower := strings.ToLower(s)
		return strings.Contains(lower, "fail") || strings.Contains(lower, "error")
	}
	last := -1
	for i, line := range lines {
		if match(line) {
			last = i
		}
	}
	if last < 0 {
		return 0
	}
	anchor := last
	for i := last - 1; i >= 0; i-- {
		if !match(lines[i]) {
			continue
		}
		if anchor-i > context+1 {
			// A match, but separated from the cluster by noise -- stop here.
			break
		}
		anchor = i
	}
	return max(anchor-context, 0)
}

// cloudLogsTarget returns the 'dagger cloud logs' selector that addresses span
// by name when possible (--test/--check), else by --span. Empty for a nil span.
func cloudLogsTarget(span *dagui.Span) string {
	switch {
	case span == nil:
		return ""
	case span.TestCaseName != "":
		return fmt.Sprintf("--test %q", span.TestCaseName)
	case span.CheckName != "":
		return fmt.Sprintf("--check %q", span.CheckName)
	default:
		return fmt.Sprintf("--span %s", span.ID)
	}
}

// errorWindowLines renders a failed span's rolled-up logs for a final report:
// the error-anchored window (errorTailStart) prefixed with a marker for any
// trimmed lines, then a 'dagger cloud logs' hint for the full output when a
// trace ID and selector target are known. Lines are prefixed with indent and
// left unclipped -- the hint is a copy-paste command.
func errorWindowLines(out TermOutput, rawLines []string, indent, traceID, target string) []string {
	start := errorTailStart(rawLines, 5)
	lines := make([]string, 0, len(rawLines)-start+2)
	if start > 0 {
		marker := out.String(fmt.Sprintf("... %d earlier log lines ...", start)).Foreground(termenv.ANSIBrightBlack).Faint().String()
		lines = append(lines, indent+marker)
	}
	for _, line := range rawLines[start:] {
		lines = append(lines, indent+line)
	}
	if traceID != "" && target != "" {
		hint := out.String(fmt.Sprintf("full: dagger cloud logs %s %s", traceID, target)).Foreground(termenv.ANSIBrightBlack).Faint().String()
		lines = append(lines, indent+hint)
	}
	return lines
}

func hasFailingDescendantCase(node *dagui.TestNode) bool {
	for _, child := range node.Children {
		if child == nil {
			continue
		}
		if child.Kind == dagui.TestNodeCase && child.SelfCategory == dagui.TestCategoryFailing {
			return true
		}
		if hasFailingDescendantCase(child) {
			return true
		}
	}
	return false
}

type TestSpanChildrenView struct {
	tuist.Compo

	fe     *frontendPretty
	rootID dagui.SpanID

	scope       spanTreeScope
	container   *tuist.Container
	focusedSpan dagui.SpanID
}

var _ tuist.Component = (*TestSpanChildrenView)(nil)

func (v *TestSpanChildrenView) UpdateAll() {
	v.Update()
	if v.container != nil {
		v.container.Update()
	}
	for _, st := range v.scope.spanTrees {
		st.Update()
	}
}

func (fe *frontendPretty) testSpanChildrenView(span *dagui.Span) tuist.Component {
	if span == nil || !span.ID.IsValid() {
		return nil
	}
	if fe.testSpanChildren == nil {
		fe.testSpanChildren = make(map[dagui.SpanID]*TestSpanChildrenView)
	}
	view := fe.testSpanChildren[span.ID]
	if view == nil {
		view = &TestSpanChildrenView{
			fe:        fe,
			rootID:    span.ID,
			container: &tuist.Container{},
			scope: spanTreeScope{
				spanTrees: make(map[dagui.SpanID]*SpanTreeView),
			},
		}
		fe.testSpanChildren[span.ID] = view
	}
	return view
}

func (v *TestSpanChildrenView) Render(ctx tuist.Context) {
	if !v.sync() {
		return
	}
	v.RenderChild(ctx, v.container)
}

func (v *TestSpanChildrenView) sync() bool {
	root := v.fe.db.Spans.Map[v.rootID]
	if root == nil {
		v.clearChildren()
		return false
	}

	opts := v.fe.FrontendOpts
	opts.ZoomedSpan = root.ID
	rowsView := v.fe.db.RowsView(opts)
	if len(rowsView.Body) == 0 {
		v.clearChildren()
		return false
	}

	v.scope.rowsView = rowsView
	v.scope.rows = rowsView.Rows(opts)
	v.scope.opts = opts
	if v.scope.spanTrees == nil {
		v.scope.spanTrees = make(map[dagui.SpanID]*SpanTreeView)
	}

	children := make([]tuist.Component, 0, len(rowsView.Body))
	for i, tree := range rowsView.Body {
		st := v.fe.getOrCreateSpanTreeInScope(tree.Span.ID, &v.scope)
		st.parent = nil
		st.indexInParent = i
		v.fe.syncTreeNodeInScope(st, treePrefix{}, &v.scope)
		children = append(children, st)
	}

	if !sameComponents(v.container.Children, children) {
		v.container.Children = children
		v.container.Update()
	}
	if v.focusedSpan.IsValid() && v.scope.rows.BySpan[v.focusedSpan] == nil {
		v.focusedSpan = dagui.SpanID{}
	}
	return len(children) > 0
}

func (v *TestSpanChildrenView) clearChildren() {
	v.scope.rowsView = nil
	v.scope.rows = nil
	v.focusedSpan = dagui.SpanID{}
	if v.container != nil && len(v.container.Children) > 0 {
		v.container.Children = nil
		v.container.Update()
	}
}

func (v *TestSpanChildrenView) FocusedSpan() *dagui.Span {
	if v == nil || !v.focusedSpan.IsValid() {
		return nil
	}
	return v.fe.db.Spans.Map[v.focusedSpan]
}

func (v *TestSpanChildrenView) FocusFirst(fe *frontendPretty) bool {
	if v == nil || !v.sync() || v.scope.rows == nil || len(v.scope.rows.Order) == 0 {
		return false
	}
	return v.FocusSpan(fe, v.scope.rows.Order[0].Span.ID)
}

func (v *TestSpanChildrenView) FocusSpan(fe *frontendPretty, id dagui.SpanID) bool {
	if v == nil || !id.IsValid() || !v.sync() || v.scope.rows == nil {
		return false
	}
	row := v.scope.rows.BySpan[id]
	if row == nil {
		return false
	}
	st := v.scope.spanTrees[id]
	if st == nil {
		return false
	}
	if fe == nil {
		fe = v.fe
	}
	v.focusedSpan = id
	fe.FocusedSpan = id
	if fe.tui != nil {
		fe.tui.SetFocus(st)
	}
	st.Update()
	v.Update()
	return true
}

func (v *TestSpanChildrenView) GoStart(fe *frontendPretty) bool {
	if v == nil || !v.sync() || v.scope.rows == nil || len(v.scope.rows.Order) == 0 {
		return false
	}
	return v.FocusSpan(fe, v.scope.rows.Order[0].Span.ID)
}

func (v *TestSpanChildrenView) GoEnd(fe *frontendPretty) bool {
	if v == nil || !v.sync() || v.scope.rows == nil || len(v.scope.rows.Order) == 0 {
		return false
	}
	return v.FocusSpan(fe, v.scope.rows.Order[len(v.scope.rows.Order)-1].Span.ID)
}

func (v *TestSpanChildrenView) GoUp(fe *frontendPretty) bool {
	if v == nil || !v.sync() || v.scope.rows == nil {
		return false
	}
	idx := v.focusedIndex()
	if idx <= 0 {
		return false
	}
	return v.FocusSpan(fe, v.scope.rows.Order[idx-1].Span.ID)
}

func (v *TestSpanChildrenView) GoDown(fe *frontendPretty) bool {
	if v == nil || !v.sync() || v.scope.rows == nil {
		return false
	}
	idx := v.focusedIndex()
	if idx < 0 || idx+1 >= len(v.scope.rows.Order) {
		return false
	}
	return v.FocusSpan(fe, v.scope.rows.Order[idx+1].Span.ID)
}

func (v *TestSpanChildrenView) focusedIndex() int {
	if v == nil || v.scope.rows == nil || !v.focusedSpan.IsValid() {
		return -1
	}
	if row := v.scope.rows.BySpan[v.focusedSpan]; row != nil {
		return row.Index
	}
	return -1
}

func (v *TestSpanChildrenView) cropRenderedLines(lines []string, height int) []string {
	if height <= 0 {
		return nil
	}
	if len(lines) <= height {
		return lines
	}
	focusLine := v.findFocusLine()
	if focusLine < 0 {
		return cropLines(lines, height)
	}
	focusHeight := 1
	if focused := v.scope.spanTrees[v.focusedSpan]; focused != nil {
		focusHeight = max(focused.selfLineCount, 1)
	}
	end := cropEnd(len(lines), height, focusLine, focusHeight)
	start := max(end-height, 0)
	return lines[start:end]
}

func (v *TestSpanChildrenView) findFocusLine() int {
	if v == nil || !v.focusedSpan.IsValid() || v.container == nil {
		return -1
	}
	focused := v.scope.spanTrees[v.focusedSpan]
	if focused == nil {
		return -1
	}

	var path []*SpanTreeView
	for cur := focused; cur != nil; cur = cur.parent {
		path = append(path, cur)
	}
	if len(path) == 0 {
		return -1
	}
	root := path[len(path)-1]
	offset := 0
	foundRoot := false
	for _, child := range v.container.Children {
		tree, ok := child.(*SpanTreeView)
		if !ok || tree == nil {
			continue
		}
		if tree == root {
			foundRoot = true
			break
		}
		offset += tree.totalLineCount()
	}
	if !foundRoot {
		return -1
	}

	for i := len(path) - 1; i >= 0; i-- {
		node := path[i]
		if i >= len(path)-1 {
			continue
		}
		parent := path[i+1]
		offset += parent.selfLineCount
		idx := node.indexInParent
		if len(parent.childGapCounts) != len(parent.children) || len(parent.childLineCounts) != len(parent.children) || idx >= len(parent.children) {
			return -1
		}
		for sibling := range idx {
			offset += parent.childGapCounts[sibling] + parent.childLineCounts[sibling]
		}
		offset += parent.childGapCounts[idx]
	}
	return offset
}

func (v *TestSpanChildrenView) CloseOrGoOut(fe *frontendPretty) bool {
	if v == nil || !v.sync() || v.scope.rows == nil || !v.focusedSpan.IsValid() {
		return false
	}
	row := v.scope.rows.BySpan[v.focusedSpan]
	if row == nil {
		return false
	}
	tree := v.scope.rowsView.BySpan[v.focusedSpan]
	if tree != nil && tree.IsExpanded(v.scope.opts) {
		fe.setExpanded(v.focusedSpan, false)
		v.sync()
		v.FocusSpan(fe, row.Span.ID)
		return true
	}
	if row.Parent != nil {
		return v.FocusSpan(fe, row.Parent.Span.ID)
	}
	return false
}

func (v *TestSpanChildrenView) OpenOrGoIn(fe *frontendPretty) bool {
	if v == nil || !v.sync() || v.scope.rows == nil || !v.focusedSpan.IsValid() {
		return false
	}
	row := v.scope.rows.BySpan[v.focusedSpan]
	if row == nil {
		return false
	}
	tree := v.scope.rowsView.BySpan[v.focusedSpan]
	if tree != nil && tree.IsExpanded(v.scope.opts) {
		idx := row.Index + 1
		if idx < len(v.scope.rows.Order) && v.scope.rows.Order[idx].Depth > row.Depth {
			return v.FocusSpan(fe, v.scope.rows.Order[idx].Span.ID)
		}
		return true
	}
	fe.setExpanded(v.focusedSpan, true)
	v.sync()
	v.FocusSpan(fe, row.Span.ID)
	return true
}

func sameComponents(a, b []tuist.Component) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (tv *TestView) flattenTestRows(view *dagui.TestView) []testSidebarRow {
	if view == nil {
		return nil
	}
	var rows []testSidebarRow
	tv.appendTestRows(&rows, view.Roots, 0, "root")
	return rows
}

func (tv *TestView) appendTestRows(rows *[]testSidebarRow, nodes []*dagui.TestNode, depth int, parentKey string) {
	partition := dagui.PartitionTests(nodes)
	groups := [][]*dagui.TestNode{
		partition.Failing,
		partition.Running,
		partition.Suites,
		partition.Mixed,
	}
	for _, group := range groups {
		for _, node := range group {
			*rows = append(*rows, testSidebarRow{kind: testSidebarNode, node: node, depth: depth})
			tv.appendTestRows(rows, node.Children, depth+1, string(node.ID))
		}
	}

	if len(partition.Passing) > 0 {
		var counts dagui.TestCounts
		for _, node := range partition.Passing {
			counts.Failing += node.Counts.Failing
			counts.Running += node.Counts.Running
			counts.Passing += node.Counts.Passing
			counts.Skipped += node.Counts.Skipped
		}
		key := parentKey + ":passed"
		expanded := tv.expandedPassedGroups[key]
		*rows = append(*rows, testSidebarRow{kind: testSidebarPassedGroup, depth: depth, key: key, counts: counts, expanded: expanded})
		if expanded {
			for _, node := range partition.Passing {
				*rows = append(*rows, testSidebarRow{kind: testSidebarNode, node: node, depth: depth + 1})
				tv.appendTestRows(rows, node.Children, depth+2, string(node.ID))
			}
		}
	}

	for _, node := range partition.Skipped {
		*rows = append(*rows, testSidebarRow{kind: testSidebarNode, node: node, depth: depth})
		tv.appendTestRows(rows, node.Children, depth+1, string(node.ID))
	}
}

func testTUISpan(node *dagui.TestNode) *dagui.Span {
	if node == nil {
		return nil
	}
	if node.Span != nil {
		return node.Span
	}
	return node.RepresentativeSpan
}

func testNodeDisplayName(node *dagui.TestNode) string {
	if node == nil {
		return ""
	}
	if node.Name != "" {
		return node.Name
	}
	return node.FullName
}

func testNodeTitleName(node *dagui.TestNode) string {
	if node == nil {
		return ""
	}
	if node.FullName != "" {
		return node.FullName
	}
	return testNodeDisplayName(node)
}

func testCategoryIcon(category dagui.TestCategory) string {
	switch category {
	case dagui.TestCategoryFailing:
		return IconFailure
	case dagui.TestCategoryRunning:
		return DotHalf
	case dagui.TestCategorySkipped:
		return IconSkipped
	case dagui.TestCategoryMixed:
		return DotCenter
	default:
		return IconSuccess
	}
}

func testCategoryColor(category dagui.TestCategory) termenv.Color {
	switch category {
	case dagui.TestCategoryFailing:
		return termenv.ANSIRed
	case dagui.TestCategoryRunning:
		return termenv.ANSIYellow
	case dagui.TestCategorySkipped:
		return termenv.ANSIBrightBlack
	case dagui.TestCategoryMixed:
		return termenv.ANSIMagenta
	default:
		return termenv.ANSIGreen
	}
}

func testNodeNameColor(node *dagui.TestNode) termenv.Color {
	if node == nil {
		return termenv.ANSIWhite
	}
	if node.Kind == dagui.TestNodeSuite || node.Kind == dagui.TestNodeVirtualSuite {
		return termenv.ANSIBrightBlack
	}
	return termenv.ANSIWhite
}

func testSpanDuration(span *dagui.Span) time.Duration {
	if span == nil {
		return 0
	}
	end := span.EndTime
	if span.IsRunningOrEffectsRunning() || end.Before(span.StartTime) {
		end = time.Now()
	}
	if dur := span.Activity.Duration(end); dur > 0 {
		return dur
	}
	if end.After(span.StartTime) {
		return end.Sub(span.StartTime)
	}
	return 0
}

func cropLines(lines []string, height int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	return lines[:height]
}

func padANSI(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func clipPlain(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	var b strings.Builder
	for _, r := range s {
		candidate := b.String() + string(r)
		if lipgloss.Width(candidate) > width-1 {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + "…"
}

// clipANSI is intentionally conservative for this spike: it only clips plain
// text reliably and otherwise returns the ANSI string if it already fits.
func clipANSI(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	return s
}
