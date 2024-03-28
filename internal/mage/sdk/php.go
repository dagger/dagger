package sdk

import (
	"context"
	"os"
	"strconv"

	"github.com/dagger/dagger/internal/mage/util"

	"github.com/magefile/mage/mg"
)

type PHP mg.Namespace

var _ SDK = PHP{}

// Lint lints the PHP SDK
func (PHP) Lint(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "php", "lint")
}

// Test tests the PHP SDK
func (PHP) Test(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "php", "test")
}

// Generate re-generates the SDK API
func (t PHP) Generate(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "php", "generate", "export", "--path=.")
}

// Publish publishes the PHP SDK
func (t PHP) Publish(ctx context.Context, tag string) error {
	args := []string{"sdk", "php", "publish", "--tag=" + tag}

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

// Bump the PHP SDK's Engine dependency
func (PHP) Bump(ctx context.Context, version string) error {
	return util.DaggerCall(ctx, "sdk", "php", "bump", "--version="+version, "export", "--path=.")
}
