package mage

import (
	"context"
	"os"

	"github.com/magefile/mage/mg"

	"github.com/dagger/dagger/ci/mage/util"
)

type Cli mg.Namespace

// Publish publishes dagger CLI using GoReleaser
func (cl Cli) Publish(ctx context.Context, version string) error {
	args := []string{"--version=" + version, "cli", "publish"}

	// explicitly pass the git directory - goreleaser needs it
	args = append(args, "--git-dir=./.git")

	if v, ok := os.LookupEnv("GH_ORG_NAME"); ok {
		args = append(args, "--github-org-name="+v)
	}
	if _, ok := os.LookupEnv("GITHUB_PAT"); ok {
		args = append(args, "--github-token=env:GITHUB_PAT")
	}
	if _, ok := os.LookupEnv("GITHUB_TOKEN"); ok {
		args = append(args, "--github-token=env:GITHUB_TOKEN")
	}
	if _, ok := os.LookupEnv("GORELEASER_KEY"); ok {
		args = append(args, "--goreleaser-key=env:GORELEASER_KEY")
	}
	if _, ok := os.LookupEnv("AWS_ACCESS_KEY_ID"); ok {
		args = append(args, "--aws-access-key-id=env:AWS_ACCESS_KEY_ID")
	}
	if _, ok := os.LookupEnv("AWS_SECRET_ACCESS_KEY"); ok {
		args = append(args, "--aws-secret-access-key=env:AWS_SECRET_ACCESS_KEY")
	}
	if _, ok := os.LookupEnv("AWS_REGION"); ok {
		args = append(args, "--aws-region=env:AWS_REGION")
	}
	if _, ok := os.LookupEnv("AWS_BUCKET"); ok {
		args = append(args, "--aws-bucket=env:AWS_BUCKET")
	}
	if _, ok := os.LookupEnv("ARTEFACTS_FQDN"); ok {
		args = append(args, "--artefacts-fqdn=env:ARTEFACTS_FQDN")
	}

	return util.DaggerCall(ctx, args...)
}
