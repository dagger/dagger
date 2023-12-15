package client

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/client/drivers"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/tracing/detect"
	"github.com/vito/progrock"
	"go.opentelemetry.io/otel"
)

const (
	// TODO: deprecate in a future release
	envDaggerCloudCachetoken = "_EXPERIMENTAL_DAGGER_CACHESERVICE_TOKEN"
)

func newBuildkitClient(ctx context.Context, rec *progrock.VertexRecorder, remote *url.URL, userAgent string) (*bkclient.Client, *bkclient.Info, error) {
	driver, err := drivers.GetDriver(remote.Scheme)
	if err != nil {
		return nil, nil, err
	}

	var cloudToken string
	if v, ok := os.LookupEnv(drivers.EnvDaggerCloudToken); ok {
		cloudToken = v
	} else if _, ok := os.LookupEnv(envDaggerCloudCachetoken); ok {
		cloudToken = v
	}

	conn, err := driver.Connect(ctx, rec, remote, &drivers.DriverOpts{
		UserAgent:        userAgent,
		DaggerCloudToken: cloudToken,
		GPUSupport:       os.Getenv(drivers.EnvGPUSupport),
	})
	if err != nil {
		return nil, nil, err
	}

	opts := []bkclient.ClientOpt{
		bkclient.WithTracerProvider(otel.GetTracerProvider()),
	}
	var counter int64
	opts = append(opts, bkclient.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		if atomic.AddInt64(&counter, 1) > 1 {
			return nil, net.ErrClosed
		}
		return conn, nil
	}))

	exp, _, err := detect.Exporter()
	if err == nil {
		if td, ok := exp.(bkclient.TracerDelegate); ok {
			opts = append(opts, bkclient.WithTracerDelegate(td))
		}
	} else {
		fmt.Fprintln(rec.Stdout(), "failed to detect opentelemetry exporter: ", err)
	}

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
