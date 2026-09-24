package idtui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

const compactMarkdownTable = "| Item | N |\n| :--- | --: |\n| A | 1 |\n| B | 20 |\n"

func plainMarkdown(s string) string {
	lines := strings.Split(ansi.Strip(s), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

func TestMarkdownTableCompact(t *testing.T) {
	want := " Item     N\n━━━━━━  ━━━━\n A        1\n──────  ────\n B       20"
	for _, width := range []int{20, 80, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			got, err := renderMarkdown(compactMarkdownTable, width, MarkdownStyle)
			require.NoError(t, err)
			require.Equal(t, want, plainMarkdown(got))
			require.Contains(t, got, "\x1b[36;1m", "headers should be bold cyan")
			require.Contains(t, got, "\x1b[2m", "rules should be dim")
		})
	}
}

func TestMarkdownTableAccentDoesNotBleed(t *testing.T) {
	for _, width := range []int{11, 80} {
		got, err := renderMarkdown("| TITLE | N |\n| --- | --- |\n| value | 1 |\n| other | 2 |\n", width, MarkdownStyle)
		require.NoError(t, err)
		buf := cellbuf.NewBuffer(width, strings.Count(got, "\n")+1)
		cellbuf.SetContent(buf, got)
		for y := 0; y < buf.Height(); y++ {
			for x := 0; x < buf.Width(); x++ {
				c := buf.Cell(x, y)
				if c == nil {
					continue
				}
				switch {
				case strings.ContainsAny(c.String(), "TILEN"):
					require.NotNil(t, c.Style.Fg, "wrapped headers retain their accent")
					require.True(t, c.Style.Attrs.Contains(cellbuf.BoldAttr))
				case strings.ContainsAny(c.String(), "valueothr12━─"):
					require.Nil(t, c.Style.Fg, "body and rules use default foreground")
				}
			}
		}
	}
}

func TestMarkdownTableWrapping(t *testing.T) {
	content := "| Name | N |\n| --- | ---: |\n| a long description with words | 12 |\n| abcdefghijklmnopqrstuvwxyz | 3 |\n"
	for _, width := range []int{12, 20, 40} {
		got, err := renderMarkdown(content, width, MarkdownStyle)
		require.NoError(t, err)
		for _, line := range strings.Split(ansi.Strip(got), "\n") {
			require.LessOrEqual(t, ansi.StringWidth(line), width, "%q", line)
		}
		require.Contains(t, ansi.Strip(got), "12")
		require.NotContains(t, ansi.Strip(got), "…")
	}
}

func TestMarkdownTableInlineContent(t *testing.T) {
	content := "| Feature | Value |\n| --- | --- |\n| **bold** and *italic* | `x\\y` |\n| a\\|b &amp; &amp;amp; | [docs][ref] |\n| 界🙂 | :smile: |\n\n[ref]: https://example.com\n"
	got, err := renderMarkdown(content, 100, MarkdownStyle)
	require.NoError(t, err)
	plain := ansi.Strip(got)
	for _, value := range []string{"bold", "italic", `x\y`, "a|b & &amp;", "docs", "https://example.com", "界🙂", "😄"} {
		require.Contains(t, plain, value)
	}
	require.Contains(t, got, "\x1b[3m")
	lines := strings.Split(plainMarkdown(got), "\n")
	for _, line := range lines {
		if strings.Contains(line, "━") || strings.Contains(line, "─") {
			require.Equal(t, ansi.StringWidth(lines[1]), ansi.StringWidth(line))
		}
	}
}

func TestMarkdownTableNarrowRecords(t *testing.T) {
	got, err := renderMarkdown(compactMarkdownTable, 7, MarkdownStyle)
	require.NoError(t, err)
	plain := ansi.Strip(got)
	require.Contains(t, plain, "Item: A")
	require.Contains(t, plain, "N: 20")
	for _, line := range strings.Split(plain, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), 7)
	}
}

func TestMarkdownTableWideGraphemes(t *testing.T) {
	for _, width := range []int{7, 8, 9, 12, 20} {
		got, err := renderMarkdown("| Name | N |\n| --- | --- |\n| 界🙂界 | 1 |\n", width, MarkdownStyle)
		require.NoError(t, err)
		require.Contains(t, got, "🙂")
		for _, line := range strings.Split(got, "\n") {
			require.LessOrEqual(t, ansi.StringWidth(line), width, "%q", line)
		}
	}
}

func TestMarkdownTableNested(t *testing.T) {
	for name, content := range map[string]string{
		"quote":       "> " + strings.ReplaceAll(strings.TrimSpace(compactMarkdownTable), "\n", "\n> "),
		"list":        "- table\n\n  " + strings.ReplaceAll(strings.TrimSpace(compactMarkdownTable), "\n", "\n  "),
		"multiple":    compactMarkdownTable + "\nBetween\n\n" + compactMarkdownTable,
		"header only": "| Item | N |\n| --- | --- |\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := renderMarkdown(content, 20, MarkdownStyle)
			require.NoError(t, err)
			require.Contains(t, got, "━")
			for _, line := range strings.Split(ansi.Strip(got), "\n") {
				require.LessOrEqual(t, ansi.StringWidth(line), 20, "%q", line)
			}
		})
	}
}

func TestMarkdownNonTablesUnchanged(t *testing.T) {
	content := "# Heading\n\nSome **bold** and *italic*, `code`, [link](https://example.com), :smile:.\n\n> a quote\n>\n> - nested\n\n- [x] done\n- list\n\n```text\n| not | a table |\n| --- | --- |\n```\n\n---\n"
	for _, style := range []string{styles.LightStyle, styles.DarkStyle} {
		t.Run(style, func(t *testing.T) {
			st := styles.LightStyleConfig
			if style == styles.DarkStyle {
				st = styles.DarkStyleConfig
			}
			old, err := glamour.NewTermRenderer(glamour.WithStyles(st), glamour.WithWordWrap(50), glamour.WithColorProfile(termenv.ANSI), glamour.WithChromaFormatter("terminal16"), glamour.WithPreservedNewLines(), glamour.WithEmoji())
			require.NoError(t, err)
			want, err := old.Render(content)
			require.NoError(t, err)
			got, err := renderMarkdown(content, 50, st)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

func TestMarkdownTableWrappedStyles(t *testing.T) {
	got, err := renderMarkdown("| Name | N |\n| --- | --- |\n| **abcdefgh** | 1 |\n", 11, MarkdownStyle)
	require.NoError(t, err)
	lines := strings.Split(plainMarkdown(got), "\n")
	buf := cellbuf.NewBuffer(11, strings.Count(got, "\n")+1)
	cellbuf.SetContent(buf, got)
	// Every wrapped fragment of bold text retains its style, but neither the
	// adjacent numeric cell nor the gutters inherit it.
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Cell(x, y)
			if c == nil {
				continue
			}
			if strings.ContainsAny(c.String(), "abcdefgh") {
				require.True(t, c.Style.Attrs.Contains(cellbuf.BoldAttr), "%q", c.String())
			}
			if c.String() == "1" {
				require.False(t, c.Style.Attrs.Contains(cellbuf.BoldAttr))
			}
		}
	}
	require.GreaterOrEqual(t, len(lines), 4)
}

func TestMarkdownTableStreamingAndAlignment(t *testing.T) {
	content := "| Left | Center | Right |\n| :--- | :---: | ---: |\n| 界 | x | 1 |\n| | yy | 20 |\n"
	for i := 0; i <= len(content); i++ {
		_, err := renderMarkdown(content[:i], 40, MarkdownStyle)
		require.NoError(t, err, "stream prefix %d", i)
	}
	got, err := renderMarkdown(content, 40, MarkdownStyle)
	require.NoError(t, err)
	require.Equal(t, " Left    Center    Right\n━━━━━━  ━━━━━━━━  ━━━━━━━\n 界        x           1\n──────  ────────  ───────\n           yy         20", plainMarkdown(got))
}

func TestMarkdownTableViews(t *testing.T) {
	m := &Markdown{Content: compactMarkdownTable, Width: 80, Prefix: "  "}
	require.Contains(t, m.View(), "━━━━━━  ━━━━")
	require.True(t, strings.HasPrefix(ansi.Strip(m.View()), " Item     N"))
	term := NewVterm(termenv.Ascii)
	term.SetWidth(80)
	_, err := term.WriteMarkdown([]byte(compactMarkdownTable))
	require.NoError(t, err)
	require.Contains(t, term.View(), "━━━━━━  ━━━━")
	require.NotContains(t, term.View(), "\x1b")
	term.WriteMedia(dagui.MediaRecord{Kind: "image"}, "[image]")
	require.Contains(t, mediaTestView(term), "━━━━━━  ━━━━")
}
