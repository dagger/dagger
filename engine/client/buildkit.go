package client

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/drivers"
)

const (
	// TODO: deprecate in a future release
	envDaggerCloudCachetoken = "_EXPERIMENTAL_DAGGER_CACHESERVICE_TOKEN"
)

func newBuildkitClient(ctx context.Context, remote *url.URL, connector drivers.Connector) (_ *bkclient.Client, _ *bkclient.Info, rerr error) {
	backoffConfig := backoff.DefaultConfig
	backoffConfig.MaxDelay = 30 * time.Second
	opts := []bkclient.ClientOpt{
		bkclient.WithTracerProvider(otel.GetTracerProvider()), // TODO verify?
		bkclient.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return connector.Connect(ctx)
		}),
		bkclient.WithGRPCDialOption(grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoffConfig,
			MinConnectTimeout: 10 * time.Second,
		})),
	}

	c, err := bkclient.New(ctx, remote.String(), opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("buildkit client: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	info, err := c.Info(ctx)
	if err != nil {
		return nil, nil, err
	}

	if info.BuildkitVersion.Package != engine.Package {
		return nil, nil, fmt.Errorf("remote is not a valid dagger server (expected %q, got %q)", engine.Package, info.BuildkitVersion.Package)
	}

	return c, info, nil
}
