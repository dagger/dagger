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

const (
	PrivilegedExecLabel = "privilegedEnabled"
	EngineNameLabel     = "engineName"
)

type Client struct {
	BuildkitClient        *bkclient.Client
	PrivilegedExecEnabled bool
	EngineName            string
}

// Client returns a buildkit client, whether privileged execs are enabled, or an error
func NewClient(ctx context.Context, remote *url.URL, userAgent string) (*Client, error) {
	buildkitdHost := remote.String()
	if remote.Scheme == DockerImageProvider {
		var err error
		buildkitdHost, err = dockerImageProvider(ctx, remote, userAgent)
		if err != nil {
			return nil, err
		}
	}

	workerInfo, err := waitBuildkit(ctx, buildkitdHost)
	if err != nil {
		return nil, err
	}
	var privilegedExecEnabled bool
	var engineName string
	if len(workerInfo) > 0 {
		for k, v := range workerInfo[0].Labels {
			// TODO:(sipsma) we set these custom labels in the engine's worker initializer
			// It's not the best solution but the only way to get this
			// information to the client right now.
			switch k {
			case PrivilegedExecLabel:
				if v == "true" {
					privilegedExecEnabled = true
				}
			case EngineNameLabel:
				engineName = v
			}
		}
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

	return &Client{
		BuildkitClient:        c,
		PrivilegedExecEnabled: privilegedExecEnabled,
		EngineName:            engineName,
	}, nil
}

// waitBuildkit waits for the buildkit daemon to be responsive.
func waitBuildkit(ctx context.Context, host string) ([]*bkclient.WorkerInfo, error) {
	// Try to connect every 100ms up to 1800 times (3 minutes total)
	// NOTE: the long timeout accounts for startup time of the engine when
	// it needs to synchronize cache state.
	const (
		retryPeriod   = 100 * time.Millisecond
		retryAttempts = 1800
	)

	var c *bkclient.Client
	var err error

	for retry := 0; retry < retryAttempts; retry++ {
		c, err = bkclient.New(ctx, host)
		if err == nil {
			break
		}
		time.Sleep(retryPeriod)
	}

	// FIXME Does output "failed to wait: signal: broken pipe"
	defer c.Close()

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
