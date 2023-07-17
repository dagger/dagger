package client

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/dagger/dagger/engine"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/tracing/detect"
	"go.opentelemetry.io/otel"
	// TODO: re-enable once upstream supports --addr
	// _ "github.com/moby/buildkit/client/connhelper/dockercontainer"
	// _ "github.com/moby/buildkit/client/connhelper/kubepod"
	// _ "github.com/moby/buildkit/client/connhelper/podmancontainer"
	// _ "github.com/moby/buildkit/client/connhelper/ssh"
)

type bkClient struct {
	*bkclient.Client
	PrivilegedExecEnabled   bool
	EngineName              string
	DaggerFrontendSessionID string
}

func newBuildkitClient(ctx context.Context, remote *url.URL, userAgent string) (*bkClient, error) {
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
	var daggerFrontendSessionID string
	if len(workerInfo) > 0 {
		for k, v := range workerInfo[0].Labels {
			// TODO:(sipsma) we set these custom labels in the engine's worker initializer
			// It's not the best solution but the only way to get this
			// information to the client right now.
			switch k {
			case engine.PrivilegedExecLabel:
				if v == "true" {
					privilegedExecEnabled = true
				}
			case engine.EngineNameLabel:
				engineName = v
			case engine.DaggerFrontendSessionIDLabel:
				daggerFrontendSessionID = v
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

	return &bkClient{
		Client:                  c,
		PrivilegedExecEnabled:   privilegedExecEnabled,
		EngineName:              engineName,
		DaggerFrontendSessionID: daggerFrontendSessionID,
	}, nil
}

// waitBuildkit waits for the buildkit daemon to be responsive.
func waitBuildkit(ctx context.Context, host string) ([]*bkclient.WorkerInfo, error) {
	// Try to connect every 100ms up to 1800 times (3 minutes total)
	// NOTE: the long timeout accounts for startup time of the engine when
	// it needs to synchronize cache state.
	const (
		retryPeriod   = 100 * time.Millisecond
		retryAttempts = 6000
	)

	var c *bkclient.Client
	var err error

	for retry := 0; retry < retryAttempts; retry++ {
		c, err = bkclient.New(ctx, host, bkclient.WithFailFast())
		if err == nil {
			break
		}
		time.Sleep(retryPeriod)
	}

	if err != nil {
		return nil, fmt.Errorf("buildkit failed to respond: %w", err)
	}

	if c == nil {
		return nil, fmt.Errorf("buildkit failed to respond")
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
