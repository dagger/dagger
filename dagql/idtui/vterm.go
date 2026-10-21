package idtui

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/danielgatis/go-vte"
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
	markdownBuf            *logBuffer
	markdownTableRuleColor termenv.Color
	// The byte stream is authoritative; midterm is a disposable logical cache.
	terminalBuf *logBuffer
	cache       *terminalCache
	follow      bool
	storageErr  error
	textRows    []vtermTextRow
	textHeight  int
	layoutAlt   bool // whether textRows were laid out from the alternate screen
	layoutDirty bool
	cacheCells  int
	// wrapWork counts runes passed to the wrapper, for complexity tests.
	wrapWork int
	// replays counts complete log replays into a terminal, for tests.
	replays int
	// version changes with every change to the searchable content.
	version uint64
	// Search rows survive eviction, so an active search does not replay every
	// unchanged log on every frame.
	searchRows   []int
	searchKey    vtermSearchKey
	searchCached bool
	// searchedAt is when an evicted log was last searched transiently.
	searchedAt time.Time
	// searchCost is how long that took; searchJob is one still running.
	searchCost time.Duration
	searchJob  *vtermSearchJob
	// Regular terminal buffer
	viewBuf *bytes.Buffer
	// rawBuf is empty until rawSeparate; see separateRawLocked.
	rawBuf      *logBuffer
	rawSeparate bool
	// printed is set once the terminal stream may have printed a character;
	// printScan follows the stream until then. See HasOutput.
	printed     bool
	printScan   *printScanner
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

// SearchMatchRows returns the rows matching SearchQuery, safe even if another
// display evicts the terminal. Search is over the complete log, never a
// truncated preview. The result is shared: callers must not modify it.
func (term *Vterm) SearchMatchRows() []int {
	term.mu.Lock()
	defer term.mu.Unlock()
	return term.searchMatchRowsLocked()
}

func (term *Vterm) WriteMarkdown(p []byte) (int, error) {
	term.mu.Lock()
	defer term.mu.Unlock()

	if term.segments != nil {
		return term.writeMediaText(p, true, false)
	}
	if err := term.separateRawLocked(); err != nil {
		return 0, err
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
	if err := term.separateRawLocked(); err != nil {
		return 0, err
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
	if !term.rawSeparate {
		return len(p), nil
	}
	n, err := term.rawBuf.Write(p)
	if err != nil {
		term.storageErr = err
	}
	return n, err
}

// separateRawLocked is called before the first write whose raw bytes differ
// from the terminal stream (Markdown, diffs, media). Until then the raw log
// is exactly terminalBuf, so ordinary output is not stored, or spilled to
// disk, twice.
func (term *Vterm) separateRawLocked() error {
	if term.rawSeparate {
		return nil
	}
	term.rawSeparate = true
	if err := term.terminalBuf.replay(term.rawBuf); err != nil {
		term.storageErr = err
		return err
	}
	return nil
}

// rawLocked is the buffer holding the bytes written to the log, before any
// highlighting or rendering.
func (term *Vterm) rawLocked() *logBuffer {
	if term.rawSeparate {
		return term.rawBuf
	}
	return term.terminalBuf
}

// UsedHeight lays the log out to count its rows, materializing it. Check
// HasOutput first where most logs are not drawn.
func (term *Vterm) UsedHeight() int {
	term.mu.Lock()
	defer term.mu.Unlock()
	return term.usedHeightLocked()
}

// HasOutput reports whether the log may have rows, without materializing it:
// false guarantees UsedHeight() == 0, so an emptiness guard can skip the log
// before UsedHeight lays it out. It errs towards true: media logs and text
// printed only on the alternate screen count as output, so code that draws
// still checks UsedHeight. As with UsedHeight, Markdown in a log without
// media is not counted.
func (term *Vterm) HasOutput() bool {
	term.mu.Lock()
	defer term.mu.Unlock()
	if term.segments != nil {
		return true
	}
	if term.vt != nil && term.vt.MaxY >= 0 {
		// Exact once materialized, whatever the scan below concluded.
		term.printed = true
	}
	return term.printed
}

// notePrintedLocked runs terminal input through midterm's own escape parser
// (go-vte, under go-ansicode) until midterm would print: only printed runes
// and horizontal tabs, which midterm fills with spaces, give it a used row
// (erased text still leaves one). Escape sequences, strings such as OSC, DCS
// and SOS/PM/APC however they end, and other controls do not.
func (term *Vterm) notePrintedLocked(p []byte) {
	if term.printScan == nil && len(p) > 0 && (p[0] == '\t' || p[0] >= 0x20 && p[0] < 0x7f) {
		// The parser starts in its ground state, where these print: most
		// logs need no parser at all.
		term.printed = true
		return
	}
	if term.printScan == nil {
		term.printScan = new(printScanner)
		term.printScan.parser = vte.NewParser(term.printScan)
	}
	for _, b := range p {
		term.printScan.parser.Advance(b)
		if term.printScan.printed {
			term.printed = true
			term.printScan = nil
			return
		}
	}
}

// printScanner is a go-vte performer that only notes whether go-ansicode would
// make midterm put a cell: Print (Input) and HT (Tab). CSI REP repeats the
// preceding printed rune, so it can't be the first to print.
type printScanner struct {
	parser  *vte.Parser
	printed bool
}

func (s *printScanner) Print(rune) { s.printed = true }
func (s *printScanner) Execute(b byte) {
	if b == '\t' {
		s.printed = true
	}
}
func (s *printScanner) Put(byte)                                        {}
func (s *printScanner) Unhook()                                         {}
func (s *printScanner) Hook([][]uint16, []byte, bool, rune)             {}
func (s *printScanner) OscDispatch([][]byte, bool)                      {}
func (s *printScanner) CsiDispatch([][]uint16, []byte, bool, rune)      {}
func (s *printScanner) EscDispatch([]byte, bool, byte)                  {}
func (s *printScanner) SosPmApcDispatch(vte.SosPmApcKind, []byte, bool) {}

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

func (term *Vterm) setMarkdownTableRuleColor(color termenv.Color) {
	term.mu.Lock()
	defer term.mu.Unlock()
	if color == term.markdownTableRuleColor {
		return
	}
	term.markdownTableRuleColor = color
	term.invalidateMedia() // also invalidates the text-only view cache
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
// A materialized terminal re-runs midterm's incremental search so that new
// content is picked up. An evicted one only stores the query: materializing
// it searches, and an active search must not replay every log every frame.
func (term *Vterm) SetSearchHighlight(query string, currentRow int) {
	term.mu.Lock()
	defer term.mu.Unlock()

	if term.SearchQuery != query || term.SearchCurrentRow != currentRow {
		term.needsRedraw = true
	}
	if query == "" && term.SearchQuery != "" && term.vt != nil {
		term.vt.SearchClear()
	}
	if query == "" {
		currentRow = -1
	}
	term.SearchQuery = query
	term.SearchCurrentRow = currentRow
	if query == "" || term.vt == nil || (term.segments != nil && term.mediaRows == nil) {
		// A pending media layout rebuilds and searches its projection.
		return
	}
	term.searchLiveLocked()
}

// setCurrentMatchByRow finds the first search match on the given row and
// marks it as "current" in midterm. Must be called with term.mu held.
func (term *Vterm) setCurrentMatchByRow(row int) {
	term.vt.SearchSetCurrent(-1)
	if row < 0 {
		return
	}
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
	defer term.cache.show(term)
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

	// Inline code is frequent enough that a background plus a foreground is
	// noisy and hard to read: just color it, in the terminal's own bright
	// blue (plain blue is too dark on many dark themes).
	brightBlue := "12"
	MarkdownStyle.Code.Color = &brightBlue
	MarkdownStyle.Code.BackgroundColor = nil
}

func (term *Vterm) redraw() {
	term.viewBuf.Reset()

	// First render any Markdown content
	if term.markdownBuf.Len() > 0 {
		text, err := term.markdownBuf.contents()
		if err != nil {
			term.storageErr = err
			fmt.Fprintf(term.viewBuf, "[log storage error: %s]\n", err)
			return
		}
		rendered, err := renderMarkdown(text, term.mediaWidth(), MarkdownStyle, term.markdownTableRuleColor)
		if err != nil {
			fmt.Fprintf(term.viewBuf, "Error rendering Markdown: %s\n", err)
		} else {
			// Remove leading and trailing newlines
			rendered = trimMarkdownPadding(rendered)
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
	Content        string
	Background     termenv.Color
	TableRuleColor termenv.Color
	Prefix         string
	Width          int

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
	width := 80
	if m.Width != 0 {
		// Subtract 2 for a margin on the right edge, matching the prefix
		// margin on the left.
		width = max(1, m.Width-lipgloss.Width(m.Prefix)-2)
	}
	rendered, err := renderMarkdown(m.Content, width, st, m.TableRuleColor)
	if err != nil {
		return fmt.Sprintf("Error rendering Markdown: %s\n", err)
	} else if m.Prefix != "" {
		// Remove leading and trailing newlines
		rendered = trimMarkdownPadding(rendered)
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
	first := sort.Search(len(term.textRows), func(row int) bool {
		layout := &term.textRows[row]
		return layout.offset+len(layout.wraps) > offset
	})
	var line strings.Builder
	for row := first; row < len(term.textRows); row++ {
		layout := &term.textRows[row]
		if layout.offset >= end {
			break
		}
		// Each visible slice starts from its own SGR state, so the logical
		// line's style carries into continuation rows even when earlier
		// wrapped rows are outside the viewport.
		cells := newVtermCells(term.vt, row)
		for i := max(0, offset-layout.offset); i < len(layout.wraps) && layout.offset+i < end; i++ {
			line.Reset()
			line.WriteString(term.Prefix)
			cells.render(&line, layout.wraps[i], layout.wrapEnd(i))
			line.WriteString(reset)
			line.WriteByte('\n')
			_, _ = io.WriteString(w, line.String())
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
	if term.segments == nil {
		used = len(term.textRows) // the rows laid out, which index textRows
	}
	if used == 0 {
		return ""
	}
	var lastLine string
	for row := used - 1; row >= 0; row-- {
		if term.segments == nil {
			// Render only the last wrapped row of the logical line.
			layout := &term.textRows[row]
			i := len(layout.wraps) - 1
			if !slices.ContainsFunc(term.vt.Content[row][:layout.length], func(r rune) bool { return !unicode.IsSpace(r) }) {
				continue
			}
			var buf strings.Builder
			newVtermCells(term.vt, row).render(&buf, layout.wraps[i], layout.wrapEnd(i))
			lastLine = strings.TrimRightFunc(buf.String(), unicode.IsSpace)
			break
		}
		buf := new(strings.Builder)
		_ = term.vt.RenderLine(buf, row)
		if strings.TrimSpace(ansi.Strip(buf.String())) == "" {
			continue
		}
		lastLine = strings.TrimRightFunc(buf.String(), unicode.IsSpace)
		break
	}
	return lastLine + reset
}

// Print prints the log output without any formatting, as the rows View lays
// out: wrapped at the viewport width, when one is set. PrintRaw is the
// byte-exact output.
func (term *Vterm) Print(w io.Writer) error {
	term.mu.Lock()
	defer term.mu.Unlock()
	if err := term.errLocked(); err != nil {
		return err
	}
	if term.segments != nil {
		text, err := term.rawLocked().contents()
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
	var line strings.Builder
	for row := range term.textRows {
		layout := &term.textRows[row]
		content := term.vt.Content[row]
		for i, start := range layout.wraps {
			line.Reset()
			line.WriteString(strings.TrimRight(string(content[start:layout.wrapEnd(i)]), " "))
			line.WriteByte('\n')
			if _, err := io.WriteString(w, line.String()); err != nil {
				return err
			}
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
		text, err := term.rawLocked().contents()
		if err != nil {
			return err
		}
		_, err = io.WriteString(w, ansi.Strip(text))
		return err
	}
	return term.rawLocked().replay(w)
}
