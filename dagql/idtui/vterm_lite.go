package idtui

type cursorBuffer struct {
	buf    []byte
	cursor int
}

func newCursorBuffer(s []byte) cursorBuffer {
	return cursorBuffer{
		buf:    s,
		cursor: len(s),
	}
}

func (l *cursorBuffer) String() string {
	return string(l.buf)
}

func (l *cursorBuffer) Write(s []byte) (int, error) {
	// grow the buffer capacity to include space for the entire input (which is
	// the most common case, carriage returns are pretty uncommon)
	if cap(l.buf) < len(l.buf)+len(s) {
		l.buf = append(l.buf, make([]byte, len(s))...)[:len(l.buf)]
	}

	for _, ch := range s {
		if ch == '\r' {
			// go back to the beginning, and start overwriting output
			l.cursor = 0
			continue
		}
		if l.cursor < len(l.buf) {
			l.buf[l.cursor] = ch
		} else {
			l.buf = append(l.buf, ch)
		}
		l.cursor++
	}
	return len(s), nil
}
