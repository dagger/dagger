package imageload

import (
	"context"
	"fmt"
	"io"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
)

type TarballWriter func(ctx context.Context, name string, tarball io.Reader) error
type TarballReader func(ctx context.Context, name string, tarball io.Writer) error

type Loader struct {
	ID string

	// TarballWriter and TarballReader allow the backend to write and read tarballs
	// to and from the content store.
	TarballWriter TarballWriter
	TarballReader TarballReader

	// Stores are used to directly access a containerd backend (when available).
	// These are *significantly* faster when available.
	ContentStore content.Store
	ImagesStore  images.Store
	LeaseManager leases.Manager

	cleanup func() error
}

func (loader *Loader) Close() error {
	if loader.cleanup == nil {
		return nil
	}
	return loader.cleanup()
}

type Backend interface {
	Loader(context.Context) (*Loader, error)
}

var backends = map[string]Backend{}

func register(scheme string, backend Backend) {
	backends[scheme] = backend
}

func GetBackend(name string) (Backend, error) {
	backend, ok := backends[name]
	if !ok {
		return nil, fmt.Errorf("no backend for %q found", name)
	}
	return backend, nil
}
