package sdk

import (
	"context"

	"github.com/dagger/dagger/engine/slog"
)

func toolchainDebug(ctx context.Context, msg string, args ...any) {
	slog.WarnContext(ctx, msg, args...)
}
