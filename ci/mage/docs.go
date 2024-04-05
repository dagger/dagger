package mage

import (
	"context"

	"github.com/magefile/mage/mg"

	"github.com/dagger/dagger/ci/mage/util"
)

type Docs mg.Namespace

// Lint lints documentation files
func (d Docs) Lint(ctx context.Context) error {
	return util.DaggerCall(ctx, "docs", "lint")
}

// Generate re-generates the API schema and CLI reference
func (d Docs) Generate(ctx context.Context) error {
	return util.DaggerCall(ctx, "docs", "generate", "export", "--path=.")
}

// GenerateSdl re-generates the API schema
func (d Docs) GenerateSdl(ctx context.Context) error {
	return util.DaggerCall(ctx, "docs", "generate-sdl", "export", "--path=.")
}

// GenerateCli re-generates the CLI reference documentation
func (d Docs) GenerateCli(ctx context.Context) error {
	return util.DaggerCall(ctx, "docs", "generate-cli", "export", "--path=.")
}
