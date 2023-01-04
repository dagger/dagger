package engine

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/tracing/detect"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"

	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the docker connection driver
	_ "github.com/moby/buildkit/client/connhelper/kubepod"         // import the kubernetes connection driver
	_ "github.com/moby/buildkit/client/connhelper/podmancontainer" // import the podman connection driver
)

func init() {
	// Disable logrus output, which only comes from the docker
	// commandconn library that is used by buildkit's connhelper
	// and prints unneeded warning logs.
	logrus.StandardLogger().SetOutput(io.Discard)
}

func Client(ctx context.Context, remote *url.URL) (*bkclient.Client, error) {
	buildkitdHost := remote.String()
	if remote.Scheme == DockerImageProvider {
		var err error
		buildkitdHost, err = dockerImageProvider(ctx, remote)
		if err != nil {
			return nil, err
		}
	}

	if err := waitBuildkit(ctx, buildkitdHost); err != nil {
		return nil, err
	}

	opts := []bkclient.ClientOpt{
		bkclient.WithFailFast(),
		bkclient.WithTracerProvider(otel.GetTracerProvider()),
	}

	exp, err := detect.Exporter()
	if err != nil {
		return nil, err
	}

	if td, ok := exp.(bkclient.TracerDelegate); ok {
		opts = append(opts, bkclient.WithTracerDelegate(td))
	}

	c, err := bkclient.New(ctx, buildkitdHost, opts...)
	if err != nil {
		return nil, fmt.Errorf("buildkit client: %w", err)
	}
	return c, nil
}

// waitBuildkit waits for the buildkit daemon to be responsive.
func waitBuildkit(ctx context.Context, host string) error {
	c, err := bkclient.New(ctx, host)
	if err != nil {
		return err
	}

	// FIXME Does output "failed to wait: signal: broken pipe"
	defer c.Close()

	// Try to connect every 100ms up to 100 times (10 seconds total)
	const (
		retryPeriod   = 100 * time.Millisecond
		retryAttempts = 100
	)

	for retry := 0; retry < retryAttempts; retry++ {
		_, err = c.ListWorkers(ctx)
		if err == nil {
			return nil
		}
		time.Sleep(retryPeriod)
	}

	listWorkerError := strings.ReplaceAll(err.Error(), "\\n", "")
	return fmt.Errorf("buildkit failed to respond: %s", listWorkerError)
}
