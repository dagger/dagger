package gitutil

import (
	"maps"
	"slices"
	"strconv"
)

// urlRewriteEnv holds the GIT_CONFIG_* entries for the engine-wide URL
// rewrites set by SetURLRewrites. It is set once at engine startup.
var urlRewriteEnv []string

// SetURLRewrites makes every git command apply url.<base>.insteadOf for each
// base URL and the URL prefixes it replaces. Git itself picks the longest
// matching prefix. It must be called before any git command runs.
func SetURLRewrites(rewrites map[string][]string) {
	var env []string
	n := 0
	for _, base := range slices.Sorted(maps.Keys(rewrites)) {
		for _, insteadOf := range rewrites[base] {
			env = append(env,
				"GIT_CONFIG_KEY_"+strconv.Itoa(n)+"=url."+base+".insteadOf",
				"GIT_CONFIG_VALUE_"+strconv.Itoa(n)+"="+insteadOf,
			)
			n++
		}
	}
	if n > 0 {
		env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(n))
	}
	urlRewriteEnv = env
}
