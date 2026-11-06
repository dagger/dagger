package core

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/dagger/dagger/core/gitref"
)

// configuredModuleRedirects holds the engine config's moduleRedirects: module
// URLs pinned to the URLs they should resolve to, so they resolve without
// reaching the redirecting host (e.g. on engines that can reach GitHub, or a
// mirror of it, but not dagger.io). Keys are host+path prefixes; a sub-path
// below a key (dagger.io/sdk/python/runtime) carries over to the target, as
// dagger.io does. A target may itself be a key, so a redirect can chain:
// dagger.io/sdk/python to github.com/dagger/python-sdk to a mirror of it. It
// is set once at engine startup.
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
	for name := range pinned {
		if _, err := followModuleRedirects(pinned, gitref.SchemeHTTPS.Prefix()+name); err != nil {
			return err
		}
	}
	configuredModuleRedirects = pinned
	return nil
}

// followConfiguredModuleRedirects applies the configured redirects to
// sourceURL, a module URL without a version, until none matches. It returns
// the result as an https URL, and false if no redirect matched.
func followConfiguredModuleRedirects(sourceURL string) (string, bool) {
	if !strings.HasPrefix(sourceURL, gitref.SchemeHTTPS.Prefix()) {
		sourceURL = gitref.SchemeHTTPS.Prefix() + sourceURL
	}
	resolved, err := followModuleRedirects(configuredModuleRedirects, sourceURL)
	if err != nil || resolved == sourceURL {
		return "", false
	}
	return resolved, true
}

// followModuleRedirects applies redirects to sourceURL, an https module URL
// without a version, until none matches. More hops than there are redirects
// means one of them matches its own result.
func followModuleRedirects(redirects map[string]string, sourceURL string) (string, error) {
	resolved := sourceURL
	for range len(redirects) + 1 {
		u, err := url.Parse(resolved)
		if err != nil {
			return "", err
		}
		target, ok := moduleRedirect(redirects, u)
		if !ok {
			return resolved, nil
		}
		resolved = target
	}
	return "", fmt.Errorf("module redirects loop from %q", strings.TrimPrefix(sourceURL, gitref.SchemeHTTPS.Prefix()))
}

// configuredModuleRedirect returns the pinned redirect target for u, an https
// module URL without a version, as an https URL. It takes a single hop.
func configuredModuleRedirect(u *url.URL) (string, bool) {
	return moduleRedirect(configuredModuleRedirects, u)
}

func moduleRedirect(redirects map[string]string, u *url.URL) (string, bool) {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return "", false
	}
	key := strings.ToLower(u.Hostname()) + strings.TrimSuffix(u.Path, "/")
	for prefix, target := range redirects {
		if subpath, ok := strings.CutPrefix(key, prefix); ok && (subpath == "" || strings.HasPrefix(subpath, "/")) {
			return gitref.SchemeHTTPS.Prefix() + target + subpath, true
		}
	}
	return "", false
}
