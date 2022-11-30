package mage

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
	"golang.org/x/mod/semver"
)

type Cli mg.Namespace

// Publish publishes dagger CLI using GoReleaser
func (cl Cli) Publish(ctx context.Context, version string) error {
	if !semver.IsValid(version) {
		fmt.Printf("'%s' is not a semver version, skipping CLI publish", version)
		return nil
	}

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
		WithSecretVariable("GITHUB_TOKEN", util.WithSetHostVar(ctx, c.Host(), "GITHUB_TOKEN").Secret()).
		WithSecretVariable("AWS_ACCESS_KEY_ID", util.WithSetHostVar(ctx, c.Host(), "AWS_ACCESS_KEY_ID").Secret()).
		WithSecretVariable("AWS_SECRET_ACCESS_KEY", util.WithSetHostVar(ctx, c.Host(), "AWS_SECRET_ACCESS_KEY").Secret()).
		WithSecretVariable("AWS_REGION", util.WithSetHostVar(ctx, c.Host(), "AWS_REGION").Secret()).
		WithSecretVariable("AWS_BUCKET", util.WithSetHostVar(ctx, c.Host(), "AWS_BUCKET").Secret()).
		WithSecretVariable("ARTEFACTS_FQDN", util.WithSetHostVar(ctx, c.Host(), "ARTEFACTS_FQDN").Secret()).
		WithSecretVariable("HOMEBREW_TAP_OWNER", util.WithSetHostVar(ctx, c.Host(), "HOMEBREW_TAP_OWNER").Secret())

	_, err = container.
		WithExec([]string{"release", "--rm-dist", "--skip-validate", "--debug"}).
		ExitCode(ctx)
	return err
}
