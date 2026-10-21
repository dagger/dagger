package idtui

import (
	"bytes"
	"container/list"
	"hash/maphash"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"unsafe"

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
	// length is the number of runes laid out (see textEnd).
	length int
	// wraps holds the midterm rune column where each wrapped row starts. A
	// wrapped row ends where the next one starts, the last one at length.
	wraps []int
	// blocks hashes each complete wrapHashBlock-rune block of the laid-out
	// text, so a changed row only re-wraps from its first edited block.
	blocks []uint64
}

func (layout *vtermTextRow) wrapEnd(i int) int {
	if i+1 < len(layout.wraps) {
		return layout.wraps[i+1]
	}
	return layout.length
}

// A long logical line (progress dots, minified JSON) is rewritten on every
// live chunk. Hashing it runs at memory speed; grapheme-aware wrapping does
// not, so only the edited tail is wrapped again. Short rows have no blocks.
const wrapHashBlock = 1024

var wrapHashSeed = maphash.MakeSeed()

func hashRunes(text []rune) uint64 {
	return maphash.Bytes(wrapHashSeed, unsafe.Slice((*byte)(unsafe.Pointer(unsafe.SliceData(text))), len(text)*4))
}

// wrapRunes appends the start column of each row that text[start:] wraps
// into. Wrapping is greedy, so restarting at a previous row start yields the
// same rows as wrapping the whole line. It returns the number of runes wrapped.
func wrapRunes(dst []int, text []rune, start, width int) ([]int, int) {
	if width <= 0 {
		return append(dst, start), 0
	}
	col := start
	for part := range strings.SplitSeq(ansi.Hardwrap(string(text[start:]), width, true), "\n") {
		dst = append(dst, col)
		col += utf8.RuneCountInString(part)
	}
	return dst, len(text) - start
}

// layoutRow wraps one logical row. Unchanged complete blocks keep the wrapped
// rows before them; wrapping resumes a row before the first edit, since a
// grapheme cluster appended across a row boundary can change where the
// previous rows broke.
func (term *Vterm) layoutRow(layout *vtermTextRow, row, width int) {
	content := term.vt.Content[row]
	end := textEnd(term.vt, row)
	text := content[:end]
	keep := 0
	if layout.wraps != nil && layout.width == width {
		for keep < len(layout.blocks) && (keep+1)*wrapHashBlock <= end &&
			hashRunes(text[keep*wrapHashBlock:(keep+1)*wrapHashBlock]) == layout.blocks[keep] {
			keep++
		}
	} else {
		layout.wraps = layout.wraps[:0]
	}
	resume := 0
	if edit := min(keep*wrapHashBlock, end); edit > 0 && len(layout.wraps) > 0 {
		// The wrapped row holding the last unchanged rune, and one before it.
		idx := sort.SearchInts(layout.wraps, edit) - 1
		resume = max(0, idx-1)
	}
	start := 0
	if resume < len(layout.wraps) {
		start = layout.wraps[resume]
	}
	layout.blocks = layout.blocks[:keep]
	for (len(layout.blocks)+1)*wrapHashBlock <= end {
		n := len(layout.blocks)
		layout.blocks = append(layout.blocks, hashRunes(text[n*wrapHashBlock:(n+1)*wrapHashBlock]))
	}
	var wrapped int
	layout.wraps, wrapped = wrapRunes(layout.wraps[:min(resume, len(layout.wraps))], text, start, width)
	term.wrapWork += wrapped
	layout.length = end
	layout.width = width
}

// textEnd omits plain erase padding while preserving visible whitespace such
// as background-colored progress bars, reverse video and underlined spaces.
func textEnd(terminal *midterm.Terminal, row int) int {
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
	return end
}

// vtermCells renders column ranges of one midterm row like RenderLine does,
// with its formats, search highlights and cursor, so a wrapped row costs its
// own cells rather than a render and ANSI-aware cut of the whole logical line.
// Ranges must be requested in ascending order; together they take one pass
// over the row's format regions.
type vtermCells struct {
	vt     *midterm.Terminal
	line   []rune
	region *midterm.Region
	pos    int // column where region starts
	hls    []midterm.SearchHighlight
	cursor int
}

func newVtermCells(vt *midterm.Terminal, row int) *vtermCells {
	cells := &vtermCells{vt: vt, line: vt.Content[row], cursor: -1}
	if row < len(vt.Format.Rows) {
		cells.region = vt.Format.Rows[row]
	}
	if vt.SearchHighlights != nil {
		cells.hls = vt.SearchHighlights[row]
	}
	if vt.CursorVisible && vt.Cursor.Y == row &&
		(vt.CursorBlinkEpoch == nil || int(time.Since(*vt.CursorBlinkEpoch).Seconds())%2 == 0) {
		cells.cursor = vt.Cursor.X
	}
	return cells
}

// render writes columns [from, to), starting from a clean SGR state, without
// a trailing reset.
func (cells *vtermCells) render(w *strings.Builder, from, to int) {
	to = min(to, len(cells.line))
	last := midterm.EmptyFormat
	for from < to {
		for cells.region != nil && cells.pos+cells.region.Size <= from {
			cells.pos += cells.region.Size
			cells.region = cells.region.Next
		}
		if cells.region == nil {
			return
		}
		f := cells.region.F
		end := min(to, cells.pos+cells.region.Size)
		for len(cells.hls) > 0 && cells.hls[0].End <= from {
			cells.hls = cells.hls[1:]
		}
		if len(cells.hls) > 0 {
			if hl := cells.hls[0]; hl.Col <= from {
				f = cells.vt.SearchMatchStyle
				if hl.Current {
					f = cells.vt.SearchCurrentStyle
				}
				end = min(end, hl.End)
			} else {
				end = min(end, hl.Col)
			}
		}
		if cells.cursor == from {
			f = cells.region.F
			f.SetReverse(!f.IsReverse())
			end = from + 1
		} else if cells.cursor > from && cells.cursor < end {
			end = cells.cursor
		}
		if f != last {
			if formatLeaks(last, f) {
				w.WriteString(reset)
			}
			w.WriteString(f.Render())
			last = f
		}
		w.WriteString(string(cells.line[from:end]))
		from = end
	}
}

// formatLeaks mirrors midterm: Format.Render emits only "on" sequences, so any
// attribute or color the previous format set that f drops needs a reset first.
func formatLeaks(prev, f midterm.Format) bool {
	if f.IsReset() || f == (midterm.Format{}) {
		return false
	}
	const attrs = midterm.BoldBit | midterm.FaintBit | midterm.ItalicBit | midterm.UnderlineBit |
		midterm.BlinkBit | midterm.ReverseBit | midterm.ConcealBit
	if (prev.Properties&attrs)&^f.Properties != 0 {
		return true
	}
	if prev.Fg != nil && f.Fg == nil {
		return true
	}
	return prev.Bg != nil && f.Bg == nil
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
			term.layoutRow(layout, row, width)
			layout.changes = term.vt.Changes[row]
		}
		layout.offset = height
		height += len(layout.wraps)
		term.cacheCells += len(term.vt.Content[row]) + 12 + 2*len(layout.wraps) + 2*len(layout.blocks)
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
	wrap := sort.Search(len(layout.wraps), func(i int) bool { return layout.wraps[i] > match.Col }) - 1
	return layout.offset + max(0, wrap)
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
