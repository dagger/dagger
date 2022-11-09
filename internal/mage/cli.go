package mage

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
)

type Cli mg.Namespace

func (cl Cli) Publish(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	wd := c.Host().Workdir()

	c.Host().EnvVariable("").Secret()

	code, err := c.Container().From("ghcr.io/goreleaser/goreleaser-pro:v1.12.3-pro").
		WithMountedDirectory("/app", wd).
		WithWorkdir("/app").
		WithSecretVariable("GITHUB_TOKEN", c.Host().EnvVariable("GITHUB_TOKEN").Secret()).
		WithSecretVariable("AWS_ACCESS_KEY_ID", c.Host().EnvVariable("GITHUB_TOKEN").Secret()).
		WithSecretVariable("AWS_SECRET_ACCESS_KEY", c.Host().EnvVariable("GITHUB_TOKEN").Secret()).
		WithSecretVariable("AWS_REGION", c.Host().EnvVariable("GITHUB_TOKEN").Secret()).
		WithSecretVariable("AWS_BUCKET", c.Host().EnvVariable("GITHUB_TOKEN").Secret()).
		WithSecretVariable("ARTEFACTS_FQDN", c.Host().EnvVariable("GITHUB_TOKEN").Secret()).
		Exec(dagger.ContainerExecOpts{Args: []string{"release", "--rm-dist", "--debug"}}).
		ExitCode(ctx)
	if err != nil {
		return fmt.Errorf("error running goreleaser. code:%d err:%w", code, err)
	}

	return nil
}
