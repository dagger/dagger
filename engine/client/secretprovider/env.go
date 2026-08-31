package secretprovider

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/slog"
)

// EnvRefresher is an optional hook, consulted by envProvider before it reads a
// requested variable from the process environment. It lets a higher layer keep
// a dynamically-managed env var (e.g. a short-lived OAuth bearer token) fresh:
// the hook may refresh the credential and update os.Setenv for name before the
// value is read. force is set by the internal forceRefresh URI query flag when
// a credential consumer knows the current value was rejected. Refresh is
// best-effort for ordinary lookups — a nil hook is ignored, and envProvider
// still reads whatever value is present after a failed refresh — but a forced
// refresh propagates failure rather than returning a token known to be stale.
// Failures are logged and recorded on the current span either way.
//
// This indirection avoids a dependency from this low-level provider package on
// the CLI's llmconfig package (which owns OAuth refresh); the CLI registers the
// hook at startup via RegisterEnvRefresher.
type EnvRefresher func(ctx context.Context, name string, force bool) error

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

func envProvider(ctx context.Context, nameWithQuery string) ([]byte, error) {
	name, rawQuery, _ := strings.Cut(nameWithQuery, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, fmt.Errorf("parse env secret options: %w", err)
	}
	forceRefresh := false
	if raw := query.Get("forceRefresh"); raw != "" {
		forceRefresh, err = strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("parse env secret forceRefresh: %w", err)
		}
	}
	// Give a registered refresher a chance to update this var before we read it
	// (e.g. refreshing an expired OAuth token). Best-effort: on error we still
	// read whatever is currently set, which yields the usual not-found or
	// (possibly stale) value rather than masking the original request.
	if r := currentEnvRefresher(); r != nil {
		if err := r(ctx, name, forceRefresh); err != nil {
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
			// Ordinary freshness checks are best-effort and may still use the
			// currently exported token. A forced refresh carries definitive
			// rejection evidence, so returning that same stale token would only
			// create a retry loop; surface the refresh error instead.
			if forceRefresh {
				return nil, fmt.Errorf("force refresh secret env var %q: %w", redactEnvName(name), err)
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
