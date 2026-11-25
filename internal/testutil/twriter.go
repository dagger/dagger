package testutil

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

// tWriter is a writer that writes to testing.T
type tWriter struct {
	t   testing.TB
	buf bytes.Buffer
	mu  sync.Mutex
	// done is set once the test's cleanup has run. Logging through t after
	// that point panics and takes the whole test binary down, so late output
	// is redirected to stderr instead.
	done bool
}

// NewTWriter creates a new TWriter
func NewTWriter(t testing.TB) io.Writer {
	tw := &tWriter{t: t}
	t.Cleanup(tw.finish)
	return tw
}

// Write writes data to the testing.T
func (tw *tWriter) Write(p []byte) (n int, err error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	if tw.done {
		// A process whose output is bound to this test outlived it, typically
		// a client session that was never closed. Keep its output visible
		// without aborting every other test in the binary.
		fmt.Fprintf(os.Stderr, "[late output after %s completed] %s", tw.t.Name(), p)
		if !bytes.HasSuffix(p, []byte("\n")) {
			fmt.Fprintln(os.Stderr)
		}
		return len(p), nil
	}

	tw.t.Helper()

	if n, err = tw.buf.Write(p); err != nil {
		return n, err
	}

	for {
		line, err := tw.buf.ReadBytes('\n')
		if err == io.EOF {
			// If we've reached the end of the buffer, write it back, because it doesn't have a newline
			tw.buf.Write(line)
			break
		}
		if err != nil {
			return n, err
		}

		tw.t.Log(strings.TrimSuffix(string(line), "\n"))
	}
	return n, nil
}

// finish flushes any partial line and stops routing output through the test.
func (tw *tWriter) finish() {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.buf.Len() > 0 {
		tw.t.Log(tw.buf.String())
		tw.buf.Reset()
	}
	tw.done = true
}

// ClientLogOutput is the log output for a test's Dagger client. On the host
// it streams into the test's output, since nothing else shows the client's
// progress. Inside Dagger, as engine-dev runs the tests, the TUI and trace
// already show it as the test's spans, so it's discarded: copying it into
// every test's log only clutters the test spans, and makes each client's
// session render progress for it.
func ClientLogOutput(t testing.TB) io.Writer {
	if InDagger() {
		return io.Discard
	}
	return NewTWriter(t)
}

// InDagger reports whether the tests run inside Dagger rather than on the
// host. engine-dev unsets DAGGER_SESSION_PORT so that the tests' clients
// reach the dev engine, so it marks the tests with _DAGGER_TESTS_IN_DAGGER.
func InDagger() bool {
	if _, nested := os.LookupEnv("DAGGER_SESSION_PORT"); nested {
		return true
	}
	_, ok := os.LookupEnv("_DAGGER_TESTS_IN_DAGGER")
	return ok
}
