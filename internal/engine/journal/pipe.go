package journal

import (
	"errors"
	"sync"
)

func Pipe() (Reader, Writer) {
	pipe := &unboundedPipe{
		cond: sync.NewCond(&sync.Mutex{}),
	}
	return pipe, pipe
}

type unboundedPipe struct {
	cond   *sync.Cond
	mu     sync.Mutex
	buffer []*Entry
	closed bool
}

func (p *unboundedPipe) WriteEntry(value *Entry) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return errors.New("pipe is closed")
	}

	p.buffer = append(p.buffer, value)
	p.cond.Signal()
	return nil
}

func (p *unboundedPipe) ReadEntry() (*Entry, bool) {
	p.cond.L.Lock()
	defer p.cond.L.Unlock()

	for len(p.buffer) == 0 && !p.closed {
		p.cond.Wait()
	}

	if len(p.buffer) == 0 && p.closed {
		return nil, false
	}

	value := p.buffer[0]
	p.buffer = p.buffer[1:]
	return value, true
}

func (p *unboundedPipe) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true
	p.cond.Broadcast()
	return nil
}
