// ctrns provides utilities for containerd resources that are pre-namespaced
// (instead of reading them from the context)
package ctrns

import (
	"context"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/namespaces"
	containerdsnapshotter "github.com/moby/buildkit/snapshot/containerd"
	"github.com/moby/buildkit/util/leaseutil"
)

type ContentStoreNamespaced = containerdsnapshotter.Store

func ContentWithNamespace(store content.Store, ns string) *ContentStoreNamespaced {
	return containerdsnapshotter.NewContentStore(store, ns)
}

type LeasesManagerNamespace = leaseutil.Manager

func LeasesWithNamespace(leases leases.Manager, ns string) *LeasesManagerNamespace {
	return leaseutil.WithNamespace(leases, ns)
}

type ImageStoreNamespaced struct {
	ns string
	images.Store
}

func ImagesWithNamespace(store images.Store, ns string) *ImageStoreNamespaced {
	return &ImageStoreNamespaced{ns, store}
}

func (c *ImageStoreNamespaced) Namespace() string {
	return c.ns
}

func (c *ImageStoreNamespaced) WithNamespace(ns string) *ImageStoreNamespaced {
	return ImagesWithNamespace(c.Store, ns)
}

func (c *ImageStoreNamespaced) Get(ctx context.Context, name string) (images.Image, error) {
	ctx = namespaces.WithNamespace(ctx, c.ns)
	return c.Store.Get(ctx, name)
}

func (c *ImageStoreNamespaced) List(ctx context.Context, filters ...string) ([]images.Image, error) {
	ctx = namespaces.WithNamespace(ctx, c.ns)
	return c.Store.List(ctx, filters...)
}

func (c *ImageStoreNamespaced) Create(ctx context.Context, image images.Image) (images.Image, error) {
	ctx = namespaces.WithNamespace(ctx, c.ns)
	return c.Store.Create(ctx, image)
}

func (c *ImageStoreNamespaced) Update(ctx context.Context, image images.Image, fieldpaths ...string) (images.Image, error) {
	ctx = namespaces.WithNamespace(ctx, c.ns)
	return c.Store.Update(ctx, image, fieldpaths...)
}

func (c *ImageStoreNamespaced) Delete(ctx context.Context, name string, opts ...images.DeleteOpt) error {
	ctx = namespaces.WithNamespace(ctx, c.ns)
	return c.Store.Delete(ctx, name, opts...)
}
