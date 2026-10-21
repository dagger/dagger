package idtui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync/atomic"

	"github.com/dagger/dagger/engine/slog"
)

// logBuffer keeps small logs cheap and spills larger logs to a private temporary
// file. Files are opened only while reading/writing: thousands of spans must not
// consume thousands of file descriptors. Callers serialize access.
//
// Spilling is an optimization: when temporary storage cannot be created or
// written (an unwritable TMPDIR, a full disk), the rest of the log stays in
// memory after whatever the file already holds, rather than becoming an error.
const logBufferMemoryLimit = 32 * 1024

type logBuffer struct {
	// memory is the whole log while nothing has spilled, and the tail after
	// the file once spilling has failed.
	memory bytes.Buffer
	path   string
	// fileSize is how much of the log the file holds.
	fileSize    int
	spillFailed bool
	size        int
	lastByte    byte
	err         error
	cleanup     runtime.Cleanup
}

// logSpillWarned limits the spill-failure warning to once per process.
var logSpillWarned atomic.Bool

func (b *logBuffer) Write(p []byte) (n int, err error) {
	if b.err != nil {
		return 0, b.err
	}
	defer func() {
		if err != nil {
			b.err = err
		}
	}()
	if len(p) > 0 {
		b.lastByte = p[len(p)-1]
	}
	if b.spillFailed || (b.path == "" && b.size+len(p) <= logBufferMemoryLimit) {
		n, err := b.memory.Write(p)
		b.size += n
		return n, err
	}
	if b.path == "" {
		if err := b.spill(); err != nil {
			b.abandonSpill(err)
			n, err := b.memory.Write(p)
			b.size += n
			return n, err
		}
	}
	if err := b.appendFile(p); err != nil {
		// Cut off a partial write so the file holds exactly fileSize bytes;
		// if even that fails the log can't be replayed faithfully.
		if terr := os.Truncate(b.path, int64(b.fileSize)); terr != nil {
			return 0, fmt.Errorf("append logs: %w", err)
		}
		b.abandonSpill(err)
		n, err := b.memory.Write(p)
		b.size += n
		return n, err
	}
	b.size += len(p)
	return len(p), nil
}

// spill moves the in-memory log to a new temporary file.
func (b *logBuffer) spill() error {
	f, err := os.CreateTemp("", "dagger-logs-*")
	if err != nil {
		return fmt.Errorf("spool logs: %w", err)
	}
	name := f.Name()
	_, err = f.Write(b.memory.Bytes())
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("spool logs: %w", err)
	}
	b.path = name
	b.fileSize = b.memory.Len()
	b.cleanup = runtime.AddCleanup(b, func(path string) { _ = os.Remove(path) }, name)
	b.memory = bytes.Buffer{}
	return nil
}

func (b *logBuffer) appendFile(p []byte) error {
	defer runtime.KeepAlive(b)
	f, err := os.OpenFile(b.path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return fmt.Errorf("append logs: %w", err)
	}
	_, err = f.Write(p)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	b.fileSize += len(p)
	return nil
}

// abandonSpill keeps the rest of the log in memory.
func (b *logBuffer) abandonSpill(err error) {
	b.spillFailed = true
	if logSpillWarned.CompareAndSwap(false, true) {
		// Asynchronously: the frontend may render this log while the
		// caller holds its lock.
		go slog.Warn("cannot spill logs to temporary storage; keeping them in memory", "err", err)
	}
}

func (b *logBuffer) WriteString(s string) (int, error) { return b.Write([]byte(s)) }
func (b *logBuffer) Len() int                          { return b.size }

func (b *logBuffer) withReader(read func(io.Reader) error) error {
	if b.err != nil {
		return b.err
	}
	if b.path == "" {
		return read(bytes.NewReader(b.memory.Bytes()))
	}
	f, err := os.Open(b.path)
	if err != nil {
		return fmt.Errorf("read logs: %w", err)
	}
	err = read(io.MultiReader(io.LimitReader(f, int64(b.fileSize)), bytes.NewReader(b.memory.Bytes())))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	runtime.KeepAlive(b)
	return err
}

func (b *logBuffer) replay(w io.Writer) error {
	return b.withReader(func(r io.Reader) error {
		_, err := io.Copy(w, r)
		return err
	})
}

func (b *logBuffer) contents() (string, error) {
	var out bytes.Buffer
	if err := b.replay(&out); err != nil {
		return "", err
	}
	return out.String(), nil
}

// String is used by diagnostics and existing raw-log assertions. Production
// output uses replay/contents so I/O failures are propagated, not hidden.
func (b *logBuffer) String() string {
	s, err := b.contents()
	if err != nil {
		return "[log storage error: " + err.Error() + "]"
	}
	return s
}

func (b *logBuffer) Reset() {
	if b.path != "" {
		b.cleanup.Stop()
		_ = os.Remove(b.path)
		b.path = ""
	}
	b.memory = bytes.Buffer{}
	b.size = 0
	b.fileSize = 0
	b.spillFailed = false
}
