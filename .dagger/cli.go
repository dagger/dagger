package main

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/.dagger/build"
	"github.com/dagger/dagger/.dagger/internal/dagger"
)

type CLI struct {
	Dagger *DaggerDev // +private
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
	if platform != "" {
		builder = builder.WithPlatform(platform)
	}
	return builder.CLI(ctx)
}

const (
	// https://github.com/goreleaser/goreleaser/releases
	goReleaserVersion = "v2.3.2"
)

// Publish the CLI using GoReleaser
func (cli *CLI) Publish(
	ctx context.Context,

	githubOrgName string,
	githubToken *dagger.Secret,

	goreleaserKey *dagger.Secret,

	awsAccessKeyID *dagger.Secret,
	awsSecretAccessKey *dagger.Secret,
	awsRegion *dagger.Secret,
	awsBucket *dagger.Secret,

	artefactsFQDN string,
) error {
	ctr, err := publishEnv(ctx)
	if err != nil {
		return err
	}
	ctr = ctr.
		WithWorkdir("/app").
		WithDirectory("/app", cli.Dagger.Source()).
		WithDirectory("/app", cli.Dagger.Git.Directory())

	tag := cli.Dagger.Tag
	_, err = ctr.WithExec([]string{"git", "show-ref", "--verify", "refs/tags/" + tag}).Sync(ctx)
	if err != nil {
		err, ok := err.(*ExecError)
		if !ok || !strings.Contains(err.Stderr, "not a valid ref") {
			return err
		}

		// clear the set tag
		tag = ""
		// goreleaser refuses to run if there isn't a tag, so set it to a dummy but valid semver
		ctr = ctr.WithExec([]string{"git", "tag", "0.0.0"})
	}

	args := []string{"release", "--clean", "--skip=validate", "--verbose"}
	if tag != "" {
		args = append(args, "--release-notes", fmt.Sprintf(".changes/%s.md", tag))
	} else {
		// if this isn't an official semver version, do a dev release
		args = append(args,
			"--nightly",
			"--config", ".goreleaser.nightly.yml",
		)
	}

	_, err = ctr.
		WithEnvVariable("GH_ORG_NAME", githubOrgName).
		WithSecretVariable("GITHUB_TOKEN", githubToken).
		WithSecretVariable("GORELEASER_KEY", goreleaserKey).
		WithSecretVariable("AWS_ACCESS_KEY_ID", awsAccessKeyID).
		WithSecretVariable("AWS_SECRET_ACCESS_KEY", awsSecretAccessKey).
		WithSecretVariable("AWS_REGION", awsRegion).
		WithSecretVariable("AWS_BUCKET", awsBucket).
		WithEnvVariable("ARTEFACTS_FQDN", artefactsFQDN).
		WithEnvVariable("ENGINE_VERSION", cli.Dagger.Version).
		WithEnvVariable("ENGINE_TAG", cli.Dagger.Tag).
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

func publishEnv(ctx context.Context) (*dagger.Container, error) {
	// TODO: remove after upgrading to GoReleaser Pro has go 1.23.2 (it currently only has go 1.23.1)
	go1_23_2 := dag.Container().From("golang:1.23.2-alpine@sha256:9dd2625a1ff2859b8d8b01d8f7822c0f528942fe56cfe7a1e7c38d3b8d72d679").Directory("/usr/local/go")

	ctr := dag.Container().
		From(fmt.Sprintf("ghcr.io/goreleaser/goreleaser-pro:%s-pro", goReleaserVersion)).
		WithDirectory("/usr/local/go", go1_23_2).
		WithEntrypoint([]string{}).
		WithExec([]string{"apk", "add", "aws-cli"})

	// install nix
	ctr = ctr.
		WithExec([]string{"apk", "add", "xz"}).
		WithDirectory("/nix", dag.Directory()).
		WithNewFile("/etc/nix/nix.conf", `build-users-group =`).
		WithExec([]string{"sh", "-c", "curl -fsSL https://nixos.org/nix/install | sh -s -- --no-daemon"})
	path, err := ctr.EnvVariable(ctx, "PATH")
	if err != nil {
		return nil, err
	}
	ctr = ctr.WithEnvVariable("PATH", path+":/nix/var/nix/profiles/default/bin")
	// goreleaser requires nix-prefetch-url, so check we can run it
	ctr = ctr.WithExec([]string{"sh", "-c", "nix-prefetch-url 2>&1 | grep 'error: you must specify a URL'"})

	return ctr, nil
}
