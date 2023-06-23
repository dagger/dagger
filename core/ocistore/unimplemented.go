package ocistore

import (
	"context"

	"github.com/containerd/containerd/content"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// unimplementedStore is a content.Store intended to be embedded in a real
// implementation that overrides its methods.
type unimplementedStore struct{}

var _ content.Store = unimplementedStore{}

func (unimplementedStore) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	return content.Info{}, errors.Errorf("contentstore.Info not implemented")
}

func (unimplementedStore) Update(ctx context.Context, info content.Info, fieldpaths ...string) (content.Info, error) {
	return content.Info{}, errors.Errorf("contentstore.Update usage is forbidden")
}

func (unimplementedStore) Walk(ctx context.Context, fn content.WalkFunc, filters ...string) error {
	return errors.Errorf("contentstore.Walk not implemented")
}

func (unimplementedStore) Delete(ctx context.Context, dgst digest.Digest) error {
	return errors.Errorf("contentstore.Delete not implemented")
}

func (unimplementedStore) Status(ctx context.Context, ref string) (content.Status, error) {
	return content.Status{}, errors.Errorf("contentstore.Status not implemented")
}

func (unimplementedStore) ListStatuses(ctx context.Context, filters ...string) ([]content.Status, error) {
	return nil, errors.Errorf("contentstore.ListStatuses not implemented")
}

func (unimplementedStore) Abort(ctx context.Context, ref string) error {
	return errors.Errorf("contentstore.Abort not implemented")
}

func (unimplementedStore) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	return nil, errors.Errorf("contentstore.ReaderAt not implemented")
}

func (unimplementedStore) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	return nil, errors.Errorf("contentstore.Writer not implemented")
}
