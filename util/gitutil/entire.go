package gitutil

// git-remote-entire, the helper git runs for entire:// remotes, takes its
// credential from ENTIRE_TOKEN only and keeps its state in per-user
// directories, which these variables relocate.
const (
	entireTokenEnv     = "ENTIRE_TOKEN"
	entireConfigDirEnv = "ENTIRE_CONFIG_DIR"
	entireCacheHomeEnv = "XDG_CACHE_HOME"
)

// WithEntireTokenAuth hands token to git-remote-entire for an entire://
// remote. The token is a single bearer: Entire has no username.
func WithEntireTokenAuth(remote *GitURL, token string) Option {
	if remote.Scheme != EntireProtocol {
		return func(*GitCLI) {}
	}
	return WithEnv(entireTokenEnv + "=" + token)
}

// WithEntireHelperDirs points git-remote-entire at configDir for saved logins
// and at cacheHome for its discovery cache (cluster trust and replica
// locations, which hold no credentials). An empty configDir leaves the helper
// no login to fall back on when no token is given.
func WithEntireHelperDirs(remote *GitURL, configDir, cacheHome string) Option {
	if remote.Scheme != EntireProtocol {
		return func(*GitCLI) {}
	}
	return WithEnv(entireConfigDirEnv+"="+configDir, entireCacheHomeEnv+"="+cacheHome)
}
