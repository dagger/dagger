package gitutil

import (
	"maps"
	"slices"
	"strconv"
	"strings"
)

// urlRewriteEnv holds the GIT_CONFIG_* entries for the engine-wide URL
// rewrites set by SetURLRewrites. It is set once at engine startup.
var urlRewriteEnv []string

// urlRewrites maps each insteadOf prefix set by SetURLRewrites to the base URL
// that replaces it, for code that reaches remotes without the git CLI.
var urlRewrites map[string]string

// SetURLRewrites makes every git command apply url.<base>.insteadOf for each
// base URL and the URL prefixes it replaces. Git itself picks the longest
// matching prefix. It must be called before any git command runs.
func SetURLRewrites(rewrites map[string][]string) {
	var env []string
	prefixes := map[string]string{}
	n := 0
	for _, base := range slices.Sorted(maps.Keys(rewrites)) {
		for _, insteadOf := range rewrites[base] {
			env = append(env,
				"GIT_CONFIG_KEY_"+strconv.Itoa(n)+"=url."+base+".insteadOf",
				"GIT_CONFIG_VALUE_"+strconv.Itoa(n)+"="+insteadOf,
			)
			prefixes[insteadOf] = base
			n++
		}
	}
	if n > 0 {
		env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(n))
	}
	urlRewriteEnv = env
	urlRewrites = prefixes
}

// RewriteURL applies the rewrites set by SetURLRewrites to remote as git does:
// the longest matching prefix is replaced with its base URL. A remote no
// prefix matches is returned unchanged.
func RewriteURL(remote string) string {
	longest := ""
	for prefix := range urlRewrites {
		if len(prefix) > len(longest) && strings.HasPrefix(remote, prefix) {
			longest = prefix
		}
	}
	if longest == "" {
		return remote
	}
	return urlRewrites[longest] + strings.TrimPrefix(remote, longest)
}
