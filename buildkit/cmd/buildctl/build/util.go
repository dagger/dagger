package build

import (
	"os"

	"github.com/pkg/errors"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/bklog"
)

// loadGithubEnv verify that url and token attributes exists in the
// cache.
// If not, it will search for $ACTIONS_RUNTIME_TOKEN and $ACTIONS_CACHE_URL
// environments variables and add it to cache Options
// Since it works for both import and export
func loadGithubEnv(cache client.CacheOptionsEntry) (client.CacheOptionsEntry, error) {
	if _, ok := cache.Attrs["url"]; !ok {
		url, ok := os.LookupEnv("ACTIONS_CACHE_URL")
		if !ok {
			return cache, errors.New("cache with type gha requires url parameter or $ACTIONS_CACHE_URL")
		}
		cache.Attrs["url"] = url
	}

	if _, ok := cache.Attrs["token"]; !ok {
		token, ok := os.LookupEnv("ACTIONS_RUNTIME_TOKEN")
		if !ok {
			return cache, errors.New("cache with type gha requires token parameter or $ACTIONS_RUNTIME_TOKEN")
		}
		cache.Attrs["token"] = token
	}
	return cache, nil
}

// loadOptEnv loads opt values from the environment.
// The returned map is always non-nil.
func loadOptEnv() map[string]string {
	m := make(map[string]string)
	propagatableEnvs := []string{"SOURCE_DATE_EPOCH"}
	for _, env := range propagatableEnvs {
		if v, ok := os.LookupEnv(env); ok {
			bklog.L.Debugf("Propagating %s from the client env to the build arg", env)
			m["build-arg:"+env] = v
		}
	}
	return m
}
