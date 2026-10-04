package main

import "crypto/rand"

func engineStateCacheKey(name string, shared bool) string {
	key := "dagger-dev-engine-state"
	if shared {
		return key
	}

	// Engines built from the same revision can run at the same time. Give
	// each service its own state directory unless sharing was requested.
	key += "-" + rand.Text()
	if name != "" {
		key += "-" + name
	}
	return key
}
