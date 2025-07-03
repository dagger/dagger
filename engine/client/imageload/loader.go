package imageload

import (
	"context"
	"fmt"
	"io"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/leases"
)

type TarballLoader func(ctx context.Context, name string, tarball io.Reader) error

type Loader struct {
	ID string

	TarballLoader TarballLoader

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
	ID() string
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
