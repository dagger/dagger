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

func (fe *frontendPretty) toggleTestsMode() {
	if !fe.testsMode && !fe.db.HasTests() {
		return
	}
	fe.testsMode = !fe.testsMode
	if fe.testsMode {
		fe.ensureFocusedTest(fe.db.TestView())
	} else {
		fe.recalculateViewLocked()
	}
	if fe.keymapBar != nil {
		fe.keymapBar.Update()
	}
	fe.Update()
}

func (fe *frontendPretty) focusedTestNode(view *dagui.TestView) *dagui.TestNode {
	if view == nil || fe.focusedTest == "" {
		return nil
	}
	return view.ByID[fe.focusedTest]
}

func (fe *frontendPretty) ensureFocusedTest(view *dagui.TestView) ([]testSidebarRow, int) {
	rows := flattenTestRows(view)
	if len(rows) == 0 {
		fe.focusedTest = ""
		return rows, -1
	}
	for i, row := range rows {
		if row.node.ID == fe.focusedTest {
			fe.focusTestNode(row.node)
			return rows, i
		}
	}
	fe.focusTestNode(rows[0].node)
	return rows, 0
}

func (fe *frontendPretty) focusTestNode(node *dagui.TestNode) {
	if node == nil {
		fe.focusedTest = ""
		return
	}
	fe.focusedTest = node.ID
	if span := testTUISpan(node); span != nil {
		fe.FocusedSpan = span.ID
	}
}

func (fe *frontendPretty) goTestStart() {
	rows, _ := fe.ensureFocusedTest(fe.db.TestView())
	if len(rows) > 0 {
		fe.focusTestNode(rows[0].node)
		fe.Update()
	}
}

func (fe *frontendPretty) goTestEnd() {
	rows, _ := fe.ensureFocusedTest(fe.db.TestView())
	if len(rows) > 0 {
		fe.focusTestNode(rows[len(rows)-1].node)
		fe.Update()
	}
}

func (fe *frontendPretty) goTestUp() {
	rows, idx := fe.ensureFocusedTest(fe.db.TestView())
	if idx > 0 {
		fe.focusTestNode(rows[idx-1].node)
		fe.Update()
	}
}

func (fe *frontendPretty) goTestDown() {
	rows, idx := fe.ensureFocusedTest(fe.db.TestView())
	if idx >= 0 && idx+1 < len(rows) {
		fe.focusTestNode(rows[idx+1].node)
		fe.Update()
	}
}

func (fe *frontendPretty) openFocusedTestTrace() {
	view := fe.db.TestView()
	node := fe.focusedTestNode(view)
	span := testTUISpan(node)
	if span == nil {
		return
	}
	fe.testsMode = false
	fe.ZoomedSpan = span.ID
	fe.FocusedSpan = span.ID
	fe.renderVersion++
	fe.recalculateViewLocked()
	if fe.keymapBar != nil {
		fe.keymapBar.Update()
	}
	fe.Update()
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

func (fe *frontendPretty) renderTestsView(ctx tuist.Context) {
	view := fe.db.TestView()
	rows, selectedIdx := fe.ensureFocusedTest(view)

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

	// Leave room for the keymap sibling and a small visual gap.
	viewportHeight := max(ctx.ScreenHeight()-2, 1)

	outBuf := new(strings.Builder)
	out := NewOutput(outBuf, termenv.WithProfile(fe.profile))

	if len(rows) == 0 {
		ctx.Line(out.String("No tests discovered yet").Foreground(termenv.ANSIBrightBlack).String())
		return
	}

	selected := rows[selectedIdx].node
	left := fe.renderTestSidebarLines(out, view, rows, selectedIdx, leftWidth, viewportHeight)
	right := fe.renderTestDetailLines(out, selected, rightWidth, viewportHeight)
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

func (fe *frontendPretty) renderTestSidebarLines(out *termenv.Output, view *dagui.TestView, rows []testSidebarRow, selectedIdx, width, height int) []string {
	var lines []string
	title := fmt.Sprintf("Tests %d", view.Counts.Total())
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
		lines = append(lines, fe.renderTestSidebarRow(out, rows[i], i == selectedIdx, width))
	}
	if end < len(rows) && len(lines) < height {
		lines = append(lines, out.String(fmt.Sprintf("… %d below", len(rows)-end)).Foreground(termenv.ANSIBrightBlack).Faint().String())
	}
	return lines
}

func (fe *frontendPretty) renderTestSidebarRow(out *termenv.Output, row testSidebarRow, selected bool, width int) string {
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

func (fe *frontendPretty) renderTestDetailLines(out *termenv.Output, node *dagui.TestNode, width, height int) []string {
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

	lines = append(lines, fe.renderTestSummaryLine(out, node, width))
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
	logs := fe.logs.Logs[span.ID]
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

func (fe *frontendPretty) renderTestSummaryLine(out *termenv.Output, node *dagui.TestNode, width int) string {
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
	if node.FullName != "" {
		return node.FullName
	}
	return node.Name
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
