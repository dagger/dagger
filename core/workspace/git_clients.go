package workspace

import (
	"context"
	"path"
	"strconv"
	"strings"
)

// TreeFileReader reads a file at a path relative to the root of a module's
// own tree. found is false when there is no regular file there.
type TreeFileReader func(rel string) (data []byte, found bool, err error)

// DeclaredGitClients reads the git clients the governing config declares for
// the module at moduleDir, a path relative to the root of its tree. The
// config is the nearest dagger.toml between moduleDir and the root; with
// none, nothing is declared.
func DeclaredGitClients(ctx context.Context, moduleDir string, read TreeFileReader) ([]string, error) {
	moduleDir = path.Clean(moduleDir)
	for dir := moduleDir; ; dir = path.Dir(dir) {
		data, found, err := read(path.Join(dir, ConfigFileName))
		if err != nil {
			return nil, err
		}
		if found {
			scope := moduleDir
			switch {
			case dir == moduleDir:
				scope = "."
			case dir != ".":
				scope = strings.TrimPrefix(moduleDir, dir+"/")
			}
			cfg, err := ParseConfigAt(ctx, data, ".")
			if err != nil {
				return nil, nil //nolint:nilerr // deliberate: a config that does not parse declares nothing
			}
			return ModuleScopeGitClients(cfg, ".")[scope], nil
		}
		if dir == "." {
			return nil, nil
		}
	}
}

// GitClientKey names the repository path, and the directory in it, that a
// git module address points at. Scheme, user, version, a "#ref" selector and
// ".git" suffixes do not change it, nor does the host's case. It reads the
// address alone: parsing it the way a load does can look the host up over the
// network.
func GitClientKey(address string) string {
	rest := address
	scheme, after, hasScheme := strings.Cut(rest, "://")
	if hasScheme && !strings.ContainsAny(scheme, "/@:") {
		rest = after
	}
	if at := strings.IndexByte(rest, '@'); at >= 0 && at < strings.IndexAny(rest+"/", "/:") {
		rest = rest[at+1:]
	}
	if colon := strings.IndexByte(rest, ':'); colon >= 0 && colon < strings.IndexAny(rest+"/", "/") {
		if port, _, _ := strings.Cut(rest[colon+1:], "/"); !isNumber(port) {
			rest = rest[:colon] + "/" + rest[colon+1:]
		}
	}
	if base, selector, ok := strings.Cut(rest, "#"); ok {
		rest = base
		if _, subdir, ok := strings.Cut(selector, ":"); ok {
			rest += "/" + subdir
		}
	} else if at := strings.LastIndexByte(rest, '@'); at >= 0 {
		rest = rest[:at]
	}
	host, repoPath, _ := strings.Cut(rest, "/")
	segments := strings.Split(path.Clean("/"+repoPath), "/")
	for i, segment := range segments {
		segments[i] = strings.TrimSuffix(segment, ".git")
	}
	return strings.ToLower(host) + strings.TrimSuffix(strings.Join(segments, "/"), "/")
}

func isNumber(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}
