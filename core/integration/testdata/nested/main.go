package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx := context.Background()

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		fatal(err)
	}
	defer c.Close()

	mode, depthStr, svcURLs := os.Args[1], os.Args[2], os.Args[3:]

	depth, err := strconv.Atoi(depthStr)
	if err != nil {
		fatal(err)
	}

	if depth > 0 {
		weHaveToGoDeeper(ctx, c, depth, mode, svcURLs)
		return
	}

	results := make(chan string, len(svcURLs))

	eg := new(errgroup.Group)
	for _, u := range svcURLs {
		u := u
		eg.Go(func() error {
			out, err := fetch(ctx, c, mode, u)
			if err != nil {
				return err
			}

			results <- out
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		fatal(err)
	}

	var last string
	for i := 0; i < cap(results); i++ {
		out := <-results

		if last == "" {
			last = out
			continue
		}

		if last != out {
			fatal("expected same response: " + last + " != " + out)
		}
	}

	fmt.Print(last)
}

func weHaveToGoDeeper(ctx context.Context, c *dagger.Client, depth int, mode string, svcURLs []string) {
	code := c.Host().Directory(".", dagger.HostDirectoryOpts{
		Include: []string{"core/integration/testdata/nested/", "sdk/go/", "go.mod", "go.sum"},
	})

	previous := svcURLs[len(svcURLs)-1]
	mirrorSvc, mirrorURL := mirror(ctx, c, mode, previous)

	args := []string{
		"go", "run", "./core/integration/testdata/nested/",
		mode, strconv.Itoa(depth - 1),
	}
	args = append(args, svcURLs...)
	args = append(args, mirrorURL)

	out, err := c.Container().
		From("golang:1.20.6-alpine").
		WithMountedCache("/go/pkg/mod", c.CacheVolume("go-mod")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithMountedCache("/go/build-cache", c.CacheVolume("go-build")).
		WithEnvVariable("GOCACHE", "/go/build-cache").
		WithMountedDirectory("/src", code).
		WithWorkdir("/src").
		WithEnvVariable("NOW", identity.NewID()).
		WithExec([]string{"cat", "/etc/resolv.conf"}).
		WithServiceBinding("mirror", mirrorSvc).
		WithExec(args, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Stdout(ctx)
	if err != nil {
		fatal(err)
	}

	fmt.Print(out)
}

func mirror(ctx context.Context, c *dagger.Client, mode, svcURL string) (*dagger.Container, string) {
	srv := c.Container().
		From("python:alpine").
		WithWorkdir("/srv/www")

	switch mode {
	case "exec":
		srv = srv.WithExec([]string{"wget", svcURL})
	case "http":
		srv = srv.WithMountedFile("/srv/www/index.html", c.HTTP(svcURL))
	}

	srv = srv.
		WithExposedPort(8000).
		WithExec([]string{"python", "-m", "http.server"})

	httpURL, err := srv.Endpoint(ctx, dagger.ContainerEndpointOpts{
		Scheme: "http",
	})
	if err != nil {
		fatal(err)
	}

	return srv, httpURL
}

func fetch(ctx context.Context, c *dagger.Client, mode, svcURL string) (string, error) {
	switch mode {
	case "exec":
		return c.Container().
			From("alpine:3.16.2").
			WithEnvVariable("NOW", identity.NewID()).
			WithExec([]string{"cat", "/etc/resolv.conf"}).
			WithExec([]string{"wget", "-O-", svcURL}).
			Stdout(ctx)
	case "http":
		return c.HTTP(svcURL).Contents(ctx)
	default:
		return "", fmt.Errorf("unknown mode: %q", mode)
	}
}

func fatal(err any) {
	fmt.Fprintf(os.Stderr, "\x1b[31m%s\x1b[0m\n", err)
	os.Exit(1)
}
