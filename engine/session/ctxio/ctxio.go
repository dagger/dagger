// Package ctxio provides context-aware io.ReadCloser and io.WriteCloser implementations that handle cancellations during blocking read/write operations.
package ctxio

import (
	"context"
	"io"
)

// NewReader returns a context-aware io.ReadCloser that reads from the given reader r.
// Cancellation works even if the reader is blocked on read.
// Closing the returned io.ReadCloser will not close the underlying reader.
func NewReader(ctx context.Context, r io.Reader) io.ReadCloser {
	pr, pw := io.Pipe()
	close := func() error {
		errR, errW := pr.Close(), pw.Close()
		if errR != nil {
			return errR
		}
		return errW
	}

	go func() {
		defer close()
		io.Copy(pw, r)
	}()

	go func() {
		defer close()
		<-ctx.Done()
	}()

	return &reader{PipeReader: pr, close: close}
}

type reader struct {
	*io.PipeReader
	close func() error
}

func (cr *reader) Close() error {
	return cr.close()
}
