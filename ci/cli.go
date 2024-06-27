package main

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/ci/build"
	"github.com/dagger/dagger/ci/internal/dagger"
)

type CLI struct {
	Dagger *Dagger // +private
}

// Build the CLI binary
func (cli *CLI) File(
	ctx context.Context,

	// +optional
	platform dagger.Platform,
) (*File, error) {
	builder, err := build.NewBuilder(ctx, cli.Dagger.Source)
	if err != nil {
		return nil, err
	}
	builder = builder.WithVersion(cli.Dagger.Version.String())
	if platform != "" {
		builder = builder.WithPlatform(platform)
	}
	return builder.CLI(ctx)
}

const (
	// https://github.com/goreleaser/goreleaser/releases
	goReleaserVersion = "v1.26.0"
)

// Publish the CLI using GoReleaser
func (cli *CLI) Publish(
	ctx context.Context,

	gitDir *Directory,

	githubOrgName string,
	githubToken *Secret,

	goreleaserKey *Secret,

	awsAccessKeyID *Secret,
	awsSecretAccessKey *Secret,
	awsRegion *Secret,
	awsBucket *Secret,

	artefactsFQDN *Secret,
) error {
	args := []string{"release", "--clean", "--skip-validate", "--debug"}
	if cli.Dagger.Version.Tag != "" {
		args = append(args, "--release-notes", fmt.Sprintf(".changes/%s.md", cli.Dagger.Version.Tag))
	} else {
		// if this isn't an official semver version, do a dev release
		args = append(args,
			"--nightly",
			"--config", ".goreleaser.nightly.yml",
		)
	}

	ctr, err := publishEnv(ctx)
	if err != nil {
		return err
	}
	_, err = ctr.
		WithWorkdir("/app").
		WithMountedDirectory("/app", cli.Dagger.Source).
		WithDirectory("/app/.git", gitDir).
		WithEnvVariable("GH_ORG_NAME", githubOrgName).
		WithSecretVariable("GITHUB_TOKEN", githubToken).
		WithSecretVariable("GORELEASER_KEY", goreleaserKey).
		WithSecretVariable("AWS_ACCESS_KEY_ID", awsAccessKeyID).
		WithSecretVariable("AWS_SECRET_ACCESS_KEY", awsSecretAccessKey).
		WithSecretVariable("AWS_REGION", awsRegion).
		WithSecretVariable("AWS_BUCKET", awsBucket).
		WithSecretVariable("ARTEFACTS_FQDN", artefactsFQDN).
		WithEnvVariable("ENGINE_VERSION", cli.Dagger.Version.String()).
		With(func(ctr *dagger.Container) *dagger.Container {
			if cli.Dagger.Version.Tag == "" {
				// goreleaser refuses to run if there isn't a tag, so set it to a dummy but valid semver
				return ctr.WithExec([]string{"git", "tag", "0.0.0"})
			}
			return ctr
		}).
		WithEntrypoint([]string{"/sbin/tini", "--", "/entrypoint.sh"}).
		WithExec(args).
		Sync(ctx)
	return err
}

// Verify that the CLI builds without actually publishing anything
func (cli *CLI) TestPublish(ctx context.Context) error {
	// TODO: ideally this would also use go releaser, but we want to run this
	// step in PRs and locally and we use goreleaser pro features that require
	// a key which is private. For now, this just builds the CLI for the same
	// targets so there's at least some coverage

	oses := []string{"linux", "windows", "darwin"}
	arches := []string{"amd64", "arm64", "arm"}

	builder, err := build.NewBuilder(ctx, cli.Dagger.Source)
	if err != nil {
		return err
	}
	builder = builder.WithVersion(cli.Dagger.Version.String())

	var eg errgroup.Group
	for _, os := range oses {
		for _, arch := range arches {
			if arch == "arm" && os == "darwin" {
				continue
			}

			platform := os + "/" + arch
			if arch == "arm" {
				platform += "/v7" // not always correct but not sure of better way
			}

			eg.Go(func() error {
				f, err := builder.
					WithPlatform(Platform(platform)).
					CLI(ctx)
				if err != nil {
					return err
				}
				_, err = f.Sync(ctx)
				return err
			})
		}
	}

	eg.Go(func() error {
		env, err := publishEnv(ctx)
		if err != nil {
			return err
		}
		_, err = env.Sync(ctx)
		return err
	})

	return eg.Wait()
}

func publishEnv(ctx context.Context) (*dagger.Container, error) {
	ctr := dag.Container().
		From(fmt.Sprintf("ghcr.io/goreleaser/goreleaser-pro:%s-pro", goReleaserVersion)).
		WithEntrypoint([]string{}).
		WithExec([]string{"apk", "add", "aws-cli"})

	// install nix
	ctr = ctr.
		WithExec([]string{"apk", "add", "xz"}).
		WithDirectory("/nix", dag.Directory()).
		// FIXME: Container.withNewFile is changing signature in
		// https://github.com/dagger/dagger/pull/7293. Until that's released
		// we're avoiding using it here for compatibility with dev and stable.
		// WithNewFile("/etc/nix/nix.conf", `build-users-group =`).
		WithFile("/etc/nix/nix.conf", dag.Directory().
			WithNewFile("nix.conf", `build-users-group =`).
			File("nix.conf"),
		).
		WithExec([]string{"sh", "-c", "curl -L https://nixos.org/nix/install | sh -s -- --no-daemon"})
	path, err := ctr.EnvVariable(ctx, "PATH")
	if err != nil {
		return nil, err
	}
	ctr = ctr.WithEnvVariable("PATH", path+":/nix/var/nix/profiles/default/bin")
	// goreleaser requires nix-prefetch-url, so check we can run it
	ctr = ctr.WithExec([]string{"sh", "-c", "nix-prefetch-url 2>&1 | grep 'error: you must specify a URL'"})

	return ctr, nil
}
