package client

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/drivers"
	"github.com/dagger/dagger/telemetry"
	bkclient "github.com/moby/buildkit/client"
	"go.opentelemetry.io/otel"
)

const (
	// TODO: deprecate in a future release
	envDaggerCloudCachetoken = "_EXPERIMENTAL_DAGGER_CACHESERVICE_TOKEN"
)

func newBuildkitClient(ctx context.Context, remote *url.URL, connector drivers.Connector) (_ *bkclient.Client, _ *bkclient.Info, rerr error) {
	opts := []bkclient.ClientOpt{
		// TODO verify?
		bkclient.WithTracerProvider(otel.GetTracerProvider()),
	}
	opts = append(opts, bkclient.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return connector.Connect(ctx)
	}))

	ctx, span := Tracer().Start(ctx, "starting engine")
	defer telemetry.End(span, func() error { return rerr })

	c, err := bkclient.New(ctx, remote.String(), opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("buildkit client: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := c.Wait(ctx); err != nil {
		return nil, nil, err
	}

	info, err := c.Info(ctx)
	if err != nil {
		return nil, nil, err
	}

	if info.BuildkitVersion.Package != engine.Package {
		return nil, nil, fmt.Errorf("remote is not a valid dagger server (expected %q, got %q)", engine.Package, info.BuildkitVersion.Package)
	}

	return c, info, nil
}
