package client

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/dagger/dagger/engine"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/tracing/detect"
	"github.com/vito/progrock"
	"go.opentelemetry.io/otel"

	// load connection helpers
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer"
	_ "github.com/moby/buildkit/client/connhelper/kubepod"
	_ "github.com/moby/buildkit/client/connhelper/podmancontainer"
	_ "github.com/moby/buildkit/client/connhelper/ssh"
)

func newBuildkitClient(ctx context.Context, remote *url.URL, userAgent string, loader *progrock.VertexRecorder) (*bkclient.Client, *bkclient.Info, error) {
	buildkitdHost := remote.String()
	if remote.Scheme == DockerImageProvider {
		var err error
		buildkitdHost, err = dockerImageProvider(ctx, remote, userAgent)
		if err != nil {
			return nil, nil, err
		}
	}

	opts := []bkclient.ClientOpt{
		bkclient.WithFailFast(),
		bkclient.WithTracerProvider(otel.GetTracerProvider()),
	}

	exp, err := detect.Exporter()
	if err == nil {
		if td, ok := exp.(bkclient.TracerDelegate); ok {
			opts = append(opts, bkclient.WithTracerDelegate(td))
		}
	} else {
		fmt.Fprintln(loader.Stdout(), "failed to detect opentelemetry exporter: ", err)
	}

	c, err := bkclient.New(ctx, buildkitdHost, opts...)
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
