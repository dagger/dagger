package ctrns

import (
	"context"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func ContentStoreWithNamespace(store content.Store, ns string) content.Store {
	return &chooseContentStore{
		store: store,
		choose: func(ctx context.Context) context.Context {
			return namespaces.WithNamespace(ctx, ns)
		},
	}
}

func ContentStoreWithLease(store content.Store, leaseID string) content.Store {
	return &chooseContentStore{
		store: store,
		choose: func(ctx context.Context) context.Context {
			return leases.WithLease(ctx, leaseID)
		},
	}
}

type chooseContentStore struct {
	store  content.Store
	choose func(ctx context.Context) context.Context
}

func (cs *chooseContentStore) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	ctx = cs.choose(ctx)
	info, err := cs.store.Info(ctx, dgst)
	return info, errors.WithStack(err)
}

func (cs *chooseContentStore) Update(ctx context.Context, info content.Info, fieldpaths ...string) (content.Info, error) {
	ctx = cs.choose(ctx)
	info, err := cs.store.Update(ctx, info, fieldpaths...)
	return info, errors.WithStack(err)
}

func (cs *chooseContentStore) Walk(ctx context.Context, fn content.WalkFunc, fs ...string) error {
	ctx = cs.choose(ctx)
	return errors.WithStack(cs.store.Walk(ctx, fn, fs...))
}

func (cs *chooseContentStore) Delete(ctx context.Context, dgst digest.Digest) error {
	ctx = cs.choose(ctx)
	return errors.WithStack(cs.store.Delete(ctx, dgst))
}

func (cs *chooseContentStore) ListStatuses(ctx context.Context, fs ...string) ([]content.Status, error) {
	ctx = cs.choose(ctx)
	resp, err := cs.store.ListStatuses(ctx, fs...)
	return resp, errors.WithStack(err)
}

func (cs *chooseContentStore) Status(ctx context.Context, ref string) (content.Status, error) {
	ctx = cs.choose(ctx)
	st, err := cs.store.Status(ctx, ref)
	return st, errors.WithStack(err)
}

func (cs *chooseContentStore) Abort(ctx context.Context, ref string) error {
	ctx = cs.choose(ctx)
	return errors.WithStack(cs.store.Abort(ctx, ref))
}

func (cs *chooseContentStore) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	ctx = cs.choose(ctx)
	w, err := cs.store.Writer(ctx, opts...)
	return w, errors.WithStack(err)
}

func (cs *chooseContentStore) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	ctx = cs.choose(ctx)
	ra, err := cs.store.ReaderAt(ctx, desc)
	return ra, errors.WithStack(err)
}
