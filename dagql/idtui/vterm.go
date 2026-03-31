package idtui

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode"

	"charm.land/lipgloss/v2"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/muesli/termenv"
	"github.com/vito/midterm"
)

type Vterm struct {
	Offset int
	Height int
	Width  int

	Prefix string

	Profile termenv.Profile

	vt *midterm.Terminal

	// Separate buffer for Markdown content
	markdownBuf *bytes.Buffer
	// Regular terminal buffer
	viewBuf     *bytes.Buffer
	needsRedraw bool

	// Thinking mode: when true, rendered Markdown is styled dim+italic
	// to visually distinguish LLM thinking/reasoning output.
	Thinking bool

	// Search highlight state. When SearchQuery is non-empty, matching
	// substrings in rendered lines are highlighted. SearchCurrentRow
	// is the vterm row index of the "current" match (-1 for none),
	// which gets a brighter highlight.
	SearchQuery      string
	SearchCurrentRow int

	mu *sync.Mutex
}

func NewVterm(profile termenv.Profile) *Vterm {
	return &Vterm{
		Profile:     profile,
		vt:          midterm.NewAutoResizingTerminal(),
		viewBuf:     new(bytes.Buffer),
		markdownBuf: new(bytes.Buffer),
		mu:          new(sync.Mutex),
	}
}

func (term *Vterm) Term() *midterm.Terminal {
	return term.vt
}

func (term *Vterm) WriteMarkdown(p []byte) (int, error) {
	term.mu.Lock()
	defer term.mu.Unlock()

	n, err := term.markdownBuf.Write(p)
	if err != nil {
		return n, err
	}

	term.needsRedraw = true
	return n, nil
}

func (term *Vterm) Write(p []byte) (int, error) {
	term.mu.Lock()
	defer term.mu.Unlock()

	atBottom := term.Offset+term.Height >= term.vt.UsedHeight()
	if term.Height == 0 {
		atBottom = true
	}

	n, err := term.vt.Write(p)
	if err != nil {
		return n, err
	}

	if atBottom {
		term.Offset = max(0, term.vt.UsedHeight()-term.Height)
	}

	term.needsRedraw = true

	return n, nil
}

func (term *Vterm) UsedHeight() int {
	return term.vt.UsedHeight()
}

func (term *Vterm) SetHeight(height int) {
	term.mu.Lock()
	defer term.mu.Unlock()
	if height == term.Height {
		return
	}
	atBottom := term.Offset+term.Height >= term.vt.UsedHeight()
	term.Height = height
	if atBottom {
		term.Offset = max(0, term.vt.UsedHeight()-term.Height)
	}
	term.needsRedraw = true
}

func (term *Vterm) SetWidth(width int) {
	term.mu.Lock()
	defer term.mu.Unlock()
	if width == term.Width {
		return
	}
	term.Width = width
	prefixWidth := lipgloss.Width(term.Prefix)
	if width > prefixWidth {
		term.vt.ResizeX(width - prefixWidth)
	}
	term.needsRedraw = true
}

func (term *Vterm) SetPrefix(prefix string) {
	term.mu.Lock()
	defer term.mu.Unlock()
	if prefix == term.Prefix {
		return
	}
	term.Prefix = prefix
	prefixWidth := lipgloss.Width(prefix)
	if term.Width > prefixWidth && !term.vt.AutoResizeX {
		term.vt.ResizeX(term.Width - prefixWidth)
	}
	term.needsRedraw = true
}

func (term *Vterm) Init() tea.Cmd {
	return nil
}

func (term *Vterm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) { //nolint:gocritic
	case tea.KeyMsg:
		_ = msg
		switch {
		// case key.Matches(msg, Keys.Up):
		// 	term.Offset = max(0, term.Offset-1)
		// case key.Matches(msg, Keys.Down):
		// 	term.Offset = min(term.vt.UsedHeight()-term.Height, term.Offset+1)
		// case key.Matches(msg, Keys.PageUp):
		// 	term.Offset = max(0, term.Offset-term.Height)
		// case key.Matches(msg, Keys.PageDown):
		// 	term.Offset = min(term.vt.UsedHeight()-term.Height, term.Offset+term.Height)
		// case key.Matches(msg, Keys.Home):
		// 	term.Offset = 0
		// case key.Matches(msg, Keys.End):
		// 	term.Offset = term.vt.UsedHeight() - term.Height
		}
	}
	return term, nil
}

// SetSearchHighlight sets the search highlight state using native midterm
// search. Pass an empty query to clear highlights. currentRow is the vterm
// row of the "current" match (-1 if none in this vterm).
//
// Always re-runs the search so that new content is picked up.
func (term *Vterm) SetSearchHighlight(query string, currentRow int) {
	term.mu.Lock()
	defer term.mu.Unlock()

	if query == "" {
		if term.SearchQuery != "" {
			term.vt.SearchClear()
			term.needsRedraw = true
		}
		term.SearchQuery = ""
		term.SearchCurrentRow = -1
		return
	}

	// Always re-run search to pick up new content.
	term.vt.Search(query)

	// Mark the current match by row.
	if currentRow >= 0 {
		term.setCurrentMatchByRow(currentRow)
	} else {
		// Clear any previous current highlight.
		term.vt.SearchSetCurrent(-1)
	}

	if term.SearchQuery != query || term.SearchCurrentRow != currentRow {
		term.needsRedraw = true
	}
	term.SearchQuery = query
	term.SearchCurrentRow = currentRow
}

// setCurrentMatchByRow finds the first search match on the given row and
// marks it as "current" in midterm. Must be called with term.mu held.
func (term *Vterm) setCurrentMatchByRow(row int) {
	for i, m := range term.vt.SearchMatches {
		if m.Row == row {
			term.vt.SearchSetCurrent(i)
			return
		}
	}
}

// ScrollToRow scrolls the viewport so that the given row is centered
// (or as close as possible) within the visible area.
func (term *Vterm) ScrollToRow(row int) {
	term.mu.Lock()
	defer term.mu.Unlock()
	// Center the target row in the viewport.
	term.Offset = max(0, row-term.Height/2)
	// Clamp to valid range.
	maxOffset := max(0, term.vt.UsedHeight()-term.Height)
	if term.Offset > maxOffset {
		term.Offset = maxOffset
	}
	term.needsRedraw = true
}

func (term *Vterm) ScrollPercent() float64 {
	return min(1, float64(term.Offset+term.Height)/float64(term.vt.UsedHeight()))
}

const reset = termenv.CSI + termenv.ResetSeq + "m"

// View returns the output for the current region of the terminal, with ANSI
// formatting or rendered Markdown if present.
func (term *Vterm) View() string {
	term.mu.Lock()
	defer term.mu.Unlock()
	if term.needsRedraw {
		term.redraw()
		term.needsRedraw = false
	}
	return term.viewBuf.String()
}

var MarkdownStyle = styles.LightStyleConfig

// ThinkingMarkdownStyle is used for LLM thinking/reasoning output.
// All text is rendered in a dim gray (ANSIBrightBlack equivalent)
// and italic to visually distinguish it from normal assistant output.
var ThinkingMarkdownStyle ansi.StyleConfig

func init() {
	if HasDarkBackground() {
		MarkdownStyle = styles.DarkStyleConfig
	}

	t := true
	noMargin := uint(0)

	// We don't need any extra margin.
	MarkdownStyle.Document.Margin = &noMargin

	// No real point setting a custom foreground, it just looks weird.
	MarkdownStyle.Document.Color = nil

	// Tone down headings: use bold with ANSI colors, no backgrounds.
	h1Color := "15" // bright white
	MarkdownStyle.H1 = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Bold:   &t,
			Color:  &h1Color,
			Prefix: "# ",
		},
	}
	h2Color := "15"
	MarkdownStyle.H2 = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Bold:   &t,
			Color:  &h2Color,
			Prefix: "## ",
		},
	}

	// Inline code: subtle, no red, no background, no padding.
	codeColor := "14" // bright cyan
	MarkdownStyle.Code = ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Color: &codeColor,
		},
	}

	// Code blocks: no chroma, no margin, just dim text.
	codeBlockColor := "7" // white (normal)
	MarkdownStyle.CodeBlock = ansi.StyleCodeBlock{
		StyleBlock: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: &codeBlockColor,
			},
			Margin: &noMargin,
		},
		Chroma: &ansi.Chroma{},
	}

	// Links: use ANSI blue.
	linkColor := "4" // blue
	MarkdownStyle.Link = ansi.StylePrimitive{
		Color:     &linkColor,
		Underline: &t,
	}

	// Build thinking style: clone the base style and set all text to
	// a dim gray + italic. We use ANSI 90 which is "bright black" —
	// the same color as termenv.ANSIBrightBlack.
	ThinkingMarkdownStyle = MarkdownStyle
	dimColor := "8" // ANSI color index 8 = termenv.ANSIBrightBlack
	ThinkingMarkdownStyle.Document.StylePrimitive.Color = &dimColor
	ThinkingMarkdownStyle.Document.StylePrimitive.Italic = &t
	ThinkingMarkdownStyle.Paragraph.Color = &dimColor
	ThinkingMarkdownStyle.Paragraph.Italic = &t
}

func (term *Vterm) redraw() {
	term.viewBuf.Reset()

	// First render any Markdown content
	if term.markdownBuf.Len() > 0 {
		style := MarkdownStyle
		if term.Thinking {
			style = ThinkingMarkdownStyle
		}

		renderer, _ := glamour.NewTermRenderer(
			glamour.WithWordWrap(term.Width-lipgloss.Width(term.Prefix)),
			glamour.WithStyles(style),
			glamour.WithPreservedNewLines(),
			glamour.WithEmoji(),
		)

		rendered, err := renderer.Render(term.markdownBuf.String())
		if err != nil {
			fmt.Fprintf(term.viewBuf, "Error rendering Markdown: %s\n", err)
		} else {
			// Remove leading and trailing newlines
			rendered = strings.TrimSpace(rendered)
			// Add prefix to each line of rendered Markdown
			lines := strings.Split(rendered, "\n")
			for i, line := range lines {
				if i > 0 {
					fmt.Fprint(term.viewBuf, term.Prefix)
				}
				fmt.Fprintln(term.viewBuf, line)
			}
		}
	}

	// Then render regular terminal content
	term.Render(term.viewBuf, term.Offset, term.Height)
}

type Markdown struct {
	Content    string
	Background termenv.Color
	Prefix     string
	Width      int

	viewBuf     strings.Builder
	needsRedraw bool
}

func (m *Markdown) View() string {
	if !m.needsRedraw && m.viewBuf.Len() > 0 {
		return m.viewBuf.String()
	}
	m.viewBuf.Reset()
	st := MarkdownStyle
	// HACK: we want "0" or "255", but termenv.Color doesn't have a
	// String() method, only Sequence(bool) which prints the ANSI
	// formatting sequence.
	if m.Background != nil {
		switch x := m.Background.(type) {
		case termenv.ANSIColor, termenv.ANSI256Color:
			// annoyingly, there's no clean conversion from termenv.Color
			// back to the value that lipgloss wants, because ANSI 0
			// translates to "#000000" and we want "0"
			bg := fmt.Sprintf("%d", x)
			st.Document.BackgroundColor = &bg
		default:
			bg := fmt.Sprint(m.Background)
			st.Document.BackgroundColor = &bg
		}
	}
	glamourOpts := []glamour.TermRendererOption{
		glamour.WithStyles(st),
		glamour.WithPreservedNewLines(),
		glamour.WithEmoji(),
	}
	if m.Width != 0 {
		glamourOpts = append(glamourOpts,
			glamour.WithWordWrap(m.Width-lipgloss.Width(m.Prefix)))
	}
	renderer, err := glamour.NewTermRenderer(glamourOpts...)
	if err != nil {
		return fmt.Sprintf("Error rendering Markdown: %s\n", err)
	}

	rendered, err := renderer.Render(m.Content)
	if err != nil {
		return fmt.Sprintf("Error rendering Markdown: %s\n", err)
	} else if m.Prefix != "" {
		// Remove leading and trailing newlines
		rendered = strings.TrimSpace(rendered)
		// Add prefix to each line of rendered Markdown
		lines := strings.Split(rendered, "\n")
		for i, line := range lines {
			if i > 0 {
				m.viewBuf.WriteString(m.Prefix)
			}
			m.viewBuf.WriteString(line)
			m.viewBuf.WriteString("\n")
		}
	} else {
		m.viewBuf.WriteString(rendered)
	}
	return m.viewBuf.String()
}

// Render writes the output for the given region of the terminal, with
// ANSI formatting. Search highlights are rendered natively by midterm.
func (term *Vterm) Render(w io.Writer, offset, height int) {
	used := term.vt.UsedHeight()
	if used == 0 {
		return
	}

	vt := NewOutput(w, termenv.WithProfile(term.Profile))
	w = vt

	var lines int
	for row := range term.vt.Content {
		if row < offset {
			continue
		}
		if row+1 > (offset + height) {
			break
		}

		fmt.Fprint(w, vt.String(term.Prefix))
		term.vt.RenderLineFgBg(w, row, nil, nil)
		fmt.Fprintln(w)
		lines++

		if row > used {
			break
		}
	}
}

// LastLine returns the last line of visible text, with ANSI formatting, but
// without any trailing whitespace.
func (term *Vterm) LastLine() string {
	used := term.vt.UsedHeight()
	if used == 0 {
		return ""
	}
	var lastLine string
	for row := used - 1; row >= 0; row-- {
		buf := new(strings.Builder)
		_ = term.vt.RenderLine(buf, row)
		if strings.TrimSpace(buf.String()) == "" {
			continue
		}
		lastLine = strings.TrimRightFunc(buf.String(), unicode.IsSpace)
		break
	}
	return lastLine + reset
}

// Print prints the full log output without any formatting.
func (term *Vterm) Print(w io.Writer) error {
	used := term.vt.UsedHeight()

	for row, l := range term.vt.Content {
		_, err := fmt.Fprintln(w, strings.TrimRight(string(l), " "))
		if err != nil {
			return err
		}

		if row > used {
			break
		}
	}

	return nil
}
