package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/dagger/dagger/util/parallel"
	"golang.org/x/mod/semver"

	"dagger/cli-dev/internal/dagger"
	"dagger/cli-dev/util"
)

// Publish the CLI release artifacts.
// +cache="session"
func (cli *CliDev) Publish(
	ctx context.Context,
	tag string,
	commit string,

	githubOrgName string,
	githubToken *dagger.Secret, // +optional
	githubHost string, // +optional
	githubCaCert *dagger.File, // +optional

	awsAccessKeyID *dagger.Secret, // +optional
	awsSecretAccessKey *dagger.Secret, // +optional
	awsRegion string, // +optional
	awsBucket string, // +optional
	artefactsFQDN string, // +optional
	awsEndpointURL string, // +optional

	dryRun bool, // +optional
) (*dagger.Directory, error) {
	dist, err := cli.releaseDist(ctx, tag, commit)
	if err != nil {
		return nil, err
	}
	if dryRun {
		return dist, nil
	}

	mode := cliReleaseModeForTag(tag)
	if err := cli.publishReleaseArtifactsToS3(ctx, dist, tag, commit, mode, awsAccessKeyID, awsSecretAccessKey, awsRegion, awsBucket, awsEndpointURL); err != nil {
		return nil, err
	}

	if mode == cliReleaseModeStable {
		notes, err := cli.Go.Source().File(".changes/" + tag + ".md").Contents(ctx)
		if err != nil {
			return nil, err
		}
		if err := cli.publishRootGitHubRelease(ctx, dist, tag, commit, notes, githubOrgName, githubToken, githubHost, githubCaCert); err != nil {
			return nil, err
		}
		if err := cli.publishPackageManagers(ctx, dist, tag, githubOrgName, githubToken, githubHost, githubCaCert, artefactsFQDN); err != nil {
			return nil, err
		}
	}

	return dist, nil
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

func withGithubCaCert(caCert *dagger.File) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		if caCert == nil {
			return ctr
		}
		return ctr.
			WithMountedFile("/usr/local/share/ca-certificates/dagger-github.crt", caCert).
			WithExec([]string{"update-ca-certificates"})
	}
}

func githubAPIURL(host string) string {
	if host == "" {
		return "https://api.github.com"
	}
	return "https://" + host + "/api/v3"
}

// +cache="session"
func (cli *CliDev) PublishMetadata(
	ctx context.Context,

	awsAccessKeyID *dagger.Secret,
	awsSecretAccessKey *dagger.Secret,
	awsRegion string,
	awsBucket string,
	awsCloudfrontDistribution string,
	awsEndpointURL string, // +optional
) error {
	ctr := dag.
		Alpine(dagger.AlpineOpts{
			Branch:   "3.22",
			Packages: []string{"aws-cli"},
		}).
		Container().
		WithWorkdir("/src").
		WithDirectory(".", cli.Go.Source()).
		WithSecretVariable("AWS_ACCESS_KEY_ID", awsAccessKeyID).
		WithSecretVariable("AWS_SECRET_ACCESS_KEY", awsSecretAccessKey).
		WithEnvVariable("AWS_REGION", awsRegion).
		With(optEnvVariable("AWS_ENDPOINT_URL", awsEndpointURL)).
		WithEnvVariable("AWS_EC2_METADATA_DISABLED", "true")

	// update install scripts
	ctr = ctr.
		WithExec(awsCommand(awsEndpointURL, "s3", "cp", "./install.sh", s3Path(awsBucket, "dagger/install.sh"))).
		WithExec(awsCommand(awsEndpointURL, "s3", "cp", "./install.ps1", s3Path(awsBucket, "dagger/install.ps1"))).
		WithExec(awsCommand(awsEndpointURL, "cloudfront", "create-invalidation", "--distribution-id", awsCloudfrontDistribution, "--paths", "/dagger/install.sh", "/dagger/install.ps1"))

	// update version pointers (only on proper releases)
	if version := cli.Version; semver.IsValid(version) {
		cpOpts := dagger.ContainerWithExecOpts{
			Stdin: strings.TrimPrefix(version, "v"),
		}
		if semver.Prerelease(version) == "" {
			ctr = ctr.
				WithExec(awsCommand(awsEndpointURL, "s3", "cp", "-", s3Path(awsBucket, "dagger/latest_version")), cpOpts).
				WithExec(awsCommand(awsEndpointURL, "s3", "cp", "-", s3Path(awsBucket, "dagger/versions/latest")), cpOpts).
				WithExec(awsCommand(awsEndpointURL, "s3", "cp", "-", s3Path(awsBucket, "dagger/versions/%s", strings.TrimPrefix(semver.MajorMinor(version), "v"))), cpOpts)
		} else {
			for _, variant := range util.PrereleaseVariants(version) {
				ctr = ctr.
					WithExec(awsCommand(awsEndpointURL, "s3", "cp", "-", s3Path(awsBucket, "dagger/versions/%s", strings.TrimPrefix(variant, "v"))), cpOpts)
			}
		}
	}

	_, err := ctr.Sync(ctx)
	return err
}

func awsCommand(endpointURL string, args ...string) []string {
	cmd := []string{"aws"}
	if endpointURL != "" {
		cmd = append(cmd, "--endpoint-url", endpointURL)
	}
	return append(cmd, args...)
}

func s3Path(bucket string, path string, args ...any) string {
	u := url.URL{
		Scheme: "s3",
		Host:   bucket,
		Path:   fmt.Sprintf(path, args...),
	}
	return u.String()
}

// +check
// Verify that the CLI builds without actually publishing anything
func (cli *CliDev) ReleaseDryRun(ctx context.Context) error {
	return parallel.New().
		WithJob(
			"dry-run build on all targets",
			func(ctx context.Context) error {
				_, err := cli.releaseBinaries().Sync(ctx)
				return err
			}).
		WithJob(
			"dry-run package release artifacts",
			func(ctx context.Context) error {
				dist, err := cli.releaseDist(ctx, cli.Version, "dry-run")
				if err != nil {
					return err
				}
				_, err = dist.Sync(ctx)
				return err
			}).
		Run(ctx)
}
