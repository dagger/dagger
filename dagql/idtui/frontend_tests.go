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

type testSidebarRowKind uint8

const (
	testSidebarNode testSidebarRowKind = iota
	testSidebarPassedGroup
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

	Profile termenv.Profile
	View    func() *dagui.TestView
	Logs    map[dagui.SpanID]*Vterm

	// MaxHeight caps the rendered height. A zero value means fullscreen mode:
	// use the terminal height, leaving room for the keymap sibling.
	MaxHeight int
	ScopeName string

	OnFocusSpan func(*dagui.Span)

	focusedRow           string
	expandedPassedGroups map[string]bool
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

	selected := rows[selectedIdx]
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

	listHeight := max(height-len(lines), 0)
	if listHeight == 0 || len(rows) == 0 {
		return cropLines(lines, height)
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
		lines = append(lines, tv.renderSidebarRow(out, rows[i], i == selectedIdx, width))
	}
	if bottomMarker && len(lines) < height {
		hiddenTests := countSidebarTests(rows[end:])
		label := fmt.Sprintf("... %d more tests ...", hiddenTests)
		if hiddenTests == 0 {
			label = fmt.Sprintf("... %d more items ...", len(rows)-end)
		}
		lines = append(lines, out.String(clipPlain(label, width)).Foreground(termenv.ANSIBrightBlack).Faint().String())
	}
	return cropLines(lines, height)
}

func (tv *TestView) renderSidebarRow(out *termenv.Output, row testSidebarRow, selected bool, width int) string {
	if row.kind == testSidebarPassedGroup {
		return tv.renderPassedGroupSidebarRow(out, row, selected, width)
	}
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

func (tv *TestView) renderPassedGroupSidebarRow(out *termenv.Output, row testSidebarRow, selected bool, width int) string {
	selector := " "
	if selected {
		selector = out.String(CaretRightFilled).Foreground(termenv.ANSIWhite).Bold().String()
	}
	caret := CaretRightFilled
	if row.expanded {
		caret = CaretDownFilled
	}
	caretStyle := out.String(caret).Foreground(termenv.ANSIBrightBlack)
	iconStyle := out.String(IconSuccess).Foreground(termenv.ANSIGreen)
	label := fmt.Sprintf("%d passed", row.counts.Total())
	labelStyle := out.String(clipPlain(label, max(width-9-row.depth*2, 1))).Foreground(termenv.ANSIGreen)
	if selected {
		caretStyle = hl(caretStyle)
		iconStyle = hl(iconStyle)
		labelStyle = hl(labelStyle).Bold()
	}
	indent := strings.Repeat("  ", row.depth)
	return selector + " " + caretStyle.String() + " " + iconStyle.String() + " " + indent + labelStyle.String()
}

func countSidebarTests(rows []testSidebarRow) int {
	var count int
	for _, row := range rows {
		count += row.testCount()
	}
	return count
}

func (tv *TestView) renderDetailLines(out *termenv.Output, row testSidebarRow, width, height int) []string {
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
	name := out.String(clipPlain(testNodeDisplayName(node), max(width-lipgloss.Width(icon)-lipgloss.Width(duration)-1, 1))).Foreground(termenv.ANSIWhite).Bold().String()
	lines = append(lines, clipANSI(icon+" "+name+duration, width))
	lines = append(lines, out.String(strings.Repeat(HorizBar, max(width, 0))).Foreground(termenv.ANSIBrightBlack).Faint().String())

	lines = append(lines, tv.renderSummaryLine(out, node, width))
	if node.FullName != "" && node.FullName != node.Name {
		lines = append(lines, out.String(clipPlain(node.FullName, width)).Foreground(termenv.ANSIBrightBlack).Faint().String())
	}
	if node.Kind == dagui.TestNodeVirtualSuite {
		lines = append(lines, out.String(clipPlain("virtual suite · no backing span", width)).Foreground(termenv.ANSIBrightBlack).Faint().String())
	} else if span == nil {
		lines = append(lines, out.String("no backing span").Foreground(termenv.ANSIBrightBlack).Faint().String())
	}

	if len(node.Children) > 0 {
		lines = append(lines, "")
		lines = append(lines, out.String("Children").Foreground(termenv.ANSIBrightBlack).Bold().String())
		childRows := flattenChildTestRows(node.Children)
		for _, child := range childRows {
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
	if fe.fullscreenTests.ToggleFocusedGroup() {
		fe.Update()
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

func flattenChildTestRows(nodes []*dagui.TestNode) []testSidebarRow {
	var rows []testSidebarRow
	appendAllTestRows(&rows, nodes, 0)
	return rows
}

func appendAllTestRows(rows *[]testSidebarRow, nodes []*dagui.TestNode, depth int) {
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
			*rows = append(*rows, testSidebarRow{kind: testSidebarNode, node: node, depth: depth})
			appendAllTestRows(rows, node.Children, depth+1)
		}
	}
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
