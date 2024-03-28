package sdk

import (
	"context"
	"os"
	"strconv"

	"github.com/dagger/dagger/internal/mage/util"
	"github.com/magefile/mage/mg"
)

type Elixir mg.Namespace

var _ SDK = Elixir{}

// Lint lints the Elixir SDK
func (Elixir) Lint(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "elixir", "lint")
}

// Test tests the Elixir SDK
func (Elixir) Test(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "elixir", "test")
}

// Generate re-generates the SDK API
func (Elixir) Generate(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "elixir", "generate", "export", "--path=.")
}

// Publish publishes the Elixir SDK
func (Elixir) Publish(ctx context.Context, tag string) error {
	args := []string{"sdk", "go", "publish", "--tag=" + tag}

	if dryRun, _ := strconv.ParseBool(os.Getenv("DRY_RUN")); dryRun {
		args = append(args, "--dry-run=true")
	}

	if _, ok := os.LookupEnv("HEX_API_KEY"); ok {
		args = append(args, "--hex-api-key=env:HEX_API_KEY")
	}

	return util.DaggerCall(ctx, args...)
}

// Bump the Elixir SDK's Engine dependency
func (Elixir) Bump(ctx context.Context, engineVersion string) error {
	return util.DaggerCall(ctx, "sdk", "go", "bump", "--version="+engineVersion, "export", "--path=.")
}
