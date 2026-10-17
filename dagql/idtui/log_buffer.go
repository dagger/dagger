package idtui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
)

// logBuffer keeps small logs cheap and spills larger logs to a private temporary
// file. Files are opened only while reading/writing: thousands of spans must not
// consume thousands of file descriptors. Callers serialize access.
const logBufferMemoryLimit = 32 * 1024

type logBuffer struct {
	memory   bytes.Buffer
	path     string
	size     int
	lastByte byte
	err      error
	cleanup  runtime.Cleanup
}

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
	if b.path == "" && b.size+len(p) <= logBufferMemoryLimit {
		n, err := b.memory.Write(p)
		b.size += n
		return n, err
	}
	if b.path == "" {
		f, err := os.CreateTemp("", "dagger-logs-*")
		if err != nil {
			return 0, fmt.Errorf("spool logs: %w", err)
		}
		name := f.Name()
		_, err = f.Write(b.memory.Bytes())
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(name)
			return 0, fmt.Errorf("spool logs: %w", err)
		}
		b.path = name
		b.cleanup = runtime.AddCleanup(b, func(path string) { _ = os.Remove(path) }, name)
		b.memory = bytes.Buffer{}
	}
	f, err := os.OpenFile(b.path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return 0, fmt.Errorf("append logs: %w", err)
	}
	n, err = f.Write(p)
	b.size += n
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	runtime.KeepAlive(b)
	return n, err
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
	err = read(f)
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
}
