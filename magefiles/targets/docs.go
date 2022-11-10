package targets

import (
	"context"
	"os"

	"github.com/dagger/dagger/magefiles/util"

	"dagger.io/dagger"
	"github.com/magefile/mage/mg"
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
		}).ExitCode(ctx)
	return err
}
