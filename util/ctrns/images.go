package ctrns

import (
	"context"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

func ImageStoreWithNamespace(store images.Store, ns string) images.Store {
	return &chooseImageStore{
		store: store,
		choose: func(ctx context.Context) context.Context {
			return namespaces.WithNamespace(ctx, ns)
		},
	}
}

type chooseImageStore struct {
	store  images.Store
	choose func(ctx context.Context) context.Context
}

func (c *chooseImageStore) Get(ctx context.Context, name string) (images.Image, error) {
	ctx = c.choose(ctx)
	return c.store.Get(ctx, name)
}

func (c *chooseImageStore) List(ctx context.Context, filters ...string) ([]images.Image, error) {
	ctx = c.choose(ctx)
	return c.store.List(ctx, filters...)
}

func (c *chooseImageStore) Create(ctx context.Context, image images.Image) (images.Image, error) {
	ctx = c.choose(ctx)
	return c.store.Create(ctx, image)
}

func (c *chooseImageStore) Update(ctx context.Context, image images.Image, fieldpaths ...string) (images.Image, error) {
	ctx = c.choose(ctx)
	return c.store.Update(ctx, image, fieldpaths...)
}

func (c *chooseImageStore) Delete(ctx context.Context, name string, opts ...images.DeleteOpt) error {
	ctx = c.choose(ctx)
	return c.store.Delete(ctx, name, opts...)
}
