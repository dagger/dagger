package snapshots

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// ChainContentError identifies an unusable supplied chain. Storage/lease and
// receiver failures are deliberately not annotated with this type.
type ChainContentError struct {
	Layer digest.Digest
	Stage string
	Err   error
}

func (e *ChainContentError) Error() string {
	return fmt.Sprintf("snapshot chain layer %s %s: %v", e.Layer, e.Stage, e.Err)
}
func (e *ChainContentError) Unwrap() error { return e.Err }
func chainError(ctx context.Context, enabled bool, desc ocispecs.Descriptor, stage string, err error) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if !enabled || err == nil {
		return err
	}
	var contentErr *ChainContentError
	if errors.As(err, &contentErr) {
		return err
	}
	return &ChainContentError{Layer: desc.Digest, Stage: stage, Err: err}
}

// Both wrappers retain one error, regardless of layer size. The content-store
// writer's errors take precedence over reader classification.
type chainCopyReader struct {
	content.ReaderAt
	err error
}

func (r *chainCopyReader) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(p, off)
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
	}
	return n, err
}

type chainCopyWriter struct {
	content.Writer
	err  error
	desc ocispecs.Descriptor
}

func (w *chainCopyWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err != nil {
		w.err = err
	}
	return n, err
}
func (w *chainCopyWriter) Status() (content.Status, error) {
	s, err := w.Writer.Status()
	w.err = err
	return s, err
}
func (w *chainCopyWriter) Commit(ctx context.Context, size int64, expected digest.Digest, opts ...content.Opt) error {
	if expected != "" && w.Writer.Digest() != expected {
		return chainError(ctx, true, w.desc, "copy", fmt.Errorf("layer checksum mismatch: expected %s, got %s", expected, w.Writer.Digest()))
	}
	err := w.Writer.Commit(ctx, size, expected, opts...)
	if err != nil {
		w.err = err
	}
	return err
}
func copyChainContent(ctx context.Context, writer content.Writer, reader content.ReaderAt, desc ocispecs.Descriptor) error {
	w := &chainCopyWriter{Writer: writer, desc: desc}
	r := &chainCopyReader{ReaderAt: reader}
	err := content.Copy(ctx, w, io.NewSectionReader(r, 0, reader.Size()), desc.Size, desc.Digest)
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if w.err != nil {
		return err
	}
	if err != nil && (r.err != nil || errors.Is(err, io.ErrUnexpectedEOF)) {
		return chainError(ctx, true, desc, "copy", err)
	}
	return err
}
