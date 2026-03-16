package idtui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestLineLevelDiff(t *testing.T) {
	old := `func hello() {
	fmt.Println("hello")
}`
	new := `func hello() {
	fmt.Println("hello world")
	fmt.Println("goodbye")
}`

	lines := lineLevelDiff(old, new)

	// Verify we get context, removed, and added lines.
	var ctx, rem, add int
	for _, l := range lines {
		switch l.Kind {
		case diffContext:
			ctx++
		case diffRemoved:
			rem++
		case diffAdded:
			add++
		}
	}

	if ctx == 0 {
		t.Error("expected context lines")
	}
	if rem == 0 {
		t.Error("expected removed lines")
	}
	if add == 0 {
		t.Error("expected added lines")
	}

	// Verify context lines appear between removed/added (not all-removed then all-added).
	// The first line "func hello() {" should be context.
	if lines[0].Kind != diffContext {
		t.Errorf("expected first line to be context, got %v", lines[0].Kind)
	}
	if lines[0].Content != "func hello() {" {
		t.Errorf("unexpected first line content: %q", lines[0].Content)
	}
}

func TestPairDiffLines(t *testing.T) {
	lines := []diffLine{
		{OldNo: 1, NewNo: 1, Kind: diffContext, Content: "a"},
		{OldNo: 2, Kind: diffRemoved, Content: "old1"},
		{OldNo: 3, Kind: diffRemoved, Content: "old2"},
		{NewNo: 2, Kind: diffAdded, Content: "new1"},
		{NewNo: 3, Kind: diffAdded, Content: "new2"},
		{NewNo: 4, Kind: diffAdded, Content: "new3"},
		{OldNo: 4, NewNo: 5, Kind: diffContext, Content: "b"},
	}

	pairs := pairDiffLines(lines)

	// Should have: 1 context + 2 paired + 1 right-only + 1 context = 5 pairs
	if len(pairs) != 5 {
		t.Fatalf("expected 5 pairs, got %d", len(pairs))
	}

	// First: context
	if pairs[0].left == nil || pairs[0].left.Kind != diffContext {
		t.Error("pair 0: expected context on left")
	}

	// Pairs 1-2: removed/added paired
	if pairs[1].left == nil || pairs[1].left.Content != "old1" {
		t.Error("pair 1: expected old1 on left")
	}
	if pairs[1].right == nil || pairs[1].right.Content != "new1" {
		t.Error("pair 1: expected new1 on right")
	}
	if pairs[2].left == nil || pairs[2].left.Content != "old2" {
		t.Error("pair 2: expected old2 on left")
	}
	if pairs[2].right == nil || pairs[2].right.Content != "new2" {
		t.Error("pair 2: expected new2 on right")
	}

	// Pair 3: right-only added
	if pairs[3].left != nil {
		t.Error("pair 3: expected nil left")
	}
	if pairs[3].right == nil || pairs[3].right.Content != "new3" {
		t.Error("pair 3: expected new3 on right")
	}

	// Pair 4: context
	if pairs[4].left == nil || pairs[4].left.Kind != diffContext {
		t.Error("pair 4: expected context")
	}
}

func TestRenderEditDiffWidth(t *testing.T) {
	old := `fmt.Println("hello")`
	new := `fmt.Println("hello world")`

	// Render at a controlled width (no tabs to avoid tab-stop ambiguity).
	result := renderEditDiff(0, "test.go", old, new, 80)

	// Each line should be no wider than 80 visible characters.
	for i, line := range splitLines(result) {
		if line == "" {
			continue
		}
		w := ansi.StringWidth(line)
		if w > 80 {
			t.Errorf("line %d: visible width %d > 80: %q", i, w, line)
		}
	}
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return split(s, '\n')
}

func split(s string, sep byte) []string {
	var parts []string
	for {
		i := indexByte(s, sep)
		if i < 0 {
			parts = append(parts, s)
			break
		}
		parts = append(parts, s[:i])
		s = s[i+1:]
	}
	return parts
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
