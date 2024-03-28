package sdk

import (
	"context"
	"os"
	"strconv"

	"github.com/dagger/dagger/internal/mage/util"

	"github.com/magefile/mage/mg"
)

type Go mg.Namespace

var _ SDK = Go{}

// Lint lints the Go SDK
func (t Go) Lint(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "go", "lint")
}

// Test tests the Go SDK
func (t Go) Test(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "go", "test")
}

// Generate re-generates the SDK API
func (t Go) Generate(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "go", "generate", "export", "--path=.")
}

// Publish publishes the Go SDK
func (t Go) Publish(ctx context.Context, tag string) error {
	args := []string{"sdk", "go", "publish", "--tag=" + tag}

	if dryRun, _ := strconv.ParseBool(os.Getenv("DRY_RUN")); dryRun {
		args = append(args, "--dry-run=true")
	}

	if v, ok := os.LookupEnv("TARGET_REPO"); ok {
		args = append(args, "--git-repo="+v)
	}
	if v, ok := os.LookupEnv("GIT_USER_NAME"); ok {
		args = append(args, "--git-user-name="+v)
	}
	if v, ok := os.LookupEnv("GIT_USER_EMAIL"); ok {
		args = append(args, "--git-user-email="+v)
	}
	if _, ok := os.LookupEnv("GITHUB_PAT"); ok {
		args = append(args, "--github-token=env:GITHUB_PAT")
	}

	return util.DaggerCall(ctx, args...)
}

// Bump the Go SDK's Engine dependency
func (t Go) Bump(ctx context.Context, version string) error {
	return util.DaggerCall(ctx, "sdk", "go", "bump", "--version="+version, "export", "--path=.")
}
