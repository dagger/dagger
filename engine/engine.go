package engine

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/cloak/core"
	"github.com/dagger/cloak/extension"
	"github.com/dagger/cloak/router"
	"github.com/dagger/cloak/sdk/go/dagger"
	"github.com/dagger/cloak/secret"
	bkclient "github.com/moby/buildkit/client"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/util/progress/progressui"
	"github.com/moby/buildkit/util/tracing/detect"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"go.opentelemetry.io/otel"
	"golang.org/x/sync/errgroup"

	_ "github.com/moby/buildkit/client/connhelper/dockercontainer" // import the docker connection driver
)

type Config struct {
	LocalDirs map[string]string
	DevServer int
}

type StartCallback func(ctx context.Context) error

func Start(ctx context.Context, startOpts *Config, fn StartCallback) error {
	if startOpts == nil {
		startOpts = &Config{}
	}

	opts := []bkclient.ClientOpt{
		bkclient.WithFailFast(),
		bkclient.WithTracerProvider(otel.GetTracerProvider()),
	}

	exp, err := detect.Exporter()
	if err != nil {
		return err
	}

	if td, ok := exp.(bkclient.TracerDelegate); ok {
		opts = append(opts, bkclient.WithTracerDelegate(td))
	}

	c, err := bkclient.New(ctx, "docker-container://dagger-buildkitd", opts...)
	if err != nil {
		return err
	}

	platform, err := detectPlatform(ctx, c)
	if err != nil {
		return err
	}

	router := router.New()
	secretStore := secret.NewStore()
	solveOpts := bkclient.SolveOpt{
		Session: []session.Attachable{
			secretsprovider.NewSecretProvider(secretStore),
			extension.NewAPIProxy(router),
		},
	}
	solveOpts.LocalDirs = startOpts.LocalDirs

	ch := make(chan *bkclient.SolveStatus)
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		var err error
		_, err = c.Build(ctx, solveOpts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
			coreAPI, err := core.New(router, secretStore, gw, *platform)
			if err != nil {
				return nil, err
			}
			if err := router.Add(coreAPI); err != nil {
				return nil, err
			}

			ctx = withInMemoryAPIClient(ctx, router)

			if fn == nil {
				return nil, nil
			}

			if err := fn(ctx); err != nil {
				return nil, err
			}

			if startOpts.DevServer != 0 {
				fmt.Fprintf(os.Stderr, "==> dev server listening on http://localhost:%d", startOpts.DevServer)
				return nil, http.ListenAndServe(fmt.Sprintf(":%d", startOpts.DevServer), router)
			}

			return bkgw.NewResult(), nil
		}, ch)
		return err
	})
	eg.Go(func() error {
		warn, err := progressui.DisplaySolveStatus(context.TODO(), "", nil, os.Stderr, ch)
		for _, w := range warn {
			fmt.Fprintf(os.Stderr, "=> %s\n", w.Short)
		}
		return err
	})
	if err := eg.Wait(); err != nil {
		return err
	}
	return nil
}

func withInMemoryAPIClient(ctx context.Context, router *router.Router) context.Context {
	return dagger.WithHTTPClient(ctx, &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				// TODO: not efficient, but whatever
				serverConn, clientConn := net.Pipe()

				go func() {
					_ = router.ServeConn(serverConn)
				}()

				return clientConn, nil
			},
		},
	})
}

func detectPlatform(ctx context.Context, c *bkclient.Client) (*specs.Platform, error) {
	w, err := c.ListWorkers(ctx)
	if err != nil {
		return nil, fmt.Errorf("error detecting platform %w", err)
	}

	if len(w) > 0 && len(w[0].Platforms) > 0 {
		dPlatform := w[0].Platforms[0]
		return &dPlatform, nil
	}
	defaultPlatform := platforms.DefaultSpec()
	return &defaultPlatform, nil
}
