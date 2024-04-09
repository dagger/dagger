package sdk

import (
	"context"
	"os"
	"strconv"

	"github.com/magefile/mage/mg"

	"github.com/dagger/dagger/ci/mage/util"
)

type Rust mg.Namespace

var _ SDK = Rust{}

// Lint lints the Rust SDK
func (r Rust) Lint(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "rust", "lint")
}

// Test tests the Rust SDK
func (r Rust) Test(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "rust", "test")
}

// Generate re-generates the SDK API
func (r Rust) Generate(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "rust", "generate", "export", "--path=.")
}

// Publish publishes the Rust SDK
func (r Rust) Publish(ctx context.Context, tag string) error {
	args := []string{"sdk", "rust", "publish", "--tag=" + tag}

	if dryRun, _ := strconv.ParseBool(os.Getenv("DRY_RUN")); dryRun {
		args = append(args, "--dry-run=true")
	}

	if _, ok := os.LookupEnv("CARGO_REGISTRY_TOKEN"); ok {
		args = append(args, "--cargo-registry-token=env:CARGO_REGISTRY_TOKEN")
	}

	return util.DaggerCall(ctx, args...)
}

// Bump the Rust SDK's Engine dependency
func (Rust) Bump(ctx context.Context, engineVersion string) error {
	return util.DaggerCall(ctx, "sdk", "rust", "bump", "--version="+engineVersion, "export", "--path=.")
}
