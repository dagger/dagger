package idtui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

type testSidebarRow struct {
	node  *dagui.TestNode
	depth int
}

type TestView struct {
	tuist.Compo

	Profile termenv.Profile
	View    func() *dagui.TestView
	Logs    map[dagui.SpanID]*Vterm

	// MaxHeight caps the rendered height. A zero value means fullscreen mode:
	// use the terminal height, leaving room for the keymap sibling.
	MaxHeight int
	ScopeName string

	OnFocusSpan func(*dagui.Span)

	focusedTest dagui.TestNodeID
}

var _ tuist.Component = (*TestView)(nil)

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

func (tv *TestView) Render(ctx tuist.Context) {
	view := tv.currentView()
	rows, selectedIdx := tv.ensureFocusedTest(view)

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
			// Leave room for the keymap sibling and a small visual gap.
			viewportHeight = max(ctx.ScreenHeight()-2, 1)
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

	selected := rows[selectedIdx].node
	left := tv.renderSidebarLines(out, view, rows, selectedIdx, leftWidth, viewportHeight)
	right := tv.renderDetailLines(out, selected, rightWidth, viewportHeight)
	border := out.String(VertBar).Foreground(termenv.ANSIBrightBlack).Faint().String()

	for i := range viewportHeight {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		ctx.Line(padANSI(l, leftWidth) + " " + border + " " + r)
	}
}

func (tv *TestView) FocusedNode() *dagui.TestNode {
	return tv.focusedTestNode(tv.currentView())
}

func (tv *TestView) FocusedSpan() *dagui.Span {
	return testTUISpan(tv.FocusedNode())
}

func (tv *TestView) GoStart() {
	rows, _ := tv.ensureFocusedTest(tv.currentView())
	if len(rows) > 0 {
		tv.focusTestNode(rows[0].node)
		tv.Update()
	}
}

func (tv *TestView) GoEnd() {
	rows, _ := tv.ensureFocusedTest(tv.currentView())
	if len(rows) > 0 {
		tv.focusTestNode(rows[len(rows)-1].node)
		tv.Update()
	}
}

func (tv *TestView) GoUp() {
	rows, idx := tv.ensureFocusedTest(tv.currentView())
	if idx > 0 {
		tv.focusTestNode(rows[idx-1].node)
		tv.Update()
	}
}

func (tv *TestView) GoDown() {
	rows, idx := tv.ensureFocusedTest(tv.currentView())
	if idx >= 0 && idx+1 < len(rows) {
		tv.focusTestNode(rows[idx+1].node)
		tv.Update()
	}
}

func (tv *TestView) focusedTestNode(view *dagui.TestView) *dagui.TestNode {
	if view == nil || tv.focusedTest == "" {
		return nil
	}
	return view.ByID[tv.focusedTest]
}

func (tv *TestView) ensureFocusedTest(view *dagui.TestView) ([]testSidebarRow, int) {
	rows := flattenTestRows(view)
	if len(rows) == 0 {
		tv.focusedTest = ""
		return rows, -1
	}
	for i, row := range rows {
		if row.node.ID == tv.focusedTest {
			tv.focusTestNode(row.node)
			return rows, i
		}
	}
	tv.focusTestNode(rows[0].node)
	return rows, 0
}

func (tv *TestView) focusTestNode(node *dagui.TestNode) {
	if node == nil {
		tv.focusedTest = ""
		return
	}
	tv.focusedTest = node.ID
	if tv.OnFocusSpan != nil {
		tv.OnFocusSpan(testTUISpan(node))
	}
}

func (tv *TestView) renderSidebarLines(out *termenv.Output, view *dagui.TestView, rows []testSidebarRow, selectedIdx, width, height int) []string {
	var lines []string
	title := "Tests"
	if tv.ScopeName != "" {
		title += " · " + tv.ScopeName
	}
	if view != nil {
		title = fmt.Sprintf("%s %d", title, view.Counts.Total())
	}
	lines = append(lines, out.String(clipPlain(title, width)).Bold().String())
	lines = append(lines, out.String(strings.Repeat(HorizBar, max(width, 0))).Foreground(termenv.ANSIBrightBlack).Faint().String())

	rowsHeight := max(height-len(lines), 0)
	start := 0
	if selectedIdx >= rowsHeight && rowsHeight > 0 {
		start = selectedIdx - rowsHeight/2
		if start+rowsHeight > len(rows) {
			start = max(len(rows)-rowsHeight, 0)
		}
	}
	end := min(start+rowsHeight, len(rows))
	if start > 0 {
		lines = append(lines, out.String(fmt.Sprintf("… %d above", start)).Foreground(termenv.ANSIBrightBlack).Faint().String())
	}
	for i := start; i < end && len(lines) < height; i++ {
		lines = append(lines, tv.renderSidebarRow(out, rows[i], i == selectedIdx, width))
	}
	if end < len(rows) && len(lines) < height {
		lines = append(lines, out.String(fmt.Sprintf("… %d below", len(rows)-end)).Foreground(termenv.ANSIBrightBlack).Faint().String())
	}
	return lines
}

func (tv *TestView) renderSidebarRow(out *termenv.Output, row testSidebarRow, selected bool, width int) string {
	node := row.node
	color := testCategoryColor(node.Category)
	selector := " "
	if selected {
		selector = out.String(CaretRightFilled).Foreground(termenv.ANSIWhite).Bold().String()
	}
	iconStyle := out.String(testCategoryIcon(node.Category)).Foreground(color)
	nameStyle := out.String(clipPlain(testNodeDisplayName(node), max(width-8-row.depth*2, 1))).Foreground(testNodeNameColor(node))
	if selected {
		iconStyle = hl(iconStyle)
		nameStyle = hl(nameStyle).Bold()
	}
	indent := strings.Repeat("  ", row.depth)
	count := ""
	if node.Kind != dagui.TestNodeCase || len(node.Children) > 0 {
		count = out.String(fmt.Sprintf(" %d", node.Counts.Total())).Foreground(termenv.ANSIBrightBlack).Faint().String()
	}
	return selector + " " + iconStyle.String() + " " + indent + nameStyle.String() + count
}

func (tv *TestView) renderDetailLines(out *termenv.Output, node *dagui.TestNode, width, height int) []string {
	if node == nil {
		return []string{out.String("No test selected").Foreground(termenv.ANSIBrightBlack).String()}
	}
	span := node.Span
	representative := testTUISpan(node)
	color := testCategoryColor(node.Category)
	var lines []string

	titlePrefix := "Test"
	if node.Kind == dagui.TestNodeSuite || node.Kind == dagui.TestNodeVirtualSuite {
		titlePrefix = "Suite"
	}
	title := fmt.Sprintf("%s %s", titlePrefix, testNodeDisplayName(node))
	lines = append(lines, out.String(clipPlain(title, width)).Foreground(color).Bold().String())
	lines = append(lines, out.String(strings.Repeat(HorizBar, max(width, 0))).Foreground(termenv.ANSIBrightBlack).Faint().String())

	lines = append(lines, tv.renderSummaryLine(out, node, width))
	if node.FullName != "" && node.FullName != node.Name {
		lines = append(lines, out.String(clipPlain(node.FullName, width)).Foreground(termenv.ANSIBrightBlack).Faint().String())
	}
	if node.Kind == dagui.TestNodeVirtualSuite {
		meta := "virtual suite · no backing span"
		if representative != nil {
			meta += fmt.Sprintf(" · representative %s", representative.ID)
		}
		lines = append(lines, out.String(clipPlain(meta, width)).Foreground(termenv.ANSIBrightBlack).Faint().String())
	} else if span != nil {
		dur := dagui.FormatDuration(testSpanDuration(span))
		meta := fmt.Sprintf("span %s · %s", span.ID, dur)
		lines = append(lines, out.String(clipPlain(meta, width)).Foreground(termenv.ANSIBrightBlack).Faint().String())
	} else {
		lines = append(lines, out.String("no backing span").Foreground(termenv.ANSIBrightBlack).Faint().String())
	}

	if len(node.Children) > 0 {
		lines = append(lines, "")
		lines = append(lines, out.String("Children").Foreground(termenv.ANSIBrightBlack).Bold().String())
		childRows := flattenChildTestRows(node.Children)
		for i, child := range childRows {
			if i >= 8 {
				lines = append(lines, out.String(fmt.Sprintf("… %d more", len(childRows)-i)).Foreground(termenv.ANSIBrightBlack).Faint().String())
				break
			}
			lines = append(lines, renderTestChildLine(out, child, width))
		}
	}

	lines = append(lines, "")
	lines = append(lines, out.String("Logs").Foreground(termenv.ANSIBrightBlack).Bold().String())
	if span == nil {
		lines = append(lines, out.String("No direct logs for a virtual suite.").Foreground(termenv.ANSIBrightBlack).Faint().String())
		return cropLines(lines, height)
	}
	logs := tv.Logs[span.ID]
	if logs == nil || logs.UsedHeight() == 0 {
		lines = append(lines, out.String("No logs for selected test span.").Foreground(termenv.ANSIBrightBlack).Faint().String())
		return cropLines(lines, height)
	}
	logs.SetWidth(width)
	logs.SetHeight(max(height-len(lines), 1))
	view := strings.TrimSuffix(logs.View(), "\n")
	if view == "" {
		lines = append(lines, out.String("No logs for selected test span.").Foreground(termenv.ANSIBrightBlack).Faint().String())
		return cropLines(lines, height)
	}
	lines = append(lines, strings.Split(view, "\n")...)
	return cropLines(lines, height)
}

func (tv *TestView) renderSummaryLine(out *termenv.Output, node *dagui.TestNode, width int) string {
	counts := node.Counts
	parts := []string{
		out.String(testCategoryIcon(node.Category)).Foreground(testCategoryColor(node.Category)).String(),
		out.String(node.Category.String()).Foreground(testCategoryColor(node.Category)).String(),
		out.String(fmt.Sprintf("%d tests", counts.Total())).Foreground(termenv.ANSIBrightBlack).Faint().String(),
	}
	if counts.Failing > 0 {
		parts = append(parts, out.String(fmt.Sprintf("%d failing", counts.Failing)).Foreground(termenv.ANSIRed).String())
	}
	if counts.Running > 0 {
		parts = append(parts, out.String(fmt.Sprintf("%d running", counts.Running)).Foreground(termenv.ANSIYellow).String())
	}
	if counts.Passing > 0 {
		parts = append(parts, out.String(fmt.Sprintf("%d passing", counts.Passing)).Foreground(termenv.ANSIGreen).String())
	}
	if counts.Skipped > 0 {
		parts = append(parts, out.String(fmt.Sprintf("%d skipped", counts.Skipped)).Foreground(termenv.ANSIBrightBlack).String())
	}
	return clipANSI(strings.Join(parts, " · "), width)
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
		fe.testViews[root] = tv
	}
	return tv
}

func (fe *frontendPretty) newTestView(root dagui.SpanID, scopeName string) *TestView {
	tv := &TestView{
		Profile:   fe.profile,
		Logs:      fe.logs.Logs,
		ScopeName: scopeName,
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
	if fe.fullscreenTests != nil {
		fe.fullscreenTests.GoStart()
		fe.Update()
	}
}

func (fe *frontendPretty) goTestEnd() {
	if fe.fullscreenTests != nil {
		fe.fullscreenTests.GoEnd()
		fe.Update()
	}
}

func (fe *frontendPretty) goTestUp() {
	if fe.fullscreenTests != nil {
		fe.fullscreenTests.GoUp()
		fe.Update()
	}
}

func (fe *frontendPretty) goTestDown() {
	if fe.fullscreenTests != nil {
		fe.fullscreenTests.GoDown()
		fe.Update()
	}
}

func (fe *frontendPretty) openFocusedTestTrace() {
	if fe.fullscreenTests == nil {
		return
	}
	span := fe.fullscreenTests.FocusedSpan()
	if span == nil {
		return
	}
	fe.testsMode = false
	fe.fullscreenTests = nil
	fe.testsReturnSpan = dagui.SpanID{}
	fe.ZoomedSpan = span.ID
	fe.FocusedSpan = span.ID
	fe.renderVersion++
	fe.recalculateViewLocked()
	if fe.keymapBar != nil {
		fe.keymapBar.Update()
	}
	fe.Update()
}

func (fe *frontendPretty) renderTestsView(ctx tuist.Context) {
	if fe.fullscreenTests == nil {
		return
	}
	fe.RenderChild(ctx, fe.fullscreenTests)
}

func (fe *frontendPretty) shouldRenderInlineTests(row *dagui.TraceRow) bool {
	if fe.finalRender || row == nil || row.Span == nil || row.Expanded || row.Span.CheckName == "" {
		return false
	}
	return fe.db.TestViewForSpan(row.Span).HasTests()
}

func (s *SpanTreeView) renderInlineTests(ctx tuist.Context, r *renderer, row *dagui.TraceRow) []string {
	if !s.fe.shouldRenderInlineTests(row) {
		return nil
	}
	tv := s.fe.inlineTestView(row.Span.ID)
	limit := max(s.fe.window.Height/3, 6)
	if tv.MaxHeight != limit {
		tv.MaxHeight = limit
		tv.Update()
	}

	prefixBuf := new(strings.Builder)
	prefixOut := NewOutput(prefixBuf, termenv.WithProfile(s.fe.profile))
	r.indentFunc = s.indentFunc(prefixOut)
	r.fancyIndent(prefixOut, row, false, false)
	pipe := prefixOut.String(VertBoldBar).Foreground(restrainedStatusColor(row.Span))
	if row.Span.ID == s.fe.FocusedSpan && !s.fe.editlineFocused {
		pipe = hl(pipe)
	}
	fmt.Fprint(prefixOut, pipe.String())
	fmt.Fprint(prefixOut, " ")
	prefix := prefixBuf.String()

	width := max(ctx.Width-lipgloss.Width(prefix), 1)
	result := s.RenderChildResult(ctx.Resize(width, limit), tv)
	lines := make([]string, len(result.Lines))
	for i, line := range result.Lines {
		lines[i] = prefix + line
	}
	return lines
}

func flattenTestRows(view *dagui.TestView) []testSidebarRow {
	if view == nil {
		return nil
	}
	var rows []testSidebarRow
	appendTestRows(&rows, view.Roots, 0)
	return rows
}

func appendTestRows(rows *[]testSidebarRow, nodes []*dagui.TestNode, depth int) {
	partition := dagui.PartitionTests(nodes)
	groups := [][]*dagui.TestNode{
		partition.Failing,
		partition.Running,
		partition.Suites,
		partition.Mixed,
		partition.Passing,
		partition.Skipped,
	}
	for _, group := range groups {
		for _, node := range group {
			*rows = append(*rows, testSidebarRow{node: node, depth: depth})
			appendTestRows(rows, node.Children, depth+1)
		}
	}
}

func flattenChildTestRows(nodes []*dagui.TestNode) []testSidebarRow {
	var rows []testSidebarRow
	appendTestRows(&rows, nodes, 0)
	return rows
}

func renderTestChildLine(out *termenv.Output, row testSidebarRow, width int) string {
	node := row.node
	indent := strings.Repeat("  ", row.depth)
	icon := out.String(testCategoryIcon(node.Category)).Foreground(testCategoryColor(node.Category)).String()
	name := out.String(clipPlain(testNodeDisplayName(node), max(width-6-row.depth*2, 1))).Foreground(testNodeNameColor(node)).String()
	return clipANSI("  "+icon+" "+indent+name, width)
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
