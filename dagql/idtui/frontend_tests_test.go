package idtui

import (
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
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

func TestTestViewChildrenAreNotCapped(t *testing.T) {
	parent := testSidebarCase("parent", "parent", dagui.TestCategoryFailing)
	parent.Counts = dagui.TestCounts{Failing: 1, Passing: 10}
	for i := range 10 {
		child := testSidebarCase("child-"+string(rune('a'+i)), "child-"+string(rune('a'+i)), dagui.TestCategoryPassing)
		child.Parent = parent
		parent.Children = append(parent.Children, child)
	}

	var buf strings.Builder
	out := NewOutput(&buf, termenv.WithProfile(termenv.Ascii))
	tv := &TestView{}
	lines := tv.renderDetailLines(out, testSidebarRow{kind: testSidebarNode, node: parent}, 80, 80)
	joined := strings.Join(lines, "\n")
	for i := range 10 {
		name := "child-" + string(rune('a'+i))
		if !strings.Contains(joined, name) {
			t.Fatalf("expected detail pane to include %q; got:\n%s", name, joined)
		}
	}
	if strings.Contains(joined, "more") {
		t.Fatalf("expected no artificial child cap, got:\n%s", joined)
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
