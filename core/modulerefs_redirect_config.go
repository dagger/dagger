package core

import (
	"fmt"
	"net/url"
	"strings"
)

// configuredModuleRedirects holds the engine config's moduleRedirects: module
// URLs pinned to the URLs their dagger-get redirect points to, so they
// resolve without reaching the redirecting host (e.g. on engines that can
// reach GitHub, possibly through a git url.insteadOf rewrite, but not
// dagger.io). Keys are host+path prefixes; a sub-path below a key
// (dagger.io/sdk/python/runtime) carries over to the target, as dagger.io
// does. It is set once at engine startup.
var configuredModuleRedirects map[string]string

// SetModuleRedirects pins each module URL, a host and path such as
// dagger.io/sdk/go, to the URL it redirects to, such as
// github.com/dagger/go-sdk. It must be called before any module resolves.
func SetModuleRedirects(redirects map[string]string) error {
	pinned := make(map[string]string, len(redirects))
	for name, target := range redirects {
		for _, s := range []string{name, target} {
			if s == "" || strings.ContainsAny(s, "@?#") || strings.Contains(s, "://") {
				return fmt.Errorf("module redirect %q -> %q: give a host and path, without scheme, version, or query", name, target)
			}
		}
		host, path, _ := strings.Cut(strings.TrimSuffix(name, "/"), "/")
		pinned[strings.ToLower(host)+"/"+path] = strings.TrimSuffix(target, "/")
	}
	configuredModuleRedirects = pinned
	return nil
}

// configuredModuleRedirect returns the pinned redirect target for u, an https
// module URL without a version, as an https URL.
func configuredModuleRedirect(u *url.URL) (string, bool) {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return "", false
	}
	key := strings.ToLower(u.Hostname()) + strings.TrimSuffix(u.Path, "/")
	for prefix, target := range configuredModuleRedirects {
		if subpath, ok := strings.CutPrefix(key, prefix); ok && (subpath == "" || strings.HasPrefix(subpath, "/")) {
			return "https://" + target + subpath, true
		}
	}
	return "", false
}
