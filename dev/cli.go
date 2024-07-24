package main

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dev/internal/build"
	"github.com/dagger/dagger/dev/internal/dagger"
)

type CLI struct {
	Dagger *DaggerDev // +private
}

// Build the CLI binary (deprecated)
func (cli *CLI) File(
	ctx context.Context,
	// +optional
	platform dagger.Platform,
) (*dagger.File, error) {
	return cli.Binary(ctx, platform)
}

// Build the CLI binary
func (cli *CLI) Binary(
	ctx context.Context,

	// +optional
	platform dagger.Platform,
) (*dagger.File, error) {
	builder, err := build.NewBuilder(ctx, cli.Dagger.Source())
	if err != nil {
		return nil, err
	}
	builder = builder.WithVersion(cli.Dagger.Version.String())
	if platform != "" {
		builder = builder.WithPlatform(platform)
	}
	return builder.CLI(ctx)
}

// Publish the CLI using GoReleaser
func (cli *CLI) Publish(
	ctx context.Context,

	gitDir *dagger.Directory,

	githubOrgName string,
	githubToken *dagger.Secret,

	goreleaserKey *dagger.Secret,

	awsAccessKeyID *dagger.Secret,
	awsSecretAccessKey *dagger.Secret,
	awsRegion *dagger.Secret,
	awsBucket *dagger.Secret,

	artefactsFQDN *dagger.Secret,
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

	_, err := dag.
		Goreleaser(dagger.GoreleaserOpts{fqdn: artefactsFQDN}).
		WithNix(	).
		WithGithub(githubOrgName, githubToken).
		WithAWS(awsRegion, awsBucket, awsAccessKeyID, awsSecretAccessKey).


		dagger.GoreleaserOpts{
			Nix:           true,
			GithubOrg:     githubOrgName,
			GithubToken:   githubToken,
			proKey:        goreleaserKey,
			awsID:         awsAccessKeyID,
			awsSecret:     awsSecretAccessKey,
			awsRegion:     awsRegion,
			awsBucket:     awsBucket,
			artefactsFQDN: artefactsFQDN,
		}).
		Release()

	ctr, err := publishEnv(ctx)
	if err != nil {
		return err
	}
	_, err = ctr.
		WithWorkdir("/app").
		WithMountedDirectory("/app", cli.Dagger.Source()).
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
		WithExec(args, dagger.ContainerWithExecOpts{
			UseEntrypoint: true,
		}).
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

	builder, err := build.NewBuilder(ctx, cli.Dagger.Source())
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
					WithPlatform(dagger.Platform(platform)).
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

func publishEnv() (*dagger.Container, error) {
	ctr := dag.Container().
		From(fmt.Sprintf("ghcr.io/goreleaser/goreleaser-pro:%s-pro", goReleaserVersion)).
		WithEntrypoint([]string{}).
		WithExec([]string{"apk", "add", "aws-cli"})

	// install nix
	ctr = ctr.
		WithExec([]string{"apk", "add", "xz"}).
		WithDirectory("/nix", dag.Directory()).
		WithNewFile("/etc/nix/nix.conf", `build-users-group =`).
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
