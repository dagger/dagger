package idtui

import (
	"bytes"
	"container/list"
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/vito/midterm"
)

// Bound retained display state, not the authoritative log. A single active
// terminal may exceed the cell budget: truncating it would change cursor/ANSI
// semantics and make search silently incomplete. Moving to another terminal
// evicts it. Rebuilding and searching always replay the complete journal.
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
	cells := term.viewBuf.Len() / 4
	if len(term.vt.Content) > 0 {
		cells += len(term.vt.Content) * len(term.vt.Content[0])
	}
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
	}
	if term.vt != nil && term.segments == nil {
		term.follow = term.Height == 0 || term.Offset+term.Height >= term.vt.UsedHeight()
	}
	term.vt = nil
	term.mediaRows = nil
	term.viewBuf = new(bytes.Buffer)
	term.needsRedraw = true
}

// journal records resizes as well as writes, preserving the exact terminal
// interpretation across eviction (replaying at just the final width would not).
func (term *Vterm) journal(op byte, payload []byte, width int) error {
	if op == 'r' {
		if width == term.terminalWidth {
			return nil
		}
		term.terminalWidth = width
	}
	if op == 'w' && len(payload) == 0 {
		return nil
	}
	var header [9]byte
	header[0] = op
	n := len(payload)
	if op == 'r' {
		n = width
	}
	binary.LittleEndian.PutUint64(header[1:], uint64(n))
	data := append(header[:], payload...)
	_, err := term.terminalBuf.Write(data)
	if err != nil {
		term.storageErr = err
	}
	return err
}

func replayTerminal(buf *logBuffer, terminal *midterm.Terminal) error {
	return buf.withReader(func(r io.Reader) error {
		var header [9]byte
		for {
			_, err := io.ReadFull(r, header[:])
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			n := binary.LittleEndian.Uint64(header[1:])
			switch header[0] {
			case 'r':
				terminal.ResizeX(int(n))
			case 'w':
				if _, err := io.CopyN(terminal, r, int64(n)); err != nil {
					return err
				}
			default:
				return fmt.Errorf("invalid terminal journal operation %q", header[0])
			}
		}
	})
}

func (term *Vterm) materializeLocked() {
	if term.segments != nil {
		term.layoutMedia()
		term.cache.touch(term)
		return
	}
	if term.vt == nil {
		term.vt = midterm.NewAutoResizingTerminal()
		err := replayTerminal(term.terminalBuf, term.vt)
		if err != nil {
			term.storageErr = err
		}
		if term.follow {
			term.Offset = max(0, term.vt.UsedHeight()-term.Height)
		}
		if term.SearchQuery != "" {
			term.vt.Search(term.SearchQuery)
			term.vt.SearchSetCurrent(-1)
			term.setCurrentMatchByRow(term.SearchCurrentRow)
		}
	}
	term.cache.touch(term)
}

func (term *Vterm) writeTerminalLocked(p []byte) error {
	if err := term.journal('w', p, 0); err != nil {
		return err
	}
	if term.vt != nil {
		term.follow = term.Height == 0 || term.Offset+term.Height >= term.vt.UsedHeight()
		if _, err := term.vt.Write(p); err != nil {
			return err
		}
		if term.follow {
			term.Offset = max(0, term.vt.UsedHeight()-term.Height)
		}
		term.cache.touch(term)
	}
	term.needsRedraw = true
	return nil
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
		if segment.journal != nil {
			segment.journal.Reset()
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
		if segment.journal != nil && segment.journal.err != nil {
			return segment.journal.err
		}
	}
	return nil
}
