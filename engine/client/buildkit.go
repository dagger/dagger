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

func newBuildkitClient(ctx context.Context, rec *progrock.VertexRecorder, remote *url.URL, userAgent string) (*bkclient.Client, *bkclient.Info, error) {
	var c *bkclient.Client
	var err error
	switch remote.Scheme {
	case DockerImageProvider:
		c, err = buildkitConnectDocker(ctx, rec, remote, userAgent)
	default:
		task := rec.Task("starting engine")
		c, err = buildkitConnectDefault(ctx, rec, remote)
		task.Done(err)
	}
	if err != nil {
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

func buildkitConnectDefault(ctx context.Context, rec *progrock.VertexRecorder, remote *url.URL) (*bkclient.Client, error) {
	opts := []bkclient.ClientOpt{
		bkclient.WithTracerProvider(otel.GetTracerProvider()),
	}

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
		return nil, fmt.Errorf("buildkit client: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := c.Wait(ctx); err != nil {
		return nil, err
	}

	return c, nil
}
