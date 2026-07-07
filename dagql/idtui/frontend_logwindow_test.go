package idtui

import (
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/dagui"
)

func TestErrorTailStart(t *testing.T) {
	const context = 5

	cases := []struct {
		name    string
		context int
		lines   []string
		want    int
	}{
		{
			name:    "no matches renders everything",
			context: context,
			lines:   []string{"a", "b", "c", "d"},
			want:    0,
		},
		{
			name:    "single match keeps context lines before it",
			context: context,
			lines:   []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "FAIL"},
			want:    4, // 9 - 5
		},
		{
			name:    "match within context of the top clamps to zero",
			context: context,
			lines:   []string{"0", "1", "boom: error", "3"},
			want:    0, // max(2-5, 0)
		},
		{
			name:    "trailing cluster anchors on its first line",
			context: context,
			// matches at 7,8,9,11,12 -- bridged into one cluster starting at 7.
			lines: []string{
				"0", "1", "2", "3", "4", "5", "6",
				"INTERNAL_ERROR from peer", // 7
				"FAIL pkg [setup failed]",  // 8
				"FAIL",                     // 9
				"    testctx.go:174:",      // 10
				"        Error Trace: ...", // 11
				"        Error: boom",      // 12
				"        Test: X",          // 13
			},
			want: 2, // anchor 7 - 5
		},
		{
			name:    "incidental match far above the cluster is excluded",
			context: context,
			// index 1 ("errors" package) is separated from the cluster by a big
			// gap, so the walk stops and it stays trimmed.
			lines: func() []string {
				ls := make([]string, 0, 20)
				ls = append(ls, "go: downloading github.com/pkg/errors v0.9.1") // 0
				for i := 1; i < 17; i++ {
					ls = append(ls, "go: downloading x")
				}
				ls = append(ls, "boom: error") // 17
				ls = append(ls, "FAIL")        // 18
				ls = append(ls, "trailing")    // 19
				return ls
			}(),
			want: 12, // anchor 17 - 5; index 0 excluded
		},
		{
			name:    "gap equal to context+1 bridges",
			context: context,
			// matches at 1 and 7: gap 6 == context+1, so bridged to anchor 1.
			lines: []string{"0", "error", "2", "3", "4", "5", "6", "FAIL", "8"},
			want:  0, // max(1-5, 0)
		},
		{
			name:    "gap larger than context+1 does not bridge",
			context: context,
			// matches at 0 and 7: gap 7 > context+1, walk stops at the cluster (7).
			lines: []string{"error", "1", "2", "3", "4", "5", "6", "FAIL", "8"},
			want:  2, // anchor 7 - 5
		},
		{
			name:    "case insensitive matching",
			context: context,
			lines:   []string{"0", "1", "2", "3", "4", "5", "6", "7", "ERROR", "FaIlEd"},
			want:    3, // cluster {8,9} -> anchor 8, then 8 - 5
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := errorTailStart(tc.lines, tc.context)
			if got != tc.want {
				t.Fatalf("errorTailStart() = %d, want %d\nlines:\n%s",
					got, tc.want, strings.Join(tc.lines, "\n"))
			}
		})
	}
}

// TestErrorTailStartLitmus mirrors the real failing-test capture: leading
// dependency-download noise (one line of which incidentally contains "errors"),
// then the actual failure cluster at the end. The window must start a few lines
// before the cluster -- keeping INTERNAL_ERROR and [setup failed] -- not at the
// incidental match up top, nor at the trailing assertion.
func TestErrorTailStartLitmus(t *testing.T) {
	lines := make([]string, 0, 306)
	for i := 0; i < 297; i++ {
		switch i {
		case 50:
			lines = append(lines, "go: downloading github.com/pkg/errors v0.9.1")
		case 85:
			lines = append(lines, "go: downloading github.com/hashicorp/go-multierror v1.1.1")
		default:
			lines = append(lines, "go: downloading example.com/x v1.0.0")
		}
	}
	lines = append(lines,
		"sdk/go/engineconn/engineconn.go:10:2: ... stream error: ... INTERNAL_ERROR; received from peer", // 297
		"FAIL\tgithub.com/dagger/dagger/core/integration [setup failed]",                                 // 298
		"FAIL",                                // 299
		"    testctx.go:174: ",                // 300
		"        \tError Trace:\t...",         // 301
		"        \t...",                       // 302
		"        \t...",                       // 303
		"        \tError:      \tunexpected",  // 304
		"        \tTest:       \tTestGoProxy", // 305
	)

	got := errorTailStart(lines, 5)
	if want := 292; got != want { // anchor 297 - 5
		t.Fatalf("errorTailStart() = %d, want %d", got, want)
	}
	// The kept window must include the real root cause, not just the assertion.
	kept := strings.Join(lines[got:], "\n")
	for _, must := range []string{"INTERNAL_ERROR", "[setup failed]"} {
		if !strings.Contains(kept, must) {
			t.Errorf("kept window missing %q", must)
		}
	}
	// The incidental "errors" download line is above the window.
	if strings.Contains(kept, "pkg/errors") {
		t.Errorf("kept window should have trimmed the incidental errors download line")
	}
}

func TestIsFailingLeafTestCase(t *testing.T) {
	leaf := func(cat dagui.TestCategory, children ...*dagui.TestNode) *dagui.TestNode {
		return &dagui.TestNode{
			Kind:         dagui.TestNodeCase,
			SelfCategory: cat,
			Children:     children,
		}
	}

	failingLeaf := leaf(dagui.TestCategoryFailing)
	passingLeaf := leaf(dagui.TestCategoryPassing)

	cases := []struct {
		name string
		node *dagui.TestNode
		want bool
	}{
		{"nil", nil, false},
		{"failing leaf", failingLeaf, true},
		{"passing leaf", passingLeaf, false},
		{
			name: "suite kind is never a leaf case",
			node: &dagui.TestNode{Kind: dagui.TestNodeSuite, SelfCategory: dagui.TestCategoryFailing},
			want: false,
		},
		{
			name: "failing parent with a failing child case is not a leaf",
			node: leaf(dagui.TestCategoryFailing, leaf(dagui.TestCategoryFailing)),
			want: false,
		},
		{
			name: "failing parent with only passing children is a leaf",
			node: leaf(dagui.TestCategoryFailing, passingLeaf, passingLeaf),
			want: true,
		},
		{
			name: "failing case with a failing grandchild is not a leaf",
			node: leaf(dagui.TestCategoryFailing, leaf(dagui.TestCategoryPassing, leaf(dagui.TestCategoryFailing))),
			want: false,
		},
		{
			name: "failing case with a failing case nested under a suite is not a leaf",
			node: leaf(dagui.TestCategoryFailing, &dagui.TestNode{
				Kind:     dagui.TestNodeSuite,
				Children: []*dagui.TestNode{leaf(dagui.TestCategoryFailing)},
			}),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFailingLeafTestCase(tc.node); got != tc.want {
				t.Fatalf("isFailingLeafTestCase() = %v, want %v", got, tc.want)
			}
		})
	}
}
