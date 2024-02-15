package mage

import (
	"context"
	"fmt"
	"os"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"
)

const (
	// https://github.com/goreleaser/goreleaser/releases
	goReleaserVersion = "v1.22.1-pro"
)

type Cli mg.Namespace

// Publish publishes dagger CLI using GoReleaser
func (cl Cli) Publish(ctx context.Context, version string) error {
	if version == "" {
		return fmt.Errorf("version tag must be specified")
	}
	var versionInfo *util.VersionInfo
	if semver.IsValid(version) {
		versionInfo = &util.VersionInfo{Tag: version}
	} else {
		versionInfo = &util.VersionInfo{Commit: version}
	}

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	args := []string{"release", "--clean", "--skip-validate", "--debug"}
	if versionInfo.Tag != "" {
		args = append(args, "--release-notes", fmt.Sprintf(".changes/%s.md", versionInfo.Tag))
	} else {
		// if this isn't an official semver version, do a dev release
		args = append(args,
			"--nightly",
			"--config", ".goreleaser.nightly.yml",
		)
	}

	ctr, err := publishEnv(ctx, c)
	if err != nil {
		return err
	}
	wd := c.Host().Directory(".")
	_, err = ctr.
		WithWorkdir("/app").
		WithMountedDirectory("/app", wd).
		With(util.HostVar(c, "GH_ORG_NAME")).
		With(util.HostSecretVar(c, "GITHUB_TOKEN")).
		With(util.HostSecretVar(c, "GORELEASER_KEY")).
		With(util.HostSecretVar(c, "AWS_ACCESS_KEY_ID")).
		With(util.HostSecretVar(c, "AWS_SECRET_ACCESS_KEY")).
		With(util.HostSecretVar(c, "AWS_REGION")).
		With(util.HostSecretVar(c, "AWS_BUCKET")).
		With(util.HostSecretVar(c, "ARTEFACTS_FQDN")).
		WithEnvVariable("ENGINE_VERSION", versionInfo.EngineVersion()).
		With(func(ctr *dagger.Container) *dagger.Container {
			if versionInfo.Tag == "" {
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

// TestPublish verifies that the CLI builds without actually publishing anything
// TODO: ideally this would also use go releaser, but we want to run this step in
// PRs and locally and we use goreleaser pro features that require a key which is private.
// For now, this just builds the CLI for the same targets so there's at least some
// coverage
func (cl Cli) TestPublish(ctx context.Context) error {
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		return err
	}
	defer c.Close()

	versionInfo, err := util.DevelVersionInfo(ctx, c)
	if err != nil {
		return err
	}

	oses := []string{"linux", "windows", "darwin"}
	arches := []string{"amd64", "arm64", "arm"}

	var eg errgroup.Group
	for _, os := range oses {
		for _, arch := range arches {
			if arch == "arm" && os == "darwin" {
				continue
			}
			var goarm string
			if arch == "arm" {
				goarm = "7" // not always correct but not sure of better way
			}
			os := os
			arch := arch
			eg.Go(func() error {
				_, err := util.
					PlatformDaggerBinary(c, os, arch, goarm, versionInfo.EngineVersion()).
					Export(ctx, "./bin/dagger-"+os+"-"+arch)
				return err
			})
		}
	}

	eg.Go(func() error {
		env, err := publishEnv(ctx, c)
		if err != nil {
			return err
		}
		_, err = env.Sync(ctx)
		return err
	})

	return eg.Wait()
}

func publishEnv(ctx context.Context, c *dagger.Client) (*dagger.Container, error) {
	ctr := c.Container().
		From(fmt.Sprintf("ghcr.io/goreleaser/goreleaser-pro:%s", goReleaserVersion)).
		WithEntrypoint([]string{}).
		WithExec([]string{"apk", "add", "aws-cli"})

	// install nix
	ctr = ctr.
		WithExec([]string{"apk", "add", "xz"}).
		WithDirectory("/nix", c.Directory()).
		WithNewFile("/etc/nix/nix.conf", dagger.ContainerWithNewFileOpts{
			Contents: `build-users-group =`,
		}).
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
