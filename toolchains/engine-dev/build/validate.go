package build

import (
	"context"

	"github.com/containerd/platforms"

	"dagger/engine-dev/internal/dagger"
)

// ValidateTypescriptSDKContent validates that the TypeScript SDK content build
// can be produced from the provided source.
//
// This intentionally bypasses version/tag resolution: those values are only
// used for engine binary metadata and do not affect TypeScript SDK content.
func ValidateTypescriptSDKContent(ctx context.Context, source *dagger.Directory) error {
	builder := &Builder{
		source:   source,
		platform: dagger.Platform(platforms.DefaultString()),
	}
	_, err := builder.typescriptSDKContent(ctx)
	return err
}
