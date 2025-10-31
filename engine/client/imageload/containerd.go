package imageload

import (
	"context"
	"os"

	"dagger.io/dagger/telemetry"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/dagger/dagger/util/ctrns"
	"go.opentelemetry.io/otel"
)

type Containerd struct{}

func init() {
	register("containerd", &Containerd{})
}

func (loader Containerd) Loader(ctx context.Context) (_ *Loader, rerr error) {
	_, span := otel.Tracer("").Start(ctx, "dial containerd")
	defer telemetry.End(span, func() error { return rerr })

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
