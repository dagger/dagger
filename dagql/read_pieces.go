package dagql

import "time"

// readPieceSteps is how many steps a read in pieces (readInPieces) takes in
// one hold of egraphMu: a count, not a time, so that a read is the same on
// every machine. A step of the Cloud's closure walks takes about one to two
// microseconds, so a piece about one to two milliseconds.
const readPieceSteps = 1024

// readPieces is a read of the cache that holds egraphMu for reading in
// pieces of readPieceSteps steps, letting it go between them, so that a
// writer waits for one piece rather than the whole read. Outside the
// service's work lock, the Cloud's long reads, such as a closure's walk, held
// the lock up to seconds, and every write waited for them.
type readPieces struct {
	c     *Cache
	size  int
	steps int
	// report, if set, is told the length of each hold, as it ends; locked
	// is when the current one began.
	report func(time.Duration)
	locked time.Time
	// held is whether the read holds egraphMu: not between two pieces, so
	// that done, deferred by the read, lets go only of a hold it has, even
	// when the read ends there, as a test's failed check in
	// testReadPieceYielded ends its goroutine.
	held bool
}

// readInPieces takes egraphMu for reading, for a read in pieces: step after
// each step of the read, and done at its end.
func (c *Cache) readInPieces() *readPieces {
	size := c.readPieceSize
	if size <= 0 {
		size = readPieceSteps
	}
	c.egraphMu.RLock()
	return &readPieces{c: c, size: size, locked: time.Now(), held: true}
}

// step counts a step of the read and, after each piece's last, lets egraphMu
// go and takes it again. It reports whether it did: the cache may have
// changed since the read's last step, and a caller that needs one state
// checks for that.
func (r *readPieces) step() bool {
	r.steps++
	if r.steps%r.size != 0 {
		return false
	}
	r.release()
	if r.c.testReadPieceYielded != nil {
		r.c.testReadPieceYielded()
	}
	r.c.egraphMu.RLock()
	r.held = true
	r.locked = time.Now()
	return true
}

// yields is how many times the read has let egraphMu go so far: a caller
// that holds a pointer across a stretch of the read compares it before and
// after, to know whether to read it again. A nil read never lets it go.
func (r *readPieces) yields() int {
	if r == nil {
		return 0
	}
	return r.steps / r.size
}

// done ends the read, letting egraphMu go if it holds it.
func (r *readPieces) done() {
	if r.held {
		r.release()
	}
}

func (r *readPieces) release() {
	hold := time.Since(r.locked)
	r.held = false
	r.c.egraphMu.RUnlock()
	if r.report != nil {
		r.report(hold)
	}
}
