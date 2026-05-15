package idtui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
	"go.opentelemetry.io/otel/trace"
)

func testSidebarCase(id, name string, category dagui.TestCategory) *dagui.TestNode {
	counts := dagui.TestCounts{}
	switch category {
	case dagui.TestCategoryFailing:
		counts.Failing = 1
	case dagui.TestCategoryRunning:
		counts.Running = 1
	case dagui.TestCategorySkipped:
		counts.Skipped = 1
	default:
		counts.Passing = 1
	}
	return &dagui.TestNode{
		ID:       dagui.TestNodeID(id),
		Kind:     dagui.TestNodeCase,
		Name:     name,
		FullName: name,
		Category: category,
		Counts:   counts,
	}
}

func TestTestViewCollapsesPassedSidebarRows(t *testing.T) {
	passingA := testSidebarCase("pass-a", "pass-a", dagui.TestCategoryPassing)
	passingB := testSidebarCase("pass-b", "pass-b", dagui.TestCategoryPassing)
	failing := testSidebarCase("fail", "fail", dagui.TestCategoryFailing)
	view := &dagui.TestView{
		Roots: []*dagui.TestNode{passingA, passingB, failing},
		Counts: dagui.TestCounts{
			Failing: 1,
			Passing: 2,
		},
	}
	tv := &TestView{View: func() *dagui.TestView { return view }}

	rows, _ := tv.ensureFocusedTest(view)
	if len(rows) != 2 {
		t.Fatalf("expected failing row plus collapsed passed group, got %d rows", len(rows))
	}
	if rows[0].node != failing {
		t.Fatalf("expected failing row first, got %#v", rows[0].node)
	}
	if rows[1].kind != testSidebarPassedGroup || rows[1].counts.Total() != 2 {
		t.Fatalf("expected collapsed passed group for two tests, got %#v", rows[1])
	}

	tv.focusSidebarRow(rows[1])
	if !tv.ToggleFocusedGroup() {
		t.Fatal("expected enter action to expand selected passed group")
	}
	rows, _ = tv.ensureFocusedTest(view)
	if len(rows) != 4 {
		t.Fatalf("expected expanded group plus two passing tests, got %d rows", len(rows))
	}
	if rows[1].kind != testSidebarPassedGroup || !rows[1].expanded {
		t.Fatalf("expected expanded passed group row to remain selectable, got %#v", rows[1])
	}
	if rows[2].node != passingA || rows[3].node != passingB {
		t.Fatalf("expected passing tests after expanded group, got %#v %#v", rows[2].node, rows[3].node)
	}
}

func TestTestViewDetailDoesNotRenderSubtestsAsChildren(t *testing.T) {
	parent := testSidebarCase("parent", "parent", dagui.TestCategoryFailing)
	parent.Counts = dagui.TestCounts{Failing: 1, Passing: 1}
	child := testSidebarCase("child", "child", dagui.TestCategoryPassing)
	child.Parent = parent
	parent.Children = append(parent.Children, child)

	var buf strings.Builder
	out := NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	tv := &TestView{}
	lines := tv.renderDetailLines(tuist.Context{}, out, testSidebarRow{kind: testSidebarNode, node: parent}, 80, 80)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "Children") || strings.Contains(joined, "child") {
		t.Fatalf("expected detail pane not to render test subtests as child spans, got:\n%s", joined)
	}
}

type staticLinesComponent struct {
	tuist.Compo
	lines []string
}

func (c *staticLinesComponent) Render(ctx tuist.Context) {
	ctx.Lines(c.lines...)
}

func TestTestViewDetailTitleUsesTestName(t *testing.T) {
	span := &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{Name: "span operation name"}}
	node := &dagui.TestNode{
		ID:       "test",
		Kind:     dagui.TestNodeCase,
		Name:     "span operation name",
		FullName: "pkg.TestThing",
		Span:     span,
		Category: dagui.TestCategoryPassing,
		Counts:   dagui.TestCounts{Passing: 1},
	}

	var buf strings.Builder
	out := NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	tv := &TestView{}
	lines := tv.renderDetailLines(tuist.Context{}, out, testSidebarRow{kind: testSidebarNode, node: node}, 80, 80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(lines[0], "pkg.TestThing") {
		t.Fatalf("expected detail title to use full test name, got %q", lines[0])
	}
	if strings.Contains(joined, "span operation name") {
		t.Fatalf("expected detail pane not to show span name, got:\n%s", joined)
	}
	if strings.Count(joined, "pkg.TestThing") != 1 {
		t.Fatalf("expected full test name only in title, got:\n%s", joined)
	}
	if strings.Contains(joined, "LOGS") {
		t.Fatalf("expected no logs UI when logs are absent, got:\n%s", joined)
	}
}

func TestTestViewDetailLogSectionUsesCompactHeader(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	spanID := dagui.SpanID{SpanID: trace.SpanID{1}}
	span := &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{ID: spanID, Name: "span operation name"}}
	node := &dagui.TestNode{
		ID:       "test",
		Kind:     dagui.TestNodeCase,
		Name:     "span operation name",
		FullName: "pkg.TestThing",
		Span:     span,
		Category: dagui.TestCategoryFailing,
		Counts:   dagui.TestCounts{Failing: 1},
	}
	logs := NewVterm(termenv.Ascii)
	logs.SetWidth(80)
	_, _ = logs.Write([]byte("boom\n"))

	var buf strings.Builder
	out := NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	tv := &TestView{Logs: map[dagui.SpanID]*Vterm{spanID: logs}}
	lines := tv.renderDetailLines(tuist.Context{}, out, testSidebarRow{kind: testSidebarNode, node: node}, 80, 80)
	joined := strings.Join(lines, "\n")
	plain := stripANSITest(joined)
	plainLines := strings.Split(plain, "\n")
	if len(plainLines) < 3 || !strings.HasPrefix(strings.TrimRight(plainLines[2], " "), "LOGS L inspect") {
		t.Fatalf("expected logs splitter immediately after title, got:\n%s", joined)
	}
	if !strings.Contains(plainLines[2], HorizBar) {
		t.Fatalf("expected logs splitter to fill with horizontal bar, got %q", plainLines[2])
	}
	for _, noise := range []string{"failing · 1 tests", "Logs · pkg.TestThing", "press L to open"} {
		if strings.Contains(plain, noise) {
			t.Fatalf("expected detail pane not to contain %q, got:\n%s", noise, joined)
		}
	}
	if !strings.Contains(plain, "boom") {
		t.Fatalf("expected rendered logs, got:\n%s", joined)
	}
}

var ansiRETest = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSITest(s string) string {
	return ansiRETest.ReplaceAllString(s, "")
}

func TestTestViewDetailLogsRenderAboveChildrenWithSplitter(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	spanID := dagui.SpanID{SpanID: trace.SpanID{1}}
	span := &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{ID: spanID, Name: "span operation name"}}
	node := &dagui.TestNode{
		ID:       "test",
		Kind:     dagui.TestNodeCase,
		Name:     "span operation name",
		FullName: "pkg.TestThing",
		Span:     span,
		Category: dagui.TestCategoryFailing,
		Counts:   dagui.TestCounts{Failing: 1},
	}
	logs := NewVterm(termenv.Ascii)
	logs.SetWidth(80)
	_, _ = logs.Write([]byte("boom\n"))

	var buf strings.Builder
	out := NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	tv := &TestView{
		Logs: map[dagui.SpanID]*Vterm{spanID: logs},
		SpanChildren: func(*dagui.Span) tuist.Component {
			return &staticLinesComponent{lines: []string{"child one", "child two"}}
		},
	}
	lines := tv.renderDetailLines(tuist.Context{}, out, testSidebarRow{kind: testSidebarNode, node: node}, 80, 80)
	plainLines := strings.Split(stripANSITest(strings.Join(lines, "\n")), "\n")
	logIdx, boomIdx, splitIdx, childIdx := -1, -1, -1, -1
	for i, line := range plainLines {
		switch {
		case strings.HasPrefix(strings.TrimRight(line, " "), "LOGS L inspect"):
			logIdx = i
		case strings.Contains(line, "boom"):
			boomIdx = i
		case strings.Contains(line, HorizBar) && strings.Trim(strings.TrimSpace(line), HorizBar) == "" && i > 0 && boomIdx >= 0 && splitIdx < 0:
			splitIdx = i
		case strings.Contains(line, "child one"):
			childIdx = i
		}
	}
	if !(logIdx >= 0 && boomIdx > logIdx && splitIdx > boomIdx && childIdx > splitIdx) {
		t.Fatalf("expected logs, splitter, then children; got:\n%s", strings.Join(plainLines, "\n"))
	}
}

func TestTestViewDetailLogsAreNotFocusable(t *testing.T) {
	spanID := dagui.SpanID{SpanID: trace.SpanID{1}}
	span := &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{ID: spanID, Name: "span operation name"}}
	node := &dagui.TestNode{
		ID:       "test",
		Kind:     dagui.TestNodeCase,
		Name:     "span operation name",
		FullName: "pkg.TestThing",
		Span:     span,
		Category: dagui.TestCategoryFailing,
		Counts:   dagui.TestCounts{Failing: 1},
	}
	view := &dagui.TestView{Roots: []*dagui.TestNode{node}, Counts: dagui.TestCounts{Failing: 1}}
	logs := NewVterm(termenv.Ascii)
	logs.SetWidth(80)
	_, _ = logs.Write([]byte("boom\n"))
	tv := &TestView{
		View: func() *dagui.TestView { return view },
		Logs: map[dagui.SpanID]*Vterm{spanID: logs},
	}
	tv.ensureFocusedTest(view)

	if tv.FocusedNodeCanFocusDetail() {
		t.Fatal("expected logs-only detail not to be focusable")
	}
}

func TestSpanChildrenCropKeepsFocusedLineVisible(t *testing.T) {
	spanA := dagui.SpanID{SpanID: trace.SpanID{1}}
	spanB := dagui.SpanID{SpanID: trace.SpanID{2}}
	a := &SpanTreeView{spanID: spanA, selfLineCount: 5}
	b := &SpanTreeView{spanID: spanB, selfLineCount: 1}
	v := &TestSpanChildrenView{
		focusedSpan: spanB,
		container:   &tuist.Container{Children: []tuist.Component{a, b}},
		scope: spanTreeScope{spanTrees: map[dagui.SpanID]*SpanTreeView{
			spanA: a,
			spanB: b,
		}},
	}
	lines := []string{"0", "1", "2", "3", "4", "5", "6", "7"}

	got := v.cropRenderedLines(lines, 3)
	if strings.Join(got, "") != "456" {
		t.Fatalf("cropped lines = %#v, want focused line visible", got)
	}
}

func TestAllocateDetailSectionHeights(t *testing.T) {
	for _, tc := range []struct {
		name                string
		childNeed, logNeed  int
		height              int
		wantChild, wantLogs int
	}{
		{name: "both capped", childNeed: 100, logNeed: 100, height: 10, wantChild: 5, wantLogs: 5},
		{name: "logs grow", childNeed: 2, logNeed: 100, height: 10, wantChild: 2, wantLogs: 8},
		{name: "children grow", childNeed: 100, logNeed: 2, height: 10, wantChild: 8, wantLogs: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotChild, gotLogs := allocateDetailSectionHeights(tc.childNeed, tc.logNeed, tc.height)
			if gotChild != tc.wantChild || gotLogs != tc.wantLogs {
				t.Fatalf("got %d/%d, want %d/%d", gotChild, gotLogs, tc.wantChild, tc.wantLogs)
			}
		})
	}
}

func TestLogPagerTitleUsesLogsHeadingAndTestName(t *testing.T) {
	logs := NewVterm(termenv.Ascii)
	logs.SetWidth(80)
	logs.SetHeight(10)
	_, _ = logs.Write([]byte("boom\n"))
	pager := &LogPagerView{
		Title:     "pkg.TestThing",
		TitleIcon: IconFailure,
		Logs:      logs,
	}

	got := pager.titleText()
	if got != "LOGS · ✘ pkg.TestThing · 1 line · 100%" {
		t.Fatalf("log pager title = %q", got)
	}
}

func TestTestViewPrettyReportSummary(t *testing.T) {
	spanID := func(id byte) dagui.SpanID {
		return dagui.SpanID{SpanID: trace.SpanID{id}}
	}
	suiteSpan := &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{ID: spanID(1), Name: "suite"}}
	failSpan := &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{ID: spanID(2), Name: "failing"}}
	skipSpan := &dagui.Span{SpanSnapshot: dagui.SpanSnapshot{ID: spanID(3), Name: "skipped"}}
	suite := &dagui.TestNode{
		ID:       "suite",
		Kind:     dagui.TestNodeSuite,
		Name:     "suite",
		Span:     suiteSpan,
		Category: dagui.TestCategoryFailing,
		Counts:   dagui.TestCounts{Failing: 1, Skipped: 1},
	}
	failing := &dagui.TestNode{
		ID:           "failing",
		Kind:         dagui.TestNodeCase,
		Name:         "failing with a long name that should not clip",
		Span:         failSpan,
		Parent:       suite,
		SelfCategory: dagui.TestCategoryFailing,
		Category:     dagui.TestCategoryFailing,
		Counts:       dagui.TestCounts{Failing: 1},
	}
	skipped := &dagui.TestNode{
		ID:           "skipped",
		Kind:         dagui.TestNodeCase,
		Name:         "skipped",
		Span:         skipSpan,
		Parent:       suite,
		SelfCategory: dagui.TestCategorySkipped,
		Category:     dagui.TestCategorySkipped,
		Counts:       dagui.TestCounts{Skipped: 1},
	}
	suite.Children = []*dagui.TestNode{failing, skipped}
	empty := &dagui.TestNode{
		ID:       "empty",
		Kind:     dagui.TestNodeSuite,
		Name:     "empty",
		Category: dagui.TestCategorySkipped,
	}
	failLogs := NewVterm(termenv.Ascii)
	_, _ = failLogs.Write([]byte("boom with a long line that should not wrap or clip\nmore\n"))
	skipLogs := NewVterm(termenv.Ascii)
	_, _ = skipLogs.Write([]byte("skip reason\n"))
	view := &dagui.TestView{
		Roots: []*dagui.TestNode{suite, empty},
		Counts: dagui.TestCounts{
			Failing: 1,
			Skipped: 1,
			Passing: 2,
		},
	}
	tv := &TestView{
		SummaryIndent:   2,
		SummaryLogLines: -1,
		Logs: map[dagui.SpanID]*Vterm{
			failSpan.ID: failLogs,
			skipSpan.ID: skipLogs,
		},
	}

	var buf strings.Builder
	out := NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	got := strings.Join(tv.renderTestSummaryLines(out, view, 20, 80), "\n")
	want := strings.Join([]string{
		"  TESTS",
		"    ✘ suite › failing with a long name that should not clip FAIL",
		"          boom with a long line that should not wrap or clip",
		"          more",
		"",
		"    ∅ suite › skipped SKIP",
		"          skip reason",
		"",
		"    ∅ empty NO TESTS",
		"",
		"    ✘ 1 failed",
		"    ∅ 1 skipped",
		"    ✔ 2 passed",
	}, "\n")
	if got != want {
		t.Fatalf("unexpected pretty report summary:\n%s", got)
	}
}

func TestTestViewInspectorHeaderUsesReportCounts(t *testing.T) {
	view := &dagui.TestView{Counts: dagui.TestCounts{
		Failing: 1,
		Skipped: 2,
		Passing: 3,
		Running: 4,
	}}

	var buf strings.Builder
	out := NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	got := renderTestInspectorHeader(out, view, 80)
	want := "TESTS  ✘ 1 failed  ∅ 2 skipped  ✔ 3 passed  ◐ 4 running"
	if got != want {
		t.Fatalf("inspector header = %q, want %q", got, want)
	}
}

func TestTestViewSidebarCutoffShowsMoreTests(t *testing.T) {
	var roots []*dagui.TestNode
	for i := range 10 {
		roots = append(roots, testSidebarCase("fail-"+string(rune('a'+i)), "fail-"+string(rune('a'+i)), dagui.TestCategoryFailing))
	}
	view := &dagui.TestView{Roots: roots, Counts: dagui.TestCounts{Failing: 10}}
	tv := &TestView{}
	rows, selected := tv.ensureFocusedTest(view)

	var buf strings.Builder
	out := NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	lines := tv.renderSidebarLines(out, view, rows, selected, 40, 5)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "... 8 more tests ...") {
		t.Fatalf("expected cutoff marker with hidden test count, got:\n%s", joined)
	}
}
