package journal

import (
	"sync"
)

func Pipe() (Reader, Writer) {
	ch := make(chan *Entry)
	return chanReader(ch), &chanWriter{ch: ch}
}

type chanWriter struct {
	ch chan<- *Entry

	sync.Mutex
}

func (doc *chanWriter) WriteStatus(v *Entry) error {
	doc.Lock()
	defer doc.Unlock()

	if doc.ch == nil {
		// discard
		return nil
	}

	doc.ch <- v
	return nil
}

func (doc *chanWriter) Close() error {
	doc.Lock()
	if doc.ch != nil {
		close(doc.ch)
		doc.ch = nil
	}
	doc.Unlock()
	return nil
}

type chanReader <-chan *Entry

func (ch chanReader) ReadStatus() (*Entry, bool) {
	val, ok := <-ch
	return val, ok
}
