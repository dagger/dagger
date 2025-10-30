package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strconv"

	"dagger.io/dagger"
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

	if depth > 1 {
		weHaveToGoDeeper(ctx, c, depth, mode, svcURLs)
		return
	}

	results := make(chan string, len(svcURLs))

	eg := new(errgroup.Group)
	for _, u := range svcURLs {
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
		Include: []string{"core/integration/testdata/nested-c2c/", "sdk/go/", "go.mod", "go.sum"},
	})

	previous := svcURLs[len(svcURLs)-1]
	mirrorSvc, mirrorURL := mirror(ctx, c, mode, previous)

	args := []string{
		"go", "run", "./core/integration/testdata/nested-c2c/",
		mode, strconv.Itoa(depth - 1),
	}
	args = append(args, svcURLs...)
	args = append(args, mirrorURL)

	out, err := c.Container().
		From("golang:1.25.3-alpine").
		WithMountedCache("/go/pkg/mod", c.CacheVolume("go-mod")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithMountedCache("/go/build-cache", c.CacheVolume("go-build")).
		WithEnvVariable("GOCACHE", "/go/build-cache").
		WithMountedDirectory("/src", code).
		WithWorkdir("/src").
		WithEnvVariable("NOW", rand.Text()).
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

func mirror(ctx context.Context, c *dagger.Client, mode, svcURL string) (*dagger.Service, string) {
	switch mode {
	case "exec":
		return httpService(ctx, c,
			c.Container().
				From("alpine:3.16.2").
				WithWorkdir("/srv/www").
				WithExec([]string{"wget", svcURL}).
				Directory("."))
	case "http":
		return httpService(ctx, c,
			c.Directory().WithFile("index.html", c.HTTP(svcURL)))
	case "git":
		return gitService(ctx, c, c.Git(svcURL).Branch("main").Tree(dagger.GitRefTreeOpts{DiscardGitDir: true}))
	default:
		fatal(fmt.Errorf("unknown mode: %q", mode))
		return nil, ""
	}
}

func fetch(ctx context.Context, c *dagger.Client, mode, svcURL string) (string, error) {
	switch mode {
	case "exec":
		return c.Container().
			From("alpine:3.16.2").
			WithEnvVariable("NOW", rand.Text()).
			WithExec([]string{"cat", "/etc/resolv.conf"}).
			WithExec([]string{"wget", "-O-", svcURL}).
			Stdout(ctx)
	case "http":
		return c.HTTP(svcURL).Contents(ctx)
	case "git":
		return c.Git(svcURL).Branch("main").Tree().File("index.html").Contents(ctx)
	default:
		return "", fmt.Errorf("unknown mode: %q", mode)
	}
}

func fatal(err any) {
	fmt.Fprintf(os.Stderr, "\x1b[31m%s\x1b[0m\n", err)
	os.Exit(1)
}

func httpService(ctx context.Context, c *dagger.Client, dir *dagger.Directory) (*dagger.Service, string) {
	srv := c.Container().
		From("python").
		WithMountedDirectory("/srv/www", dir).
		WithWorkdir("/srv/www").
		WithExposedPort(8000).
		WithDefaultArgs([]string{"python", "-m", "http.server"}).
		AsService()

	httpURL, err := srv.Endpoint(ctx, dagger.ServiceEndpointOpts{
		Scheme: "http",
	})
	if err != nil {
		fatal(err)
	}

	return srv, httpURL
}

func gitService(ctx context.Context, c *dagger.Client, content *dagger.Directory) (*dagger.Service, string) {
	const gitPort = 9418
	gitDaemon := c.Container().
		From("alpine:3.16.2").
		WithExec([]string{"apk", "add", "git", "git-daemon"}).
		WithDirectory("/root/repo", content).
		WithMountedFile("/root/start.sh",
			c.Directory().
				WithNewFile("start.sh", `#!/bin/sh

set -e -u -x

cd /root

git config --global user.email "root@localhost"
git config --global user.name "Test User"

mkdir srv

cd repo
	git init
	git branch -m main
	git add * || true
	git commit -m "init"
cd ..

cd srv
	git clone --bare ../repo repo.git
cd ..

git daemon --verbose --export-all --base-path=/root/srv
`).
				File("start.sh")).
		WithExposedPort(gitPort).
		WithDefaultArgs([]string{"sh", "/root/start.sh"}).
		AsService()

	gitHost, err := gitDaemon.Hostname(ctx)
	if err != nil {
		fatal(err)
	}

	repoURL := fmt.Sprintf("git://%s/repo.git", gitHost)

	return gitDaemon, repoURL
}
