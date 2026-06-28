package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (*Test) PublishEndpoint(ctx context.Context) (string, error) {
	return publish(ctx, 0, true)
}

func (*Test) PublishFixed(ctx context.Context, frontend int) (string, error) {
	return publish(ctx, frontend, false)
}

func (*Test) PublishStartStop(ctx context.Context) (string, error) {
	svc := publishService(0, true)
	started, err := svc.Start(ctx)
	if err != nil {
		return "", err
	}
	if _, err := started.Stop(ctx, dagger.ServiceStopOpts{Kill: true}); err != nil {
		return "", err
	}
	return "ok", nil
}

func publish(ctx context.Context, frontend int, random bool) (string, error) {
	svc := publishService(frontend, random)

	return svc.Endpoint(ctx, dagger.ServiceEndpointOpts{Scheme: "http"})
}

func publishService(frontend int, random bool) *dagger.Service {
	return dag.Container().
		From("python").
		WithMountedDirectory(
			"/srv/www",
			dag.Directory().WithNewFile("index.html", "published from module"),
		).
		WithWorkdir("/srv/www").
		WithExposedPort(8000).
		WithDefaultArgs([]string{"python", "-m", "http.server", "8000"}).
		AsService().
		Publish(dagger.ServicePublishOpts{
			Ports: []dagger.PortForward{{
				Backend:  8000,
				Frontend: frontend,
			}},
			Random: random,
		})
}
