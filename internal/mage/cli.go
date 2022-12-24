package mage

import (
	"context"
	"os"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
	"golang.org/x/mod/semver"
)

type Cli mg.Namespace

// Publish publishes dagger CLI using GoReleaser
func (cl Cli) Publish(ctx context.Context, version string) error {
	// if this isn't an official semver version, do a nightly release
	nightly := !semver.IsValid(version)

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	wd := c.Host().Directory(".")
	container := c.Container().
		From("ghcr.io/goreleaser/goreleaser-pro:v1.12.3-pro").
		WithEntrypoint([]string{}).
		WithExec([]string{"apk", "add", "aws-cli"}).
		WithWorkdir("/app").
		WithMountedDirectory("/app", wd).
		WithSecretVariable("GITHUB_TOKEN", util.WithSetHostVar(ctx, c.Host(), "GITHUB_TOKEN").Secret()).
		WithSecretVariable("GORELEASER_KEY", util.WithSetHostVar(ctx, c.Host(), "GORELEASER_KEY").Secret()).
		WithSecretVariable("AWS_ACCESS_KEY_ID", util.WithSetHostVar(ctx, c.Host(), "AWS_ACCESS_KEY_ID").Secret()).
		WithSecretVariable("AWS_SECRET_ACCESS_KEY", util.WithSetHostVar(ctx, c.Host(), "AWS_SECRET_ACCESS_KEY").Secret()).
		WithSecretVariable("AWS_REGION", util.WithSetHostVar(ctx, c.Host(), "AWS_REGION").Secret()).
		WithSecretVariable("AWS_BUCKET", util.WithSetHostVar(ctx, c.Host(), "AWS_BUCKET").Secret()).
		WithSecretVariable("ARTEFACTS_FQDN", util.WithSetHostVar(ctx, c.Host(), "ARTEFACTS_FQDN").Secret()).
		WithSecretVariable("HOMEBREW_TAP_OWNER", util.WithSetHostVar(ctx, c.Host(), "HOMEBREW_TAP_OWNER").Secret())

	if nightly {
		// goreleaser refuses to run if there isn't a tag, so set it to a dummy but valid semver
		container = container.WithExec([]string{"git", "tag", "0.0.0"})
	}

	args := []string{"release", "--rm-dist", "--skip-validate", "--debug"}
	if nightly {
		args = append(args,
			"--nightly",
			"--config", ".goreleaser.nightly.yml",
		)
	}

	_, err = container.
		WithEntrypoint([]string{"/sbin/tini", "--", "/entrypoint.sh"}).
		WithExec(args).
		ExitCode(ctx)
	return err
}
