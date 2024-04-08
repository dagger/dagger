package sdk

import (
	"context"
	"os"
	"strconv"

	"github.com/magefile/mage/mg"

	"github.com/dagger/dagger/ci/mage/util"
)

type Python mg.Namespace

var _ SDK = Python{}

// Lint lints the Python SDK
func (t Python) Lint(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "python", "lint")
}

// Test tests the Python SDK
func (t Python) Test(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "python", "test")
}

// Generate re-generates the SDK API
func (t Python) Generate(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "python", "generate", "export", "--path=.")
}

// Publish publishes the Python SDK
func (t Python) Publish(ctx context.Context, tag string) error {
	args := []string{"sdk", "python", "publish", "--tag=" + tag}

	if dryRun, _ := strconv.ParseBool(os.Getenv("DRY_RUN")); dryRun {
		args = append(args, "--dry-run=true")
	}

	if v, ok := os.LookupEnv("PYPI_REPO"); ok {
		args = append(args, "--pypi-repo="+v)
	}
	if _, ok := os.LookupEnv("PYPI_TOKEN"); ok {
		args = append(args, "--pypi-token=env:PYPI_TOKEN")
	}

	return util.DaggerCall(ctx, args...)
}

// Bump the Python SDK's Engine dependency
func (t Python) Bump(ctx context.Context, version string) error {
	return util.DaggerCall(ctx, "sdk", "python", "bump", "--version="+version, "export", "--path=.")
}
