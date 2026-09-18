package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strconv"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx := context.Background()

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		fatal(err)
	}
	defer c.Close()
	q := core.NewQuery(c)

	mode, depthStr, svcURLs := os.Args[1], os.Args[2], os.Args[3:]

	depth, err := strconv.Atoi(depthStr)
	if err != nil {
		fatal(err)
	}

	if depth > 1 {
		weHaveToGoDeeper(ctx, q, depth, mode, svcURLs)
		return
	}

	results := make(chan string, len(svcURLs))

	eg := new(errgroup.Group)
	for _, u := range svcURLs {
		eg.Go(func() error {
			out, err := fetch(ctx, q, mode, u)
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

func weHaveToGoDeeper(ctx context.Context, q *core.Query, depth int, mode string, svcURLs []string) {
	code := q.Host().Directory(".", core.HostDirectoryOpts{
		Include: []string{"core/integration/testdata/nested-c2c/", "sdk/go/", "go.mod", "go.sum"},
	})

	previous := svcURLs[len(svcURLs)-1]
	mirrorSvc, mirrorURL := mirror(ctx, q, mode, previous)

	args := []string{
		"go", "run", "./core/integration/testdata/nested-c2c/",
		mode, strconv.Itoa(depth - 1),
	}
	args = append(args, svcURLs...)
	args = append(args, mirrorURL)

	out, err := q.Container().
		From("golang:1.26-alpine").
		WithMountedCache("/go/pkg/mod", q.CacheVolume("go-mod")).
		WithEnvVariable("GOMODCACHE", "/go/pkg/mod").
		WithMountedCache("/go/build-cache", q.CacheVolume("go-build")).
		WithEnvVariable("GOCACHE", "/go/build-cache").
		WithMountedDirectory("/src", code).
		WithWorkdir("/src").
		WithEnvVariable("NOW", rand.Text()).
		WithExec([]string{"cat", "/etc/resolv.conf"}).
		WithServiceBinding("mirror", mirrorSvc).
		WithExec(args, core.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Stdout(ctx)
	if err != nil {
		fatal(err)
	}

	fmt.Print(out)
}

func mirror(ctx context.Context, q *core.Query, mode, svcURL string) (*core.Service, string) {
	switch mode {
	case "exec":
		return httpService(ctx, q,
			q.Container().
				From("alpine:3.16.2").
				WithWorkdir("/srv/www").
				WithExec([]string{"wget", svcURL}).
				Directory("."))
	case "http":
		return httpService(ctx, q,
			q.Directory().WithFile("index.html", q.HTTP(svcURL)))
	case "git":
		return gitService(ctx, q, q.Git(svcURL).Branch("main").Tree(core.GitRefTreeOpts{DiscardGitDir: true}))
	default:
		fatal(fmt.Errorf("unknown mode: %q", mode))
		return nil, ""
	}
}

func fetch(ctx context.Context, q *core.Query, mode, svcURL string) (string, error) {
	switch mode {
	case "exec":
		return q.Container().
			From("alpine:3.16.2").
			WithEnvVariable("NOW", rand.Text()).
			WithExec([]string{"cat", "/etc/resolv.conf"}).
			WithExec([]string{"wget", "-O-", svcURL}).
			Stdout(ctx)
	case "http":
		return q.HTTP(svcURL).Contents(ctx)
	case "git":
		return q.Git(svcURL).Branch("main").Tree().File("index.html").Contents(ctx)
	default:
		return "", fmt.Errorf("unknown mode: %q", mode)
	}
}

func fatal(err any) {
	fmt.Fprintf(os.Stderr, "\x1b[31m%s\x1b[0m\n", err)
	os.Exit(1)
}

func httpService(ctx context.Context, q *core.Query, dir *core.Directory) (*core.Service, string) {
	srv := q.Container().
		From("python").
		WithMountedDirectory("/srv/www", dir).
		WithWorkdir("/srv/www").
		WithExposedPort(8000).
		WithDefaultArgs([]string{"python", "-m", "http.server"}).
		AsService()

	httpURL, err := srv.Endpoint(ctx, core.ServiceEndpointOpts{
		Scheme: "http",
	})
	if err != nil {
		fatal(err)
	}

	return srv, httpURL
}

func gitService(ctx context.Context, q *core.Query, content *core.Directory) (*core.Service, string) {
	const gitPort = 9418
	gitDaemon := q.Container().
		From("alpine:3.16.2").
		WithExec([]string{"apk", "add", "git", "git-daemon"}).
		WithDirectory("/root/repo", content).
		WithMountedFile("/root/start.sh",
			q.Directory().
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
