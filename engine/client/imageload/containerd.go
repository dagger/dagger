package imageload

import (
	"context"
	"os"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/defaults"
	"github.com/dagger/dagger/util/ctrns"
)

type Containerd struct{}

func init() {
	register("containerd", &Containerd{})
}

func (Containerd) ID() string {
	return "containerd"
}

func (loader Containerd) Loader(ctx context.Context) (_ *Loader, rerr error) {
	addr := defaults.DefaultAddress
	if v, ok := os.LookupEnv("CONTAINERD_ADDRESS"); ok {
		addr = v
	}

	c, err := containerd.New(addr)
	if err != nil {
		return nil, err
	}
	defer func() {
		if rerr != nil {
			c.Close()
		}
	}()

	ns := "default"
	return &Loader{
		ContentStore: ctrns.ContentWithNamespace(c.ContentStore(), ns),
		ImagesStore:  ctrns.ImageStoreWithNamespace(c.ImageService(), ns),
		LeaseManager: ctrns.LeasesWithNamespace(c.LeasesService(), ns),
		cleanup:      c.Close,
	}, nil
}
