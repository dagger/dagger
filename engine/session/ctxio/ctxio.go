// Package ctxio provides context-aware io.ReadCloser and io.WriteCloser implementations that handle cancellations during blocking read/write operations.
package ctxio

import (
	"context"
	"io"

	"golang.org/x/sync/errgroup"
)

// NewReader returns a context-aware io.ReadCloser that reads from the given reader r.
// Cancellation works even if the reader is blocked on read.
// Closing the returned io.ReadCloser will not close the underlying reader.
func NewReader(ctx context.Context, r io.Reader) io.ReadCloser {
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		done := make(chan struct{})

		go func() {
			_, err := io.Copy(pw, r)
			pw.CloseWithError(err)
			close(done)
		}()

		select {
		case <-ctx.Done():
			close(done)
			pw.CloseWithError(ctx.Err())
		case <-done:
		}
	}()

	return &reader{cancel: cancel, PipeReader: pr}
}

type reader struct {
	cancel context.CancelFunc
	*io.PipeReader
}

func (cr *reader) Close() error {
	cr.cancel()
	return cr.PipeReader.Close()
}

// NewWriter returns a context-aware io.WriteCloser that writes to the given writer w.
// Cancellation works even if the writer is blocked on write.
// Closing the returned io.WriteCloser will not close the underlying writer.
func NewWriter(ctx context.Context, w io.Writer) io.WriteCloser {
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		done := make(chan struct{})

		go func() {
			_, err := io.Copy(w, pr)
			pr.CloseWithError(err)
			close(done)
		}()

		select {
		case <-ctx.Done():
			close(done)
			pr.CloseWithError(ctx.Err())
		case <-done:
		}
	}()

	return &writer{cancel: cancel, PipeWriter: pw}
}

type writer struct {
	cancel context.CancelFunc
	*io.PipeWriter
}

func (cw *writer) Close() error {
	cw.cancel()
	return cw.PipeWriter.Close()
}

type readWriter struct {
	*io.PipeReader
	*io.PipeWriter
}

// NewReadWriter returns a context-aware io.ReadWriteCloser that reads and writes using the given ReadWriter rw.
// Cancellation works even if reads or writes are blocked.
// Closing the returned io.ReadWriteCloser will not close the underlying ReadWriter.
func NewReadWriter(ctx context.Context, rw io.ReadWriter) io.ReadWriteCloser {
	prr, prw := io.Pipe() // For reading
	pwr, pww := io.Pipe() // For writing
	g, _ := errgroup.WithContext(ctx)

	g.Go(func() error {
		_, err := io.Copy(prw, rw)
		prw.CloseWithError(err)
		return err
	})

	g.Go(func() error {
		_, err := io.Copy(rw, pwr)
		pwr.CloseWithError(err)
		return err
	})

	go func() {
		if err := g.Wait(); err != nil {
			prw.CloseWithError(err)
			pwr.CloseWithError(err)
		}
	}()

	return &readWriter{PipeReader: prr, PipeWriter: pww}
}

func (crw *readWriter) Close() error {
	err1 := crw.PipeReader.Close()
	err2 := crw.PipeWriter.Close()
	if err1 != nil {
		return err1
	}
	return err2
}
