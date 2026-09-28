package idtui

import (
	"bytes"
	"html"
	"strings"

	glamouransi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/muesli/termenv"
	"github.com/yuin/goldmark"
	emoji "github.com/yuin/goldmark-emoji"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	mdrenderer "github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// renderMarkdown keeps Glamour's parser and ANSI renderer, replacing only the
// table layout. Glamour's table style cannot express separate header/body rules
// or an open grid with gutters instead of vertical borders.
func renderMarkdown(content string, width int, style glamouransi.StyleConfig, ruleColor termenv.Color) (string, error) {
	if width <= 0 {
		width = 80
	}
	opts := glamouransi.Options{
		WordWrap: width, Styles: style, ColorProfile: termenv.ANSI,
		ChromaFormatter: "terminal16", PreserveNewLines: true,
	}
	r := &markdownTableRenderer{base: glamouransi.NewRenderer(opts), options: opts, width: width, ruleColor: ruleColor}
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.DefinitionList, emoji.New()),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	// Extensions register HTML renderers too; replace them after extension setup,
	// just as glamour.NewTermRenderer does.
	md.SetRenderer(mdrenderer.NewRenderer(mdrenderer.WithNodeRenderers(util.Prioritized(r, 1000))))
	var out bytes.Buffer
	err := md.Convert([]byte(content), &out)
	return out.String(), err
}

// trimMarkdownPadding removes Glamour's surrounding blank lines, but preserves
// the first content line's indentation (notably the padding of a table cell).
// It also drops the padding Glamour adds to fill every line out to the wrap
// width: that padding sits inside SGR sequences, where plain whitespace
// trimming cannot reach it, and would otherwise leak into views as trailing
// spaces (plain ones, once an Ascii profile strips the sequences).
func trimMarkdownPadding(rendered string) string {
	lines := strings.Split(rendered, "\n")
	blank := func(line string) bool { return strings.TrimSpace(ansi.Strip(line)) == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}
	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		lines[i] = trimTrailingSpaceANSI(line)
	}
	return strings.Join(lines, "\n")
}

// trimTrailingSpaceANSI removes trailing whitespace from a line while keeping
// every escape sequence that follows the last visible character, so styling
// is still reset as before.
func trimTrailingSpaceANSI(line string) string {
	type token struct {
		seq     string
		escape  bool
		visible bool
	}
	var tokens []token
	last := -1
	var state byte
	for rest := line; len(rest) > 0; {
		seq, width, n, next := ansi.DecodeSequence(rest, state, nil)
		if n <= 0 {
			// Defensive: never loop forever on undecodable input.
			return line
		}
		tok := token{seq: seq, escape: width == 0 && strings.HasPrefix(seq, "\x1b")}
		tok.visible = !tok.escape && strings.TrimSpace(seq) != ""
		if tok.visible {
			last = len(tokens)
		}
		tokens = append(tokens, tok)
		rest, state = rest[n:], next
	}
	var b strings.Builder
	for i, tok := range tokens {
		if i <= last || tok.escape {
			b.WriteString(tok.seq)
		}
	}
	return b.String()
}

type markdownTableRenderer struct {
	base      *glamouransi.ANSIRenderer
	options   glamouransi.Options
	width     int
	text      mdrenderer.NodeRendererFunc
	ruleColor termenv.Color
}

// Register intercepts Glamour's callbacks without taking over its block buffers.
// Tracking the same block styles also accounts for quote/list indentation when
// allocating table columns.
func (r *markdownTableRenderer) RegisterFuncs(reg mdrenderer.NodeRendererFuncRegisterer) {
	r.base.RegisterFuncs(markdownNodeRegisterer(func(kind ast.NodeKind, fn mdrenderer.NodeRendererFunc) {
		if kind == ast.KindText {
			r.text = fn
		}
		if kind == extast.KindTable {
			reg.Register(kind, r.renderTable)
			return
		}
		switch kind {
		case ast.KindDocument, ast.KindBlockquote, ast.KindList, extast.KindDefinitionList:
			// These are the nodes for which Glamour creates a BlockElement.
		default:
			reg.Register(kind, fn)
			return
		}
		var reservations []int
		reg.Register(kind, func(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
			if entering {
				block := r.base.NewElement(node, source).Renderer.(*glamouransi.BlockElement)
				var reserved int
				if block.Style.Indent != nil {
					reserved += int(*block.Style.Indent)
				}
				if block.Style.Margin != nil {
					reserved += 2 * int(*block.Style.Margin)
				}
				reservations = append(reservations, reserved)
				r.width -= reserved
			} else {
				r.width += reservations[len(reservations)-1]
				reservations = reservations[:len(reservations)-1]
			}
			return fn(w, source, node, entering)
		})
	}))
}

type markdownNodeRegisterer func(ast.NodeKind, mdrenderer.NodeRendererFunc)

func (f markdownNodeRegisterer) Register(kind ast.NodeKind, fn mdrenderer.NodeRendererFunc) {
	f(kind, fn)
}

func (r *markdownTableRenderer) renderTable(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	table := node.(*extast.Table)
	var rows [][]string
	for row := table.FirstChild(); row != nil; row = row.NextSibling() {
		cells := make([]string, len(table.Alignments))
		col := 0
		for cell := row.FirstChild(); cell != nil && col < len(cells); cell = cell.NextSibling() {
			rendered, err := r.renderCell(source, cell, len(rows) == 0)
			if err != nil {
				return ast.WalkStop, err
			}
			cells[col] = rendered
			col++
		}
		rows = append(rows, cells)
	}
	value := "\n" + layoutMarkdownTable(rows, table.Alignments, max(1, r.width), r.ruleColor) + "\n"
	// Feed the finished table into Glamour's current block so nested containers
	// and document backgrounds still apply. Protect already-rendered literals
	// from Text's entity decoding and Markdown backslash unescaping.
	value = html.EscapeString(strings.ReplaceAll(value, `\`, `\\`))
	literal := ast.NewTextSegment(text.NewSegment(0, len(value)))
	_, err := r.text(w, []byte(value), literal, true)
	return ast.WalkSkipChildren, err
}

func (r *markdownTableRenderer) renderCell(source []byte, cell ast.Node, header bool) (string, error) {
	// Render the parsed inline nodes, not their source text: reparsing would lose
	// reference links and misinterpret escaped pipes or inline code as Markdown.
	doc := ast.NewDocument()
	paragraph := ast.NewParagraph()
	doc.AppendChild(doc, paragraph)
	for cell.FirstChild() != nil {
		paragraph.AppendChild(paragraph, cell.FirstChild())
	}
	defer func() {
		for paragraph.FirstChild() != nil {
			cell.AppendChild(cell, paragraph.FirstChild())
		}
	}()
	opts := r.options
	opts.WordWrap = 0
	opts.Styles.Document = glamouransi.StyleBlock{}
	if header {
		bold := true
		// Use the terminal's cyan accent, like other Dagger titles, rather than
		// a fixed RGB color that may clash with the user's palette.
		accent := "6"
		opts.Styles.Document.Bold = &bold
		opts.Styles.Document.Color = &accent
	}
	inline := mdrenderer.NewRenderer(mdrenderer.WithNodeRenderers(util.Prioritized(glamouransi.NewRenderer(opts), 1000)))
	var out bytes.Buffer
	if err := inline.Render(&out, source, doc); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// Codex's open table style: one space of cell padding, two-space gutters,
// a heavy header rule and dim, light rules between body rows. No outer border.
func layoutMarkdownTable(rows [][]string, alignments []extast.Alignment, width int, ruleColor termenv.Color) string {
	if len(rows) == 0 || len(alignments) == 0 {
		return ""
	}
	widths := make([]int, len(alignments))
	minimums := make([]int, len(alignments))
	for _, row := range rows {
		for col, cell := range row {
			for _, line := range strings.Split(cell, "\n") {
				widths[col] = max(widths[col], ansi.StringWidth(line), 1)
			}
			// Never squeeze a wide grapheme into a one-cell column.
			var state byte
			for len(cell) > 0 {
				_, size, n, next := ansi.DecodeSequence(cell, state, nil)
				minimums[col] = max(minimums[col], size, 1)
				cell, state = cell[n:], next
			}
		}
	}
	minimum := 0
	for col := range minimums {
		minimums[col] = max(1, minimums[col])
		minimum += minimums[col]
	}
	budget := width - 2*len(widths) - 2*(len(widths)-1)
	if budget < minimum {
		return markdownTableRecords(rows, width, ruleColor)
	}
	// Keep short columns compact; shrink the widest columns first, wrapping
	// their contents rather than truncating values.
	for {
		total, widest := 0, -1
		for col, size := range widths {
			total += size
			if size > minimums[col] && (widest == -1 || size > widths[widest]) {
				widest = col
			}
		}
		if total <= budget || widest == -1 {
			break
		}
		widths[widest]--
	}
	var lines []string
	rule := func(char string) string {
		parts := make([]string, len(widths))
		for col, size := range widths {
			parts[col] = strings.Repeat(char, size+2)
		}
		return styleMarkdownTableRule(strings.Join(parts, "  "), ruleColor)
	}
	for idx, row := range rows {
		wrapped := make([][]string, len(widths))
		height := 1
		for col, cell := range row {
			wrapped[col] = strings.Split(cellbuf.Wrap(cell, widths[col], ""), "\n")
			height = max(height, len(wrapped[col]))
		}
		for line := 0; line < height; line++ {
			parts := make([]string, len(widths))
			for col, size := range widths {
				var value string
				if line < len(wrapped[col]) {
					value = wrapped[col][line]
				}
				space := max(0, size-ansi.StringWidth(value))
				left := 0
				switch alignments[col] {
				case extast.AlignRight:
					left = space
				case extast.AlignCenter:
					left = space / 2
				}
				parts[col] = " " + strings.Repeat(" ", left) + value + strings.Repeat(" ", space-left) + " "
			}
			lines = append(lines, strings.TrimRight(strings.Join(parts, "  "), " "))
		}
		if idx == 0 {
			lines = append(lines, rule("━"))
		} else if idx < len(rows)-1 {
			lines = append(lines, rule("─"))
		}
	}
	return strings.Join(lines, "\n")
}

// styleMarkdownTableRule uses the negotiated blend without also applying SGR
// faint: the blend itself supplies the dimming. Faint remains the fallback.
func styleMarkdownTableRule(text string, color termenv.Color) string {
	style := termenv.String(text)
	if color != nil {
		return style.Foreground(color).String()
	}
	return style.Faint().String()
}

// At widths too small for even one character per column, keep every value
// readable by stacking labeled records instead of drawing a broken grid.
func markdownTableRecords(rows [][]string, width int, ruleColor termenv.Color) string {
	var lines []string
	if len(rows) == 1 {
		for _, header := range rows[0] {
			lines = append(lines, cellbuf.Wrap(header, width, ""))
		}
	}
	for idx, row := range rows[1:] {
		if idx > 0 {
			lines = append(lines, styleMarkdownTableRule(strings.Repeat("─", width), ruleColor))
		}
		for col, value := range row {
			lines = append(lines, cellbuf.Wrap(rows[0][col]+": "+value, width, ""))
		}
	}
	return strings.Join(lines, "\n")
}
