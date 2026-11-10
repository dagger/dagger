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
	cfg, configDir, err := governingConfig(ctx, moduleDir, read)
	if err != nil || cfg == nil {
		return nil, err
	}
	return ModuleScopeGitClients(cfg, ".")[moduleScopeIn(path.Clean(moduleDir), configDir)], nil
}

// DeclaredLocalClients reads the local clients the governing config declares
// for the module at moduleDir, a path relative to the root of its tree, as
// paths relative to that root. configDir is the config's directory, relative
// to the root, or "." when there is none. The config is the nearest
// dagger.toml between moduleDir and the root; with none, nothing is declared.
func DeclaredLocalClients(ctx context.Context, moduleDir string, read TreeFileReader) (clients []string, configDir string, _ error) {
	cfg, configDir, err := governingConfig(ctx, moduleDir, read)
	if err != nil || cfg == nil {
		return nil, ".", err
	}
	for _, target := range ModuleScopeLocalClients(cfg, ".")[moduleScopeIn(path.Clean(moduleDir), configDir)] {
		clients = append(clients, path.Join(configDir, target))
	}
	return clients, configDir, nil
}

// governingConfig parses the nearest dagger.toml between moduleDir and the
// root of the tree, and returns it with its directory. It returns no config
// when there is none, or when it does not parse: such a config declares
// nothing.
func governingConfig(ctx context.Context, moduleDir string, read TreeFileReader) (*Config, string, error) {
	for dir := path.Clean(moduleDir); ; dir = path.Dir(dir) {
		data, found, err := read(path.Join(dir, ConfigFileName))
		if err != nil {
			return nil, "", err
		}
		if found {
			cfg, err := ParseConfigAt(ctx, data, ".")
			if err != nil {
				return nil, "", nil //nolint:nilerr // deliberate: a config that does not parse declares nothing
			}
			return cfg, dir, nil
		}
		if dir == "." {
			return nil, "", nil
		}
	}
}

// moduleScopeIn is the module scope key for moduleDir in a config at
// configDir, both relative to the root of the tree.
func moduleScopeIn(moduleDir, configDir string) string {
	switch {
	case configDir == moduleDir:
		return "."
	case configDir == ".":
		return moduleDir
	default:
		return strings.TrimPrefix(moduleDir, configDir+"/")
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
