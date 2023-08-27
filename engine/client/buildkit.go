package client

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/tracing/detect"
	"go.opentelemetry.io/otel"

	// load connection helpers
	_ "github.com/moby/buildkit/client/connhelper/dockercontainer"
	_ "github.com/moby/buildkit/client/connhelper/kubepod"
	_ "github.com/moby/buildkit/client/connhelper/podmancontainer"
	_ "github.com/moby/buildkit/client/connhelper/ssh"
)

func newBuildkitClient(ctx context.Context, remote *url.URL, userAgent string) (*bkclient.Client, error) {
	buildkitdHost := remote.String()
	if remote.Scheme == DockerImageProvider {
		var err error
		buildkitdHost, err = dockerImageProvider(ctx, remote, userAgent)
		if err != nil {
			return nil, err
		}
	}

	_, err := waitBuildkit(ctx, buildkitdHost)
	if err != nil {
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
// TODO: there's a builtin method for this now
func waitBuildkit(ctx context.Context, host string) ([]*bkclient.WorkerInfo, error) {
	var c *bkclient.Client
	var workerInfo []*bkclient.WorkerInfo
	var err error

	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 100 * time.Millisecond

	// NOTE: the long timeout accounts for startup time of the engine when
	// it needs to synchronize cache state.
	connectRetryCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	err = backoff.Retry(func() error {
		ctx, cancel := context.WithTimeout(connectRetryCtx, bo.NextBackOff())
		defer cancel()

		c, err = bkclient.New(ctx, host, bkclient.WithFailFast())
		if err != nil {
			return err
		}

		// FIXME Does output "failed to wait: signal: broken pipe"
		defer c.Close()

		workerInfo, err = c.ListWorkers(ctx)
		if err != nil {
			listWorkerError := strings.ReplaceAll(err.Error(), "\\n", "")
			return fmt.Errorf("buildkit failed to respond: %s", listWorkerError)
		}

		return nil
	}, backoff.WithContext(bo, connectRetryCtx))
	if err != nil {
		return nil, fmt.Errorf("buildkit failed to respond: %w", err)
	}

	return workerInfo, nil
}
