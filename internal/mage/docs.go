package mage

import (
	"context"
	"os"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg"
	"golang.org/x/sync/errgroup"
)

type Docs mg.Namespace

// Lint lints documentation files
func (Docs) Lint(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	workdir := util.Repository(c)

	eg, gctx := errgroup.WithContext(ctx)

	// Markdown
	eg.Go(func() error {
		_, err = c.Container().
			From("tmknom/markdownlint:0.31.1").
			WithMountedDirectory("/src", workdir).
			WithMountedFile("/src/.markdownlint.yaml", workdir.File(".markdownlint.yaml")).
			WithWorkdir("/src").
			Exec(dagger.ContainerExecOpts{
				Args: []string{
					"-c",
					".markdownlint.yaml",
					"--",
					"./docs",
					"README.md",
				},
			}).ExitCode(gctx)
		return err
	})

	// NodeJS
	eg.Go(func() error {
		nodeSnippets := c.Directory().
			WithDirectory("/", workdir.Directory("docs/current/sdk/nodejs/snippets"))
		_, err = c.Container().
			From("node:16-alpine").
			WithWorkdir("/src").
			WithMountedDirectory("/src", nodeSnippets).
			Exec(dagger.ContainerExecOpts{
				Args: []string{"yarn", "install"},
			}).
			Exec(dagger.ContainerExecOpts{
				Args: []string{"yarn", "lint"},
			}).ExitCode(gctx)
		return err
	})

	// FIXME: Python

	// Go is already linted by engine:lint

	return eg.Wait()
}
