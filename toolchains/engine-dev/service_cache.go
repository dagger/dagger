package main

import "crypto/rand"

func engineStateCacheKey(name string, shared bool) string {
	key := "dagger-dev-engine-state"
	if shared {
		return key
	}

	// Keep unrelated service definitions in separate cache volumes. The
	// entrypoint also isolates concurrent starts of the same definition.
	key += "-" + rand.Text()
	if name != "" {
		key += "-" + name
	}
	return key
}

func isolatedEngineEntrypoint(stateDir, entrypoint string) []string {
	// A cached service definition can run in multiple sessions. Allocate the
	// root at process startup, not while constructing that definition.
	// Keep it on the cache volume so nested overlay mounts still work.
	// The volume and its runtime directories are reclaimed by cache pruning.
	return []string{"sh", "-ec", `
state_dir=$(mktemp -d "$1/engine.XXXXXXXXXX")
entrypoint=$2
shift 2
exec "$entrypoint" --root "$state_dir" "$@"
`, "dagger-dev-engine", stateDir, entrypoint}
}
