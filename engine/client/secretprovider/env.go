package secretprovider

import (
	"context"
	"fmt"
	"os"
	"sync"
)

// EnvRefresher is an optional hook, consulted by envProvider before it reads a
// requested variable from the process environment. It lets a higher layer keep
// a dynamically-managed env var (e.g. a short-lived OAuth bearer token) fresh:
// the hook may refresh the credential and update os.Setenv for name before the
// value is read. It is best-effort — errors and a nil hook are ignored, and
// envProvider still reads whatever value is present afterwards.
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

func envProvider(ctx context.Context, name string) ([]byte, error) {
	// Give a registered refresher a chance to update this var before we read it
	// (e.g. refreshing an expired OAuth token). Best-effort: on error we still
	// read whatever is currently set, which yields the usual not-found or
	// (possibly stale) value rather than masking the original request.
	if r := currentEnvRefresher(); r != nil {
		_ = r(ctx, name)
	}
	v, ok := os.LookupEnv(name)
	if !ok {
		// Don't show the entire env var name, in case the user accidentally passed the value instead...
		// This is important because users originally *did* have to pass the value, before we changed to
		// passing by name instead.
		if len(name) >= 4 {
			name = name[:3] + "..."
		}
		return nil, fmt.Errorf("secret env var not found: %q", name)
	}
	return []byte(v), nil
}
