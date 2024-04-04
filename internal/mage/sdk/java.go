package sdk

import (
	"context"
	"os"
	"strconv"

	"github.com/magefile/mage/mg"

	"github.com/dagger/dagger/internal/mage/util"
)

type Java mg.Namespace

var _ SDK = Java{}

// Lint lints the Java SDK
func (Java) Lint(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "java", "lint")
}

// Test tests the Java SDK
func (Java) Test(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "java", "test")
}

// Generate re-generates the SDK API
func (Java) Generate(ctx context.Context) error {
	return util.DaggerCall(ctx, "sdk", "java", "generate", "export", "--path=.")
}

// Publish publishes the Java SDK
func (Java) Publish(ctx context.Context, tag string) error {
	args := []string{"sdk", "java", "publish", "--tag=" + tag}

	if dryRun, _ := strconv.ParseBool(os.Getenv("DRY_RUN")); dryRun {
		args = append(args, "--dry-run=true")
	}

	return util.DaggerCall(ctx, args...)
}

// Bump the Java SDK's Engine dependency
func (Java) Bump(ctx context.Context, engineVersion string) error {
	return util.DaggerCall(ctx, "sdk", "java", "bump", "--version="+engineVersion, "export", "--path=.")
}
