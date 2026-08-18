package idtui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// TestMarkdownRenderDemo is a scratch demonstration (not an assertion) that
// renders a variety of Markdown constructs through the exact path the agent
// uses -- Vterm.WriteMarkdown -> glamour -> prefixed lines -- so quirks in
// wrapping, indentation, and block elements are visible. Each construct is
// rendered at two indents: a top-level 2-space gutter and a nested-sub-agent
// 8-space one. Lines that overflow the width are flagged ">>>OVER".
func TestMarkdownRenderDemo(t *testing.T) {
	const width = 72

	show := func(name, prefix, md string) {
		vt := NewVterm(termenv.Ascii)
		vt.SetPrefix(prefix)
		vt.SetWidth(width)
		_, _ = vt.WriteMarkdown([]byte(md))
		rendered := prefix + vt.View() // first line renders inline after the gutter

		var b strings.Builder
		fmt.Fprintf(&b, "\n=== %s (prefix=%d) ===\n", name, len(prefix))
		fmt.Fprintf(&b, "%s>| (width=%d)\n", strings.Repeat("-", width-2), width)
		for _, line := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
			plain := strings.TrimRight(ansi.Strip(line), " ")
			w := len([]rune(plain))
			flag := ""
			if w > width {
				flag = fmt.Sprintf(" >>>OVER by %d", w-width)
			}
			fmt.Fprintf(&b, "[%2d]%s |%s|\n", w, flag, plain)
		}
		t.Log(b.String())
	}

	both := func(name, md string) {
		show(name, "  ", md)       // top-level agent gutter
		show(name, "        ", md) // ~3 levels of sub-agent nesting
	}

	both("nested unordered list", `- top item that is long enough to wrap onto a continuation line under the bullet marker
  - nested once, also long enough to wrap under the deeper bullet indentation here
    - nested twice, again long enough to wrap and show how the indent eats the width
      - nested thrice, long enough to wrap once more near the right edge of the pane`)

	both("fenced code block", "```go\nfunc main() {\n\tfmt.Println(\"a single line of code that is deliberately long enough to exceed the wrap width for demonstration\")\n}\n```")

	both("blockquote", `> A blockquote paragraph that is long enough to wrap onto multiple lines so we can see how the quote marker and prefix interact with the wrap width.`)

	both("table", `| Column A | Column B | Column C |
| --- | --- | --- |
| short | a longer cell value that pushes the table wider | ok |`)

	both("plain paragraph (control)", `A plain paragraph with no block-level indentation, long enough to wrap onto several lines, as a control to compare against the indented block elements above.`)

	// The renderer is built WithPreservedNewLines(): single newlines inside a
	// paragraph are kept instead of being reflowed the way CommonMark does.
	both("source-hardwrapped paragraph", "This paragraph was hard-wrapped in the\nsource with single newlines after\n\"the\" and \"after\", so watch whether it\nreflows to the pane width or keeps the\nsource line breaks.")

	both("code block inside a list", "1. Run this:\n\n   ```sh\n   dagger call test --a-fairly-long-flag=value --another=one-more-to-exceed-width\n   ```\n\n2. Then check the output.")
}
