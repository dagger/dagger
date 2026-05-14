package idtui

import (
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
		Name:         "failing",
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
	_, _ = failLogs.Write([]byte("boom\nmore\n"))
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
	got := strings.Join(tv.renderTestSummaryLines(out, view, 80, 80), "\n")
	want := strings.Join([]string{
		"  TESTS",
		"    ✘ suite › failing FAIL",
		"          boom",
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
