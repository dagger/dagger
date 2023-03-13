package engine

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/tracing/detect"
	"go.opentelemetry.io/otel"

	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the docker connection driver
	_ "github.com/moby/buildkit/client/connhelper/kubepod"         // import the kubernetes connection driver
	_ "github.com/moby/buildkit/client/connhelper/podmancontainer" // import the podman connection driver
)

// Client returns a buildkit client, whether privileged execs are enabled, or an error
func Client(ctx context.Context, remote *url.URL) (*bkclient.Client, bool, error) {
	buildkitdHost := remote.String()
	if remote.Scheme == DockerImageProvider {
		var err error
		buildkitdHost, err = dockerImageProvider(ctx, remote)
		if err != nil {
			return nil, false, err
		}
	}

	workerInfo, err := waitBuildkit(ctx, buildkitdHost)
	if err != nil {
		return nil, false, err
	}
	var privilegedExecEnabled bool
	if len(workerInfo) > 0 {
		for k, v := range workerInfo[0].Labels {
			// TODO:(sipsma) we set this custom label in the default engine config
			// toml. It's not the best solution but the only way to get this
			// information to the client right now.
			if k == "privilegedEnabled" && v == "true" {
				privilegedExecEnabled = true
				break
			}
		}
	}

	opts := []bkclient.ClientOpt{
		bkclient.WithFailFast(),
		bkclient.WithTracerProvider(otel.GetTracerProvider()),
	}

	exp, err := detect.Exporter()
	if err != nil {
		return nil, false, err
	}

	if td, ok := exp.(bkclient.TracerDelegate); ok {
		opts = append(opts, bkclient.WithTracerDelegate(td))
	}

	c, err := bkclient.New(ctx, buildkitdHost, opts...)
	if err != nil {
		return nil, false, fmt.Errorf("buildkit client: %w", err)
	}

	return c, privilegedExecEnabled, nil
}

// waitBuildkit waits for the buildkit daemon to be responsive.
func waitBuildkit(ctx context.Context, host string) ([]*bkclient.WorkerInfo, error) {
	c, err := bkclient.New(ctx, host)
	if err != nil {
		return nil, err
	}

	// FIXME Does output "failed to wait: signal: broken pipe"
	defer c.Close()

	// Try to connect every 100ms up to 1800 times (3 minutes total)
	// NOTE: the long timeout accounts for startup time of the engine when
	// it needs to synchronize cache state.
	const (
		retryPeriod   = 100 * time.Millisecond
		retryAttempts = 1800
	)

	var workerInfo []*bkclient.WorkerInfo
	for retry := 0; retry < retryAttempts; retry++ {
		workerInfo, err = c.ListWorkers(ctx)
		if err == nil {
			return workerInfo, nil
		}
		time.Sleep(retryPeriod)
	}

	listWorkerError := strings.ReplaceAll(err.Error(), "\\n", "")
	return nil, fmt.Errorf("buildkit failed to respond: %s", listWorkerError)
}
