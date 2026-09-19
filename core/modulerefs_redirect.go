package core

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dagger/dagger/core/gitref"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
)

const (
	// daggerGetQueryParam is the query flag appended to a module ref to probe
	// for a redirect that points at the real module source.
	daggerGetQueryParam = "dagger-get"

	// daggerVersionQueryParam carries the requested version in the probe. A
	// host that rewrites versions returns the selected version in the same
	// param of the Location. A host that passes the query through unchanged
	// returns the requested version, which is an identity rewrite.
	daggerVersionQueryParam = "dagger-version"

	// daggerGetProbeTimeout bounds the redirect probe so a slow or hanging host
	// cannot block module resolution.
	daggerGetProbeTimeout = 5 * time.Second
)

// daggerGetClient issues the redirect probe. It never follows redirects itself:
// we must read the Location header and rewrite it (stripping dagger-get,
// re-appending any version) before continuing resolution.
var daggerGetClient = &http.Client{
	Timeout: daggerGetProbeTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// daggerGetResult is the outcome of a redirect probe.
type daggerGetResult struct {
	// SourceURL is the redirect destination without the probe params, or the
	// probed source URL when no redirect applies.
	SourceURL string
	// Version is the version that the host selected with dagger-version. It is
	// empty when the host did not select one.
	Version string
}

type vanityURLLookupLockKey struct{}

// ContextWithVanityURLLookupLock makes an explicitly loaded workspace lock
// available while resolving module sources for a workspace overlay.
func ContextWithVanityURLLookupLock(ctx context.Context, lock *workspace.Lock) context.Context {
	return context.WithValue(ctx, vanityURLLookupLockKey{}, lock)
}

// ResolveDaggerGetRedirect resolves a dagger-get vanity URL for any Git-backed
// source consumer, including both modules and workspaces. It first consults the
// workspace lockfile, then falls back to the session-cached HTTP probe.
func ResolveDaggerGetRedirect(ctx context.Context, refString string) (string, error) {
	if !daggerGetEligible(refString) {
		return refString, nil
	}

	sourceURL, version, err := splitSourceURLVersion(refString)
	if err != nil {
		return refString, nil //nolint:nilerr // deliberate: unparseable refs fall back to the original ref
	}

	lock, lockOverridden := ctx.Value(vanityURLLookupLockKey{}).(*workspace.Lock)
	var setLookup func(string, string, []any, string) error
	query, queryErr := CurrentQuery(ctx)
	if lockOverridden {
		setLookup = lock.SetLookup
	} else if queryErr == nil {
		var ok bool
		lock, ok, err = query.CurrentWorkspaceLock(ctx, false)
		if err != nil {
			return "", fmt.Errorf("vanity-url lockfile: %w", err)
		}
		if !ok {
			lock = nil
		}
	}

	urlInputs := []any{sourceURL}
	versionInputs := []any{sourceURL, version}
	lockedURL := ""
	if lock != nil {
		resolvedURL, urlLocked := lock.GetLookup(
			workspace.CoreLockNamespace,
			workspace.LockOperationVanityURL,
			urlInputs,
		)
		resolvedVersion, versionLocked := lock.GetLookup(
			workspace.CoreLockNamespace,
			workspace.LockOperationVanityVersion,
			versionInputs,
		)
		if !urlLocked {
			resolvedURL = sourceURL
		}
		// A ref without a version has a version entry only if the host supplied
		// a default. Its absence does not require a new probe.
		if versionLocked || (urlLocked && version == "") {
			if resolvedVersion == "" {
				resolvedVersion = version
			}
			return sourceURLWithVersion(resolvedURL, resolvedVersion), nil
		}
		if urlLocked {
			// The URL is locked but this version is not: probe for the version
			// only, and keep the locked URL.
			lockedURL = resolvedURL
		}
		if !lockOverridden && queryErr == nil {
			_, lockWritable, err := query.CurrentWorkspaceLock(ctx, true)
			if err != nil {
				return "", fmt.Errorf("vanity-url lockfile: %w", err)
			}
			if lockWritable {
				setLookup = func(namespace, operation string, inputs []any, value string) error {
					return query.SetCurrentWorkspaceLookup(ctx, namespace, operation, inputs, value)
				}
			}
		}
	}

	cache, cacheErr := dagql.EngineCache(ctx)
	clientMetadata, mdErr := engine.ClientMetadataFromContext(ctx)
	if cacheErr != nil || mdErr != nil {
		// No session infrastructure means no probe: keep parsing network-free.
		// A locked URL still applies, with the caller's version unchanged.
		if lockedURL != "" {
			return sourceURLWithVersion(lockedURL, version), nil
		}
		return refString, nil //nolint:nilerr // deliberate: see above
	}

	res, err := cache.GetOrInitArbitrary(
		ctx,
		clientMetadata.SessionID,
		// Scope the cached value to the session: GetOrInitArbitrary looks entries
		// up by call key alone (the session ID only tracks ownership), so the
		// session ID must be part of the key to keep results session-private.
		// The host can rewrite each version differently, so the version is part
		// of the key too.
		"module-dagger-get-redirect:"+clientMetadata.SessionID+":"+sourceURL+"@"+version,
		func(ctx context.Context) (any, error) {
			return daggerGetProbeVersion(ctx, sourceURL, version), nil
		},
	)
	var result daggerGetResult
	if err != nil {
		slog.Debug("dagger-get redirect cache error; probing directly", "ref", refString, "error", err)
		result = daggerGetProbeVersion(ctx, sourceURL, version)
	} else if cached, ok := res.Value().(daggerGetResult); ok && cached.SourceURL != "" {
		result = cached
	} else {
		result = daggerGetResult{SourceURL: sourceURL}
	}

	if lockedURL == "" && result.SourceURL == sourceURL && result.Version == "" {
		return refString, nil
	}
	resolvedURL := result.SourceURL
	if lockedURL != "" {
		resolvedURL = lockedURL
	}
	resolvedVersion := result.Version
	if resolvedVersion == "" {
		resolvedVersion = version
	}
	if setLookup == nil {
		return sourceURLWithVersion(resolvedURL, resolvedVersion), nil
	}
	// Store the destination before applying the caller's version. A version in
	// the redirect is its default and must survive later lookups and refreshes.
	if lockedURL == "" && result.SourceURL != sourceURL {
		if err := setLookup(
			workspace.CoreLockNamespace,
			workspace.LockOperationVanityURL,
			urlInputs,
			result.SourceURL,
		); err != nil {
			return "", fmt.Errorf("set vanity-url lock entry: %w", err)
		}
	}
	// Store the version even if the host did not change it, so that a later
	// load does not probe again.
	if resolvedVersion != "" {
		if err := setLookup(
			workspace.CoreLockNamespace,
			workspace.LockOperationVanityVersion,
			versionInputs,
			resolvedVersion,
		); err != nil {
			return "", fmt.Errorf("set vanity-version lock entry: %w", err)
		}
	}
	return sourceURLWithVersion(resolvedURL, resolvedVersion), nil
}

func splitSourceURLVersion(refString string) (string, string, error) {
	normalized := strings.Replace(refString, "#", "@", 1)
	if !strings.HasPrefix(normalized, gitref.SchemeHTTPS.Prefix()) {
		normalized = gitref.SchemeHTTPS.Prefix() + normalized
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return "", "", err
	}
	version := ""
	if i := strings.Index(u.Path, "@"); i >= 0 {
		version = u.Path[i+1:]
		u.Path = u.Path[:i]
	}
	// Keep the scheme so schemeless and HTTPS refs share one lockfile key.
	return u.String(), version, nil
}

func sourceURLWithVersion(sourceURL, version string) string {
	if version == "" {
		return sourceURL
	}
	schemeless := !strings.HasPrefix(sourceURL, gitref.SchemeHTTPS.Prefix())
	parsedURL := sourceURL
	if schemeless {
		parsedURL = gitref.SchemeHTTPS.Prefix() + parsedURL
	}
	u, err := url.Parse(parsedURL)
	if err != nil {
		return sourceURL + "@" + version
	}
	// An explicit caller version overrides a default supplied by the redirect.
	u.Fragment = ""
	if i := strings.Index(u.Path, "@"); i >= 0 {
		u.Path = u.Path[:i]
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "@" + version
	resolved := u.String()
	if schemeless {
		resolved = strings.TrimPrefix(resolved, gitref.SchemeHTTPS.Prefix())
	}
	return resolved
}

// daggerGetEligible reports whether the redirect probe applies to refString.
// Only https and schemeless refs qualify; local paths, SCP-like refs, and
// explicit non-https schemes (http, ssh, git) are excluded.
func daggerGetEligible(refString string) bool {
	if refString == "" || refString[0] == '/' || refString[0] == '.' {
		return false
	}
	if strings.Contains(refString, "://") && !strings.HasPrefix(refString, gitref.SchemeHTTPS.Prefix()) {
		return false
	}
	switch gitref.Scheme(refString) {
	case gitref.SchemeHTTPS:
		return true
	case gitref.NoScheme:
		// Schemeless refs are attempted over https; require a hostname with a
		// dot so we don't probe obvious non-URLs.
		host := refString
		if i := strings.IndexAny(host, "/@:"); i >= 0 {
			host = host[:i]
		}
		return strings.Contains(host, ".")
	default:
		return false
	}
}

// daggerGetProbe probes a ref that can carry a version. It returns the
// resolved ref, or the original ref on any non-redirect outcome.
func daggerGetProbe(ctx context.Context, refString string) string {
	sourceURL, version, err := splitSourceURLVersion(refString)
	if err != nil {
		return refString
	}
	result := daggerGetProbeVersion(ctx, sourceURL, version)
	if result.SourceURL == sourceURL && result.Version == "" {
		return refString
	}
	if result.Version != "" {
		version = result.Version
	}
	return sourceURLWithVersion(result.SourceURL, version)
}

// daggerGetProbeVersion performs the actual single-hop redirect probe for a
// source URL and a requested version. On any non-redirect outcome the result
// has the original source URL and no version.
func daggerGetProbeVersion(ctx context.Context, sourceURL, version string) daggerGetResult {
	noRedirect := daggerGetResult{SourceURL: sourceURL}

	normalized := sourceURL
	if !strings.HasPrefix(normalized, gitref.SchemeHTTPS.Prefix()) {
		normalized = gitref.SchemeHTTPS.Prefix() + normalized
	}
	u, err := url.Parse(normalized)
	if err != nil {
		return noRedirect
	}

	probe := *u
	probeQuery := url.Values{}
	probeQuery.Set(daggerGetQueryParam, "1")
	// Always send the param, even when empty: it tells the host that this
	// engine removes dagger-version from the Location.
	probeQuery.Set(daggerVersionQueryParam, version)
	probe.RawQuery = probeQuery.Encode()
	probe.Fragment = ""

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe.String(), nil)
	if err != nil {
		return noRedirect
	}
	resp, err := daggerGetClient.Do(req)
	if err != nil {
		slog.Debug("dagger-get probe failed; using original ref", "url", probe.String(), "error", err)
		return noRedirect
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusMultipleChoices || resp.StatusCode >= http.StatusBadRequest {
		// Not a 3xx: no redirect configured for this ref.
		return noRedirect
	}

	loc := resp.Header.Get("Location")
	if loc == "" {
		return noRedirect
	}
	locURL, err := url.Parse(loc)
	if err != nil || locURL.Scheme != "https" || locURL.Host == "" {
		slog.Debug("dagger-get redirect ignored: Location is not an absolute https URL",
			"ref", sourceURL, "location", loc)
		return noRedirect
	}

	// Hosts also emit incidental 3xx responses that are not dagger-get
	// opt-ins: auth walls (e.g. GitLab 302s unauthenticated repo paths to
	// /users/sign_in, dropping the query string) and same-repo URL
	// canonicalization. Require the Location to echo the dagger-get marker as
	// proof of intent: a host opting in preserves the query (standard
	// path+query passthrough redirects do), while auth-wall redirects drop it.
	q := locURL.Query()
	if q.Get(daggerGetQueryParam) != "1" {
		slog.Debug("dagger-get redirect ignored: Location does not echo the dagger-get marker",
			"ref", sourceURL, "location", loc)
		return noRedirect
	}

	// A passthrough host echoes the requested version. Only a different value
	// is a rewrite.
	hostVersion := q.Get(daggerVersionQueryParam)
	if hostVersion == version {
		hostVersion = ""
	}

	// Drop only the probe params the server may have echoed back.
	q.Del(daggerGetQueryParam)
	q.Del(daggerVersionQueryParam)
	locURL.RawQuery = q.Encode()

	// Canonicalization redirects (e.g. GitHub 301s "repo.git" -> "repo",
	// "www." -> apex, trailing-slash and case fixups) echo the query string,
	// so they pass the marker check above. Treating them as redirects would
	// silently rewrite the user's ref and change the module's clone
	// ref/identity. Only honor redirects that point somewhere genuinely
	// different. A version rewrite still applies.
	if canonicalRepoKey(u) == canonicalRepoKey(locURL) {
		slog.Debug("dagger-get redirect ignored: same-repo canonicalization",
			"ref", sourceURL, "location", loc)
		return daggerGetResult{SourceURL: sourceURL, Version: hostVersion}
	}

	result := daggerGetResult{SourceURL: locURL.String(), Version: hostVersion}
	slog.Debug("dagger-get redirect resolved", "from", sourceURL, "version", version, "to", result)
	return result
}

// canonicalRepoKey reduces a URL to a host+path key that is stable across the
// common canonicalization redirects hosts emit for the same repository:
// case-only differences, a leading "www.", a ".git" suffix, and trailing
// slashes.
func canonicalRepoKey(u *url.URL) string {
	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")
	path := strings.ToLower(u.Path)
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	return host + path
}
