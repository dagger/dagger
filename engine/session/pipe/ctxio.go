package pipe

import (
	"context"
	"io"
)

// newCtxReader returns a context-aware io.ReadCloser that reads from the given reader r.
// Cancellation works even if the reader is blocked on read.
// Closing the returned io.ReadCloser will not close the underlying reader.
func newCtxReader(ctx context.Context, r io.Reader) io.ReadCloser {
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

	return &ctxReader{PipeReader: pr, close: close}
}

type ctxReader struct {
	*io.PipeReader
	close func() error
}

func (cr *ctxReader) Close() error {
	return cr.close()
}
