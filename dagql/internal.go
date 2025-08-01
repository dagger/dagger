package dagql

import "context"

type internalKey struct{}

// withInternal returns a new context with the internal flag set.
//
// This is used for analytics so that we can distinguish between calls made by
// an end user and calls made within the engine, for example to SDK modules.
func withInternal(ctx context.Context) context.Context {
	return context.WithValue(ctx, internalKey{}, true)
}

// IsInternal returns whether the internal flag is set in the context.
func IsInternal(ctx context.Context) bool {
	if val := ctx.Value(internalKey{}); val != nil {
		return val.(bool)
	}
	return false
}

type skipKey struct{}

func WithSkip(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipKey{}, true)
}

func IsSkipped(ctx context.Context) bool {
	if val := ctx.Value(skipKey{}); val != nil {
		return val.(bool)
	}
	return false
}
