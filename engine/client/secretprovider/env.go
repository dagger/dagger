package secretprovider

import (
	"context"
	"fmt"
	"os"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"

	"github.com/dagger/dagger/engine/slog"
)

// EnvRefresher is an optional hook, consulted by envProvider before it reads a
// requested variable from the process environment. It lets a higher layer keep
// a dynamically-managed env var (e.g. a short-lived OAuth bearer token) fresh:
// the hook may refresh the credential and update os.Setenv for name before the
// value is read. It is best-effort unless the consumer has explicitly rejected
// the old value: a nil hook is ignored, and otherwise failures are logged and
// recorded on the current span. A rejected value must not mask a refresh error.
//
// This indirection avoids a dependency from this low-level provider package on
// the CLI's llmconfig package (which owns OAuth refresh); the CLI registers the
// hook at startup via RegisterEnvRefresher.
type EnvRefresher func(ctx context.Context, name string) error

var (
	envRefresherMu sync.RWMutex
	envRefresher   EnvRefresher
)

// RegisterEnvRefresher installs the hook consulted by envProvider. Passing nil
// clears it. It is safe to call concurrently.
func RegisterEnvRefresher(r EnvRefresher) {
	envRefresherMu.Lock()
	defer envRefresherMu.Unlock()
	envRefresher = r
}

func currentEnvRefresher() EnvRefresher {
	envRefresherMu.RLock()
	defer envRefresherMu.RUnlock()
	return envRefresher
}

// rejectedEnvValueMetadata carries only a SHA-256 fingerprint, never the secret
// itself. A live credential consumer uses it to report a rejected value so the
// owning client can refresh it even when its recorded expiry is still ahead.
const rejectedEnvValueMetadata = "x-dagger-rejected-env-value-sha256"

// ContextWithRejectedEnvValue marks a secret lookup with the SHA-256 hex
// fingerprint of a value its consumer has rejected. An empty fingerprint clears
// a prior rejection, including one inherited from incoming RPC metadata.
func ContextWithRejectedEnvValue(ctx context.Context, fingerprint string) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set(rejectedEnvValueMetadata, fingerprint)
	return metadata.NewOutgoingContext(ctx, md)
}

// RejectedEnvValue returns the rejected value's fingerprint at either end of
// a secret RPC. Outgoing metadata takes precedence to allow clearing it.
func RejectedEnvValue(ctx context.Context) string {
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		if values := md.Get(rejectedEnvValueMetadata); len(values) > 0 {
			return values[0]
		}
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get(rejectedEnvValueMetadata); len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func envProvider(ctx context.Context, name string) ([]byte, error) {
	// Give a registered refresher a chance to update this var before we read it
	// (e.g. refreshing an expired OAuth token). Best-effort: on error we still
	// read whatever is currently set, which yields the usual not-found or
	// (possibly stale) value rather than masking the original request.
	if r := currentEnvRefresher(); r != nil {
		if err := r(ctx, name); err != nil {
			// Don't degrade silently, though: a revoked credential or an
			// unwritable config otherwise surfaces much later as an
			// unexplained 401 from the provider. The span event puts it in
			// `dagger trace`, where the failing resolution actually is.
			slog.WarnContext(ctx, "failed to refresh secret env var",
				"name", redactEnvName(name), "error", err)
			trace.SpanFromContext(ctx).AddEvent("secret env var refresh failed",
				trace.WithAttributes(
					attribute.String("env.name", redactEnvName(name)),
					attribute.String("error", err.Error()),
				))
			if RejectedEnvValue(ctx) != "" {
				// The consumer already knows this value is bad. Returning it
				// again would hide a refresh failure behind another HTTP 401.
				return nil, fmt.Errorf("refresh rejected secret env var: %w", err)
			}
		}
	}
	v, ok := os.LookupEnv(name)
	if !ok {
		return nil, fmt.Errorf("secret env var not found: %q", redactEnvName(name))
	}
	return []byte(v), nil
}

// redactEnvName shortens a requested variable name for display. Users
// originally had to pass the secret *value* here rather than its name, and
// some still do by accident, so the name itself may be a credential.
func redactEnvName(name string) string {
	if len(name) >= 4 {
		return name[:3] + "..."
	}
	return name
}
