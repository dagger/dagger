package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/.dagger/internal/dagger"
)

type CLI struct {
	Dagger *DaggerDev // +private
}

// Build the CLI binary
func (cli *CLI) Binary(
	// +optional
	runnerHost string,
	// +optional
	platform dagger.Platform,
) *dagger.File {
	return dag.DaggerCli(dagger.DaggerCliOpts{RunnerHost: runnerHost}).
		Binary(dagger.DaggerCliBinaryOpts{Platform: platform})
}

// Build dev CLI binaries
func (cli *CLI) DevBinaries(
	// +optional
	runnerHost string,
	// +optional
	platform dagger.Platform,
) *dagger.Directory {
	p := platforms.MustParse(string(platform))
	c := dag.DaggerCli(dagger.DaggerCliOpts{RunnerHost: runnerHost})

	bin := c.Binary(dagger.DaggerCliBinaryOpts{Platform: platform})
	binName := "dagger"
	if p.OS == "windows" {
		binName += ".exe"
	}

	dir := dag.Directory().WithFile(binName, bin)
	if p.OS != "linux" {
		p2 := p
		p2.OS = "linux"
		p2.OSFeatures = nil
		p2.OSVersion = ""
		dir = dir.WithFile("dagger-linux", c.Binary(dagger.DaggerCliBinaryOpts{Platform: dagger.Platform(platforms.Format(p2))}))
	}

	return dir
}

const (
	// https://github.com/goreleaser/goreleaser/releases
	goReleaserVersion = "v2.7.0"
	goReleaserImage   = "ghcr.io/goreleaser/goreleaser-pro:" + goReleaserVersion
)

// Publish the CLI using GoReleaser
func (cli *CLI) Publish(
	ctx context.Context,
	tag string,

	goreleaserKey *dagger.Secret,

	githubOrgName string,
	githubToken *dagger.Secret, // +optional

	awsAccessKeyID *dagger.Secret, // +optional
	awsSecretAccessKey *dagger.Secret, // +optional
	awsRegion string, // +optional
	awsBucket string, // +optional
	artefactsFQDN string, // +optional

	dryRun bool, // +optional
) (*dagger.Directory, error) {
	ctr, err := publishEnv(ctx)
	if err != nil {
		return nil, err
	}
	ctr = ctr.
		WithWorkdir("/app").
		WithDirectory(".", cli.Dagger.Source()).
		WithDirectory(".", cli.Dagger.Git.Directory()).
		WithDirectory("build", cli.goreleaserBinaries())

	if !semver.IsValid(tag) {
		// all non-semver tags (like "main") are dev builds
		tag = ""
	} else {
		// sanity check that the semver tag actually exists, otherwise do a dev build
		_, err = ctr.WithExec([]string{"git", "show-ref", "--verify", "refs/tags/" + tag}).Sync(ctx)
		if err != nil {
			err, ok := err.(*ExecError)
			if !ok || !strings.Contains(err.Stderr, "not a valid ref") {
				return nil, err
			}
			tag = ""
		}
	}
	if tag == "" {
		// goreleaser refuses to run if there isn't a tag, so set it to a dummy but valid semver
		ctr = ctr.WithExec([]string{"git", "tag", "v0.0.0"})
	}

	args := []string{"release", "--clean", "--skip=validate", "--verbose"}
	if tag != "" {
		if semver.Prerelease(tag) == "" {
			// public release (vX.Y.Z)
			args = append(args,
				"--release-notes", fmt.Sprintf(".changes/%s.md", tag),
			)
		} else {
			// public pre-release (vX.Y.Z-prerelease)
			args = append(args,
				"--nightly",
				"--config", ".goreleaser.prerelease.yml",
			)
		}
	} else {
		// nightly off of main
		args = append(args,
			"--nightly",
			"--config", ".goreleaser.nightly.yml",
		)
	}
	if dryRun {
		args = append(args, "--skip=publish")
	}

	ctr, err = ctr.
		WithEnvVariable("GH_ORG_NAME", githubOrgName).
		WithSecretVariable("GORELEASER_KEY", goreleaserKey).
		With(optSecretVariable("GITHUB_TOKEN", githubToken)).
		With(optSecretVariable("AWS_ACCESS_KEY_ID", awsAccessKeyID)).
		With(optSecretVariable("AWS_SECRET_ACCESS_KEY", awsSecretAccessKey)).
		With(optEnvVariable("AWS_REGION", awsRegion)).
		With(optEnvVariable("AWS_BUCKET", awsBucket)).
		With(optEnvVariable("ARTEFACTS_FQDN", artefactsFQDN)).
		WithEnvVariable("ENGINE_VERSION", cli.Dagger.Version).
		WithEnvVariable("ENGINE_TAG", cli.Dagger.Tag).
		WithEnvVariable("GORELEASER_CURRENT_TAG", tag).
		WithEntrypoint([]string{"/sbin/tini", "--", "/entrypoint.sh"}).
		WithExec(args, dagger.ContainerWithExecOpts{
			UseEntrypoint: true,
		}).
		Sync(ctx)
	if err != nil {
		return nil, err
	}
	return ctr.Directory("dist"), nil
}

func optEnvVariable(name string, val string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		if val == "" {
			return ctr
		}
		return ctr.WithEnvVariable(name, val)
	}
}

func optSecretVariable(name string, val *dagger.Secret) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		if val == nil {
			return ctr
		}
		return ctr.WithSecretVariable(name, val)
	}
}

func (cli *CLI) PublishMetadata(
	ctx context.Context,

	awsAccessKeyID *dagger.Secret,
	awsSecretAccessKey *dagger.Secret,
	awsRegion string,
	awsBucket string,
	awsCloudfrontDistribution string,
) error {
	ctr := dag.
		Alpine(dagger.AlpineOpts{
			Packages: []string{"aws-cli"},
		}).
		Container().
		WithWorkdir("/src").
		WithDirectory(".", cli.Dagger.Source()).
		WithSecretVariable("AWS_ACCESS_KEY_ID", awsAccessKeyID).
		WithSecretVariable("AWS_SECRET_ACCESS_KEY", awsSecretAccessKey).
		WithEnvVariable("AWS_REGION", awsRegion).
		WithEnvVariable("AWS_EC2_METADATA_DISABLED", "true")

	// update install scripts
	ctr = ctr.
		WithExec([]string{"aws", "s3", "cp", "./install.sh", s3Path(awsBucket, "dagger/install.sh")}).
		WithExec([]string{"aws", "s3", "cp", "./install.ps1", s3Path(awsBucket, "dagger/install.ps1")}).
		WithExec([]string{"aws", "cloudfront", "create-invalidation", "--distribution-id", awsCloudfrontDistribution, "--paths", "/dagger/install.sh", "/dagger/install.ps1"})

	// update version pointers (only on proper releases)
	if version := cli.Dagger.Version; semver.IsValid(version) && semver.Prerelease(version) == "" {
		cpOpts := dagger.ContainerWithExecOpts{
			Stdin: strings.TrimPrefix(version, "v"),
		}
		ctr = ctr.
			WithExec([]string{"aws", "s3", "cp", "-", s3Path(awsBucket, "dagger/latest_version")}, cpOpts).
			WithExec([]string{"aws", "s3", "cp", "-", s3Path(awsBucket, "dagger/versions/latest")}, cpOpts).
			WithExec([]string{"aws", "s3", "cp", "-", s3Path(awsBucket, "dagger/versions/%s", strings.TrimPrefix(semver.MajorMinor(version), "v"))}, cpOpts)
	}

	_, err := ctr.Sync(ctx)
	return err
}

func s3Path(bucket string, path string, args ...any) string {
	u := url.URL{
		Scheme: "s3",
		Host:   bucket,
		Path:   fmt.Sprintf(path, args...),
	}
	return u.String()
}

// Verify that the CLI builds without actually publishing anything
func (cli *CLI) TestPublish(ctx context.Context) error {
	// TODO: ideally this would also use go releaser, but we want to run this
	// step in PRs and locally and we use goreleaser pro features that require
	// a key which is private. For now, this just builds the CLI for the same
	// targets so there's at least some coverage

	var eg errgroup.Group
	eg.Go(func() error {
		_, err := cli.goreleaserBinaries().Sync(ctx)
		return err
	})
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

func (cli *CLI) goreleaserBinaries() *dagger.Directory {
	oses := []string{"linux", "windows", "darwin"}
	arches := []string{"amd64", "arm64", "arm"}

	dir := dag.Directory()
	for _, os := range oses {
		for _, arch := range arches {
			if arch == "arm" && os == "darwin" {
				continue
			}

			platform := os + "/" + arch
			if arch == "arm" {
				platform += "/v7" // not always correct but not sure of better way
			}

			binary := dag.DaggerCli().Binary(dagger.DaggerCliBinaryOpts{Platform: dagger.Platform(platform)})
			dest := fmt.Sprintf("dagger_%s_%s/dagger", cli.Dagger.Version, strings.ReplaceAll(platform, "/", "_"))
			dir = dir.WithFile(dest, binary)
		}
	}
	return dir
}

func publishEnv(ctx context.Context) (*dagger.Container, error) {
	ctr := dag.Container().From(goReleaserImage)

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
