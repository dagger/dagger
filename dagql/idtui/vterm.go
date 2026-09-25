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
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/x/ansi"
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

	// Media uses ordered blocks; text-only terminals retain their legacy path.
	images      *kittyImages
	segments    []vtermSegment
	mediaRows   []vtermMediaRow
	mediaActive bool
	mediaFollow bool

	// Separate buffer for Markdown content
	markdownBuf *logBuffer
	// The byte stream is authoritative; midterm is a disposable logical cache.
	terminalBuf *logBuffer
	cache       *terminalCache
	follow      bool
	storageErr  error
	textRows    []vtermTextRow
	textHeight  int
	layoutDirty bool
	cacheCells  int
	// Regular terminal buffer
	viewBuf     *bytes.Buffer
	rawBuf      *logBuffer
	needsRedraw bool

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
		viewBuf:     new(bytes.Buffer),
		rawBuf:      new(logBuffer),
		markdownBuf: new(logBuffer),
		terminalBuf: new(logBuffer),
		follow:      true,
		mu:          new(sync.Mutex),
	}
}

func (term *Vterm) Term() *midterm.Terminal {
	term.mu.Lock()
	defer term.mu.Unlock()
	term.materializeLocked()
	return term.vt
}

// SearchMatchRows returns a snapshot, safe even if another display evicts the
// terminal. Search is over the complete log, never a truncated preview.
func (term *Vterm) SearchMatchRows() []int {
	term.mu.Lock()
	defer term.mu.Unlock()
	term.materializeLocked()
	var rows []int
	for _, match := range term.vt.SearchMatches {
		row := term.matchRow(match)
		if len(rows) == 0 || rows[len(rows)-1] != row {
			rows = append(rows, row)
		}
	}
	return rows
}

func (term *Vterm) WriteMarkdown(p []byte) (int, error) {
	term.mu.Lock()
	defer term.mu.Unlock()

	if term.segments != nil {
		return term.writeMediaText(p, true, false)
	}
	n, err := term.markdownBuf.Write(p)
	if err != nil {
		return n, err
	}
	if _, err := term.rawBuf.Write(p); err != nil {
		term.storageErr = err
		return 0, err
	}

	term.needsRedraw = true
	return n, nil
}

// WriteDiff syntax-highlights an authoritative unified diff while retaining its
// original bytes for raw output and agent-facing reports.
func (term *Vterm) WriteDiff(p []byte) (int, error) {
	term.mu.Lock()
	defer term.mu.Unlock()

	if term.segments != nil {
		return term.writeMediaText(p, false, true)
	}
	highlighted := highlightDiff(term.Profile, string(p))
	if err := term.writeTerminalLocked([]byte(highlighted)); err != nil {
		return 0, err
	}
	n, err := term.rawBuf.Write(p)
	if err != nil {
		term.storageErr = err
	}
	return n, err
}

func (term *Vterm) Write(p []byte) (int, error) {
	term.mu.Lock()
	defer term.mu.Unlock()

	if term.segments != nil {
		return term.writeMediaText(p, false, false)
	}
	if err := term.writeTerminalLocked(p); err != nil {
		return 0, err
	}
	n, err := term.rawBuf.Write(p)
	if err != nil {
		term.storageErr = err
	}
	return n, err
}

func (term *Vterm) UsedHeight() int {
	term.mu.Lock()
	defer term.mu.Unlock()
	return term.usedHeightLocked()
}

func (term *Vterm) usedHeightLocked() int {
	term.materializeLocked()
	if term.segments != nil {
		return len(term.mediaRows)
	}
	return term.textHeight
}

func (term *Vterm) SetHeight(height int) {
	term.mu.Lock()
	defer term.mu.Unlock()
	if height == term.Height {
		return
	}
	used := term.usedHeightLocked()
	atBottom := term.Offset+term.Height >= used
	term.Height = height
	if atBottom {
		term.Offset = max(0, term.usedHeightLocked()-term.Height)
	}
	term.needsRedraw = true
}

func (term *Vterm) SetWidth(width int) {
	term.mu.Lock()
	defer term.mu.Unlock()
	if width == term.Width {
		return
	}
	term.rememberFollowLocked()
	term.Width = width
	if term.segments != nil {
		term.invalidateMedia()
		return
	}
	term.layoutDirty = true
	term.needsRedraw = true
}

func (term *Vterm) SetPrefix(prefix string) {
	term.mu.Lock()
	defer term.mu.Unlock()
	if prefix == term.Prefix {
		return
	}
	term.rememberFollowLocked()
	term.Prefix = prefix
	if term.segments != nil {
		term.invalidateMedia()
		return
	}
	term.layoutDirty = true
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

	if query == "" && term.vt == nil {
		term.SearchQuery = ""
		term.SearchCurrentRow = -1
		return
	}
	term.materializeLocked()
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
	term.vt.SearchSetCurrent(-1)
	for i, m := range term.vt.SearchMatches {
		if term.matchRow(m) == row {
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
	term.materializeLocked()
	// Center the target row in the viewport.
	term.Offset = max(0, row-term.Height/2)
	term.clampOffsetLocked()
	term.needsRedraw = true
}

func (term *Vterm) ScrollBy(delta int) {
	term.mu.Lock()
	defer term.mu.Unlock()
	term.materializeLocked()
	term.Offset += delta
	term.clampOffsetLocked()
	term.needsRedraw = true
}

func (term *Vterm) ScrollPage(deltaPages int) {
	term.mu.Lock()
	defer term.mu.Unlock()
	term.materializeLocked()
	page := max(term.Height-1, 1)
	term.Offset += deltaPages * page
	term.clampOffsetLocked()
	term.needsRedraw = true
}

func (term *Vterm) ScrollToTop() {
	term.mu.Lock()
	defer term.mu.Unlock()
	term.mediaFollow = false
	term.follow = false
	term.Offset = 0
	term.needsRedraw = true
}

func (term *Vterm) ScrollToBottom() {
	term.mu.Lock()
	defer term.mu.Unlock()
	term.Offset = max(0, term.usedHeightLocked()-term.Height)
	term.needsRedraw = true
}

func (term *Vterm) clampOffsetLocked() {
	if term.Offset < 0 {
		term.Offset = 0
	}
	maxOffset := max(0, term.usedHeightLocked()-term.Height)
	if term.Offset > maxOffset {
		term.Offset = maxOffset
	}
}

func (term *Vterm) Search(query string, currentIdx int) (count, row int) {
	term.mu.Lock()
	defer term.mu.Unlock()

	if query == "" && term.vt == nil {
		term.SearchQuery = ""
		term.SearchCurrentRow = -1
		return 0, -1
	}
	term.materializeLocked()
	if query == "" {
		term.vt.SearchClear()
		term.SearchQuery = ""
		term.SearchCurrentRow = -1
		term.needsRedraw = true
		return 0, -1
	}

	count = term.vt.Search(query)
	if currentIdx < 0 || currentIdx >= count {
		row, _ = term.vt.SearchSetCurrent(-1)
	} else {
		term.vt.SearchSetCurrent(currentIdx)
		row = term.matchRow(term.vt.SearchMatches[currentIdx])
	}
	term.SearchQuery = query
	term.SearchCurrentRow = row
	term.needsRedraw = true
	return count, row
}

func (term *Vterm) ScrollPercent() float64 {
	term.mu.Lock()
	defer term.mu.Unlock()
	used := term.usedHeightLocked()
	return min(1, float64(term.Offset+term.Height)/float64(used))
}

const reset = termenv.CSI + termenv.ResetSeq + "m"

// View returns the output for the current region of the terminal, with ANSI
// formatting or rendered Markdown if present.
func (term *Vterm) View() string {
	term.mu.Lock()
	defer term.mu.Unlock()
	term.materializeLocked()
	defer term.cache.touch(term)
	if err := term.errLocked(); err != nil {
		return fmt.Sprintf("[log storage error: %s]\n", err)
	}
	if term.segments != nil {
		term.layoutMedia()
		term.viewBuf.Reset()
		term.renderMedia(term.viewBuf, term.Offset, term.Height)
		return term.viewBuf.String()
	}
	if term.needsRedraw {
		term.redraw()
		term.needsRedraw = false
	}
	return term.viewBuf.String()
}

var MarkdownStyle = styles.LightStyleConfig

func init() {
	if HasDarkBackground() {
		MarkdownStyle = styles.DarkStyleConfig
	}

	// We don't need any extra margin.
	MarkdownStyle.Document.Margin = nil

	// No real point setting a custom foreground, it just looks weird.
	MarkdownStyle.Document.Color = nil

	// Render inline code without the default padding spaces on either side.
	MarkdownStyle.Code.Prefix = ""
	MarkdownStyle.Code.Suffix = ""
}

func (term *Vterm) redraw() {
	term.viewBuf.Reset()

	// First render any Markdown content
	if term.markdownBuf.Len() > 0 {
		renderer, _ := glamour.NewTermRenderer(
			glamour.WithWordWrap(term.Width-lipgloss.Width(term.Prefix)),
			glamour.WithStyles(MarkdownStyle),
			// Constrain rendering to the 16-color ANSI palette.
			glamour.WithColorProfile(termenv.ANSI),
			glamour.WithChromaFormatter("terminal16"),
			glamour.WithPreservedNewLines(),
			glamour.WithEmoji(),
		)

		text, err := term.markdownBuf.contents()
		if err != nil {
			term.storageErr = err
			fmt.Fprintf(term.viewBuf, "[log storage error: %s]\n", err)
			return
		}
		rendered, err := renderer.Render(text)
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
	term.renderLocked(term.viewBuf, term.Offset, term.Height)

	// In agent / NO_COLOR mode (Ascii profile), escape codes are pure noise to a
	// text consumer. midterm still emits SGR resets even with colour disabled, so
	// strip them from the rendered view. ansi.Strip only removes escape
	// sequences; the log text itself is left untouched.
	if term.Profile == termenv.Ascii {
		stripped := ansi.Strip(term.viewBuf.String())
		term.viewBuf.Reset()
		term.viewBuf.WriteString(stripped)
	}
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
		// Constrain rendering to the 16-color ANSI palette.
		glamour.WithColorProfile(termenv.ANSI),
		glamour.WithChromaFormatter("terminal16"),
		glamour.WithPreservedNewLines(),
		glamour.WithEmoji(),
	}
	if m.Width != 0 {
		// Subtract 2 for a margin on the right edge, matching the prefix
		// margin on the left.
		glamourOpts = append(glamourOpts,
			glamour.WithWordWrap(m.Width-lipgloss.Width(m.Prefix)-2))
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
	term.mu.Lock()
	defer term.mu.Unlock()
	term.materializeLocked()
	term.renderLocked(w, offset, height)
}

func (term *Vterm) renderLocked(w io.Writer, offset, height int) {
	if term.storageErr != nil {
		fmt.Fprintf(w, "[log storage error: %s]\n", term.storageErr)
		return
	}
	if term.segments != nil {
		term.layoutMedia()
		term.renderMedia(w, offset, height)
		return
	}
	end := max(0, offset) + max(0, height)
	for row, layout := range term.textRows {
		if layout.offset+len(layout.wraps) <= offset {
			continue
		}
		if layout.offset >= end {
			break
		}
		var line strings.Builder
		term.vt.RenderLineFgBg(&line, row, nil, nil)
		for i, wrap := range layout.wraps {
			if layout.offset+i < offset || layout.offset+i >= end {
				continue
			}
			// Cut carries the logical line's ANSI style into every visible
			// slice, even when earlier wrapped rows are outside the viewport.
			fmt.Fprintln(w, term.Prefix+ansi.Cut(line.String(), wrap.start, wrap.end)+reset)
		}
	}
}

// LastLine returns the last line of visible text, with ANSI formatting, but
// without any trailing whitespace.
func (term *Vterm) LastLine() string {
	term.mu.Lock()
	defer term.mu.Unlock()
	term.materializeLocked()
	if term.storageErr != nil {
		return fmt.Sprintf("[log storage error: %s]", term.storageErr)
	}
	used := term.vt.UsedHeight()
	if used == 0 {
		return ""
	}
	var lastLine string
	for row := used - 1; row >= 0; row-- {
		buf := new(strings.Builder)
		_ = term.vt.RenderLine(buf, row)
		if strings.TrimSpace(ansi.Strip(buf.String())) == "" {
			continue
		}
		lastLine = strings.TrimRightFunc(buf.String(), unicode.IsSpace)
		if term.segments == nil {
			wraps := term.textRows[row].wraps
			wrap := wraps[len(wraps)-1]
			lastLine = ansi.Cut(lastLine, wrap.start, wrap.end)
		}
		break
	}
	return lastLine + reset
}

// Print prints the full log output without any formatting.
func (term *Vterm) Print(w io.Writer) error {
	term.mu.Lock()
	defer term.mu.Unlock()
	if err := term.errLocked(); err != nil {
		return err
	}
	if term.segments != nil {
		text, err := term.rawBuf.contents()
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, ansi.Strip(text))
		return err
	}
	term.materializeLocked()
	if err := term.errLocked(); err != nil {
		return err
	}
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

// PrintRaw prints the bytes written to the log terminal without applying any
// terminal wrapping or markdown rendering.
func (term *Vterm) PrintRaw(w io.Writer) error {
	term.mu.Lock()
	defer term.mu.Unlock()
	if err := term.errLocked(); err != nil {
		return err
	}
	if term.Profile == termenv.Ascii {
		// Agent / NO_COLOR mode: strip only ANSI escapes, not log content.
		text, err := term.rawBuf.contents()
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, ansi.Strip(text))
		return err
	}
	return term.rawBuf.replay(w)
}
