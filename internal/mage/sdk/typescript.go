package sdk

import (
	"context"
	"os"
	"strconv"

	"github.com/magefile/mage/mg"

	"github.com/dagger/dagger/internal/mage/util"
)

type TypeScript mg.Namespace

var _ SDK = TypeScript{}

// Lint lints the TypeScript SDK
func (t TypeScript) Lint(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "typescript", "lint")
}

// Test tests the TypeScript SDK
func (t TypeScript) Test(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "typescript", "test")
}

// Generate re-generates the SDK API
func (t TypeScript) Generate(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "typescript", "generate", "export", "--path=.")
}

// Publish publishes the TypeScript SDK
func (t TypeScript) Publish(ctx context.Context, tag string) error {
	args := []string{"sdk", "typescript", "publish", "--tag=" + tag}

	if dryRun, _ := strconv.ParseBool(os.Getenv("DRY_RUN")); dryRun {
		args = append(args, "--dry-run=true")
	}

	if _, ok := os.LookupEnv("NPM_TOKEN"); ok {
		args = append(args, "--npm-token=env:NPM_TOKEN")
	}

	return util.DaggerCall(ctx, args...)
}

// Bump the TypeScript SDK's Engine dependency
func (t TypeScript) Bump(ctx context.Context, version string) error {
	return util.DaggerCall(ctx, "sdk", "typescript", "bump", "--version="+version, "export", "--path=.")
}
