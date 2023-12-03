package daggertest

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func Connect(t testing.TB, opts ...dagger.ClientOpt) (*dagger.Client, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	opts = append([]dagger.ClientOpt{
		dagger.WithLogOutput(newTWriter(t)),
	}, opts...)

	client, err := dagger.Connect(ctx, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	return client, ctx
}

// tWriter is a writer that writes to testing.T
type tWriter struct {
	t   testing.TB
	buf bytes.Buffer
	mu  sync.Mutex
}

// newTWriter creates a new TWriter
func newTWriter(t testing.TB) *tWriter {
	tw := &tWriter{t: t}
	t.Cleanup(tw.flush)
	return tw
}

// Write writes data to the testing.T
func (tw *tWriter) Write(p []byte) (n int, err error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()

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

func (tw *tWriter) flush() {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	tw.t.Log(tw.buf.String())
}
