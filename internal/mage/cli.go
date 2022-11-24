package mage

import (
	"context"
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

	wd := c.Host().Directory(".")
	container := c.Container().
		From("ghcr.io/goreleaser/goreleaser:v1.12.3").
		WithEntrypoint([]string{}).
		WithExec([]string{"apk", "add", "aws-cli"}).
		WithEntrypoint([]string{"/sbin/tini", "--", "/entrypoint.sh"}).
		WithWorkdir("/app").
		WithMountedDirectory("/app", wd).
		WithSecretVariable("GITHUB_TOKEN", c.Host().EnvVariable("GITHUB_TOKEN").Secret()).
		WithSecretVariable("AWS_ACCESS_KEY_ID", c.Host().EnvVariable("AWS_ACCESS_KEY_ID").Secret()).
		WithSecretVariable("AWS_SECRET_ACCESS_KEY", c.Host().EnvVariable("AWS_SECRET_ACCESS_KEY").Secret()).
		WithSecretVariable("AWS_REGION", c.Host().EnvVariable("AWS_REGION").Secret()).
		WithSecretVariable("AWS_BUCKET", c.Host().EnvVariable("AWS_BUCKET").Secret()).
		WithSecretVariable("ARTEFACTS_FQDN", c.Host().EnvVariable("ARTEFACTS_FQDN").Secret()).
		WithSecretVariable("HOMEBREW_TAP_OWNER", c.Host().EnvVariable("HOMEBREW_TAP_OWNER").Secret())

	_, err = container.
		WithExec([]string{"release", "--rm-dist", "--debug"}).
		ExitCode(ctx)
	return err
}
