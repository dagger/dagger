package dagql

import "context"

type internalKey struct{}
type cacheBusterKey struct{}

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

// isNonInternal returns whether the internal flag has been explicitly set to
// false in the context.
func isNonInternal(ctx context.Context) bool {
	if val := ctx.Value(internalKey{}); val != nil {
		return !val.(bool)
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

// WithCacheBuster returns a new context with the cache-buster value set.
//
// Callers can use this to isolate a subtree of dagql execution onto a distinct
// cache branch without clearing or disabling shared cache entries globally.
func WithCacheBuster(ctx context.Context, cacheBuster string) context.Context {
	return context.WithValue(ctx, cacheBusterKey{}, cacheBuster)
}

// CacheBuster returns the cache-buster value from the context, if any.
func CacheBuster(ctx context.Context) string {
	return cacheBusterFromContext(ctx)
}

func cacheBusterFromContext(ctx context.Context) string {
	if val := ctx.Value(cacheBusterKey{}); val != nil {
		return val.(string)
	}
	return ""
}
