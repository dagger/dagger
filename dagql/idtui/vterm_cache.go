package idtui

import (
	"bytes"
	"container/list"
	"strings"
	"sync"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/vito/midterm"
)

// Bound retained display state, not the authoritative log. A single active
// terminal may exceed the cell budget: truncating it would change cursor/ANSI
// semantics and make search silently incomplete. Moving to another terminal
// evicts it. Rebuilding and searching always replay the complete stream.
const (
	terminalCacheCount = 16
	terminalCacheCells = 1024 * 1024
)

type terminalCache struct {
	mu                 sync.Mutex
	entries            list.List
	byTerm             map[*Vterm]*list.Element
	cells              int
	maxCount, maxCells int
}

type terminalCacheEntry struct {
	term  *Vterm
	cells int
}

func newTerminalCache() *terminalCache {
	return &terminalCache{byTerm: make(map[*Vterm]*list.Element), maxCount: terminalCacheCount, maxCells: terminalCacheCells}
}

// touch is called with term.mu held. TryLock avoids lock inversion with another
// terminal being displayed concurrently; busy entries are retried next time.
func (c *terminalCache) touch(term *Vterm) {
	if c == nil || term.vt == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cells := term.cacheCells + term.viewBuf.Len()/4
	if el := c.byTerm[term]; el != nil {
		entry := el.Value.(*terminalCacheEntry)
		c.cells -= entry.cells
		entry.cells = cells
		c.entries.MoveToFront(el)
	} else {
		c.byTerm[term] = c.entries.PushFront(&terminalCacheEntry{term, cells})
	}
	c.cells += cells
	for el := c.entries.Back(); el != nil && (c.entries.Len() > c.maxCount || c.cells > c.maxCells); {
		previous := el.Prev()
		entry := el.Value.(*terminalCacheEntry)
		if entry.term != term && entry.term.mu.TryLock() {
			entry.term.evictLocked()
			entry.term.mu.Unlock()
			delete(c.byTerm, entry.term)
			c.cells -= entry.cells
			c.entries.Remove(el)
		}
		el = previous
	}
}

func (term *Vterm) evictLocked() {
	if term.segments != nil {
		term.mediaFollow = term.mediaAtBottom()
	} else {
		term.rememberFollowLocked()
	}
	term.vt = nil
	term.textRows = nil
	term.textHeight = 0
	term.cacheCells = 0
	term.layoutDirty = true
	term.mediaRows = nil
	term.viewBuf = new(bytes.Buffer)
	term.needsRedraw = true
}

func (term *Vterm) materializeLocked() {
	if term.segments != nil {
		term.layoutMedia()
		term.cache.touch(term)
		return
	}
	if term.vt == nil {
		// Engine logs have no terminal width. Cursor edits are interpreted in
		// logical lines; display wrapping must never affect these coordinates.
		term.vt = midterm.NewAutoResizingTerminal()
		if err := term.terminalBuf.replay(term.vt); err != nil {
			term.storageErr = err
		}
		term.layoutDirty = true
		if term.SearchQuery != "" {
			term.vt.Search(term.SearchQuery)
		}
	}
	term.layoutTextLocked()
	term.cache.touch(term)
}

func (term *Vterm) writeTerminalLocked(p []byte) error {
	if _, err := term.terminalBuf.Write(p); err != nil {
		term.storageErr = err
		return err
	}
	if term.vt != nil {
		term.rememberFollowLocked()
		if _, err := term.vt.Write(p); err != nil {
			return err
		}
		term.layoutDirty = true
		// Avoid scanning the full log on each live chunk. This estimate may
		// overcount cursor rewrites; layout recounts the actual ragged cells.
		term.cacheCells += len(p)
		term.cache.touch(term)
	}
	term.needsRedraw = true
	return nil
}

// Retain the last viewport's follow decision through any number of writes or
// resizes, without laying out streamed input until a consumer asks for it.
func (term *Vterm) rememberFollowLocked() {
	if term.vt != nil && !term.layoutDirty {
		term.follow = term.Height == 0 || term.Offset+term.Height >= term.textHeight
	}
}

type vtermTextRow struct {
	changes uint64
	width   int
	offset  int
	wraps   []vtermWrap
}

type vtermWrap struct {
	col        int // midterm rune column, for search mapping
	start, end int // display columns, for ANSI-aware slicing
}

// wrapLine contains only presentation metadata, not another terminal or a copy
// of the rendered text. ANSI parsing and progress edits happen once in midterm.
func wrapLine(line string, width int) []vtermWrap {
	lines := []string{line}
	if width > 0 {
		lines = strings.Split(ansi.Hardwrap(line, width, true), "\n")
	}
	wraps := make([]vtermWrap, 0, len(lines))
	col, start := 0, 0
	for _, part := range lines {
		end := start + ansi.StringWidth(part)
		wraps = append(wraps, vtermWrap{col: col, start: start, end: end})
		col += utf8.RuneCountInString(part)
		start = end
	}
	return wraps
}

// textLine omits plain erase padding while preserving visible whitespace such
// as background-colored progress bars, reverse video and underlined spaces.
func textLine(terminal *midterm.Terminal, row int) string {
	content := terminal.Content[row]
	end := len(content)
	for end > 0 && content[end-1] == ' ' {
		end--
	}
	col := 0
	for region := range terminal.Format.Regions(row) {
		col += region.Size
		if region.F.Bg != nil || region.F.IsReverse() || region.F.IsUnderline() {
			end = max(end, min(col, len(content)))
		}
	}
	return string(content[:end])
}

func (term *Vterm) layoutTextLocked() {
	if !term.layoutDirty {
		return
	}
	width := 0
	if term.Width > 0 {
		width = max(1, term.Width-lipgloss.Width(term.Prefix))
	}
	used := term.vt.UsedHeight()
	for len(term.textRows) < used {
		term.textRows = append(term.textRows, vtermTextRow{})
	}
	term.textRows = term.textRows[:used]
	height := 0
	term.cacheCells = 0
	for row := range term.textRows {
		layout := &term.textRows[row]
		if layout.wraps == nil || layout.changes != term.vt.Changes[row] || layout.width != width {
			// Erasing a progress line leaves blank cells in midterm. They must
			// not become extra empty wrapped rows after a shorter replacement.
			layout.wraps = wrapLine(textLine(term.vt, row), width)
			layout.changes = term.vt.Changes[row]
			layout.width = width
		}
		layout.offset = height
		height += len(layout.wraps)
		term.cacheCells += len(term.vt.Content[row]) + 12 + 6*len(layout.wraps)
	}
	term.textHeight = height
	term.layoutDirty = false
	if term.follow {
		term.Offset = max(0, height-term.Height)
	} else {
		term.Offset = min(term.Offset, max(0, height-term.Height))
	}
	if term.SearchQuery != "" {
		term.vt.Search(term.SearchQuery)
		term.setCurrentMatchByRow(term.SearchCurrentRow)
	}
}

func (term *Vterm) matchRow(match midterm.SearchMatch) int {
	if term.segments != nil {
		return match.Row
	}
	layout := term.textRows[match.Row]
	row := layout.offset
	for _, wrap := range layout.wraps[1:] {
		if wrap.col > match.Col {
			break
		}
		row++
	}
	return row
}

// Close removes disk-backed log storage. The terminal must no longer be used.
// The frontend calls it only after final/noninteractive output is complete.
func (term *Vterm) Close() {
	term.mu.Lock()
	defer term.mu.Unlock()
	term.rawBuf.Reset()
	term.terminalBuf.Reset()
	term.markdownBuf.Reset()
	for _, segment := range term.segments {
		if segment.text != nil {
			segment.text.Reset()
		}
	}
	term.segments = nil
	term.evictLocked()
	term.cache = nil
}

func (term *Vterm) Err() error {
	term.mu.Lock()
	defer term.mu.Unlock()
	return term.errLocked()
}

func (term *Vterm) errLocked() error {
	for _, err := range []error{term.storageErr, term.rawBuf.err, term.terminalBuf.err, term.markdownBuf.err} {
		if err != nil {
			return err
		}
	}
	for _, segment := range term.segments {
		if segment.text != nil && segment.text.err != nil {
			return segment.text.err
		}
	}
	return nil
}
