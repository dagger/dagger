package workspace

import (
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/dagger/dagger/engine/vcs"
)

// ModuleSelection identifies an existing installation. Version is a replacement
// request from the selector, never a condition for matching an installation.
type ModuleSelection struct {
	Name    string
	Entry   ModuleEntry
	Source  string // normalized source used for matching; empty for a name match
	Version string
}

// SelectModule matches an installed name first, then a source without its
// version. Config sources are relative to configDir; selectors are relative to
// cwd. Both directories must use the same workspace root.
func SelectModule(modules map[string]ModuleEntry, configDir, cwd, selector string, allowVersion bool) (ModuleSelection, error) {
	if entry, ok := modules[selector]; ok {
		return ModuleSelection{Name: selector, Entry: entry}, nil
	}
	if name, version, hasVersion := strings.Cut(selector, "@"); hasVersion && !strings.Contains(version, ":") {
		if entry, installed := modules[name]; installed {
			if !allowVersion {
				return ModuleSelection{}, fmt.Errorf("version selector is not allowed here; use an installed name or a source without a version")
			}
			if version == "" {
				return ModuleSelection{}, fmt.Errorf("version must not be empty in %q", selector)
			}
			return ModuleSelection{Name: name, Entry: entry, Version: version}, nil
		}
	}
	source, version, hasVersion, err := SplitModuleVersion(selector)
	if err != nil {
		return ModuleSelection{}, err
	}
	if hasVersion && !allowVersion {
		return ModuleSelection{}, fmt.Errorf("version selector is not allowed here; use an installed name or a source without a version")
	}
	if entry, ok := modules[source]; ok {
		return ModuleSelection{Name: source, Entry: entry, Version: version}, nil
	}
	key := ModuleSourceIdentity(source, cwd)
	if !IsLocalRef(selector, "") {
		key = ModuleSourceIdentity(selector, cwd)
	}
	var matches []string
	for name, entry := range modules {
		if entry.Source != "" && ModuleSourceIdentity(entry.Source, configDir) == key {
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return ModuleSelection{}, fmt.Errorf("module %q is not installed in the workspace", source)
	}
	if len(matches) > 1 {
		quoted := make([]string, len(matches))
		for i, name := range matches {
			quoted[i] = fmt.Sprintf("%q", name)
		}
		return ModuleSelection{}, fmt.Errorf("source %q matches installed modules %s; use an installed name", key.String(), strings.Join(quoted, ", "))
	}
	name := matches[0]
	return ModuleSelection{Name: name, Entry: modules[name], Source: key.String(), Version: version}, nil
}

// SplitModuleVersion separates a module request without fetching the source.
// SSH user names are not version selectors. Explicit Git URLs retain their
// subdirectory when their #ref selector is removed. Local paths remain literal.
func SplitModuleVersion(ref string) (source, version string, hasVersion bool, err error) {
	if IsLocalRef(ref, "") {
		return ref, "", false, nil
	}
	if repo, fragment, ok := strings.Cut(ref, "#"); ok {
		version, subdir, _ := strings.Cut(fragment, ":")
		if version == "" || strings.Contains(fragment, "#") {
			return "", "", false, fmt.Errorf("invalid Git version selector in %q", ref)
		}
		source = repo
		if subdir != "" {
			source = strings.TrimSuffix(repo, "/") + "/" + subdir
		}
		return source, version, true, nil
	}
	// The first @ before the host's path is SSH/URL user information.
	start := 0
	separators := "/:"
	if scheme := strings.Index(ref, "://"); scheme >= 0 {
		start = scheme + 3
		separators = "/"
	}
	at := strings.IndexByte(ref, '@')
	if at < 0 {
		return ref, "", false, nil
	}
	authorityEnd := strings.IndexAny(ref[start:], separators)
	if authorityEnd >= 0 && at < start+authorityEnd &&
		(start > 0 || strings.Contains(ref[at+1:start+authorityEnd], ".") || ref[start+authorityEnd] == ':') {
		start += authorityEnd
		at = strings.IndexByte(ref[start:], '@')
		if at < 0 {
			return ref, "", false, nil
		}
		at += start
	}
	if at == len(ref)-1 {
		return "", "", false, fmt.Errorf("version must not be empty in %q", ref)
	}
	return ref[:at], ref[at+1:], true, nil
}

// ModuleSourceKey keeps repository and module subdirectory separate. A module
// in a subdirectory must not match a different repository at the same URL path.
type ModuleSourceKey struct {
	Local      string
	Repository string
	Subpath    string
}

func (key ModuleSourceKey) String() string {
	if key.Local != "" {
		return key.Local
	}
	if key.Subpath != "" {
		source := key.Repository + "/" + key.Subpath
		if ModuleSourceIdentity(source, ".") == key {
			return source
		}
		// Keep the repository boundary explicit on custom Git hosts.
		return key.Repository + ".git/" + key.Subpath
	}
	return key.Repository
}

// ModuleSourceIdentity normalizes transport, Git suffix, version and path
// spelling. It does not contact the source: installed modules can be selected
// even when their repositories are unavailable. Custom import-path redirects
// cannot be inferred offline and must use the spelling recorded in the config.
func ModuleSourceIdentity(ref, dir string) ModuleSourceKey {
	if IsLocalRef(ref, "") {
		if path.IsAbs(ref) {
			return ModuleSourceKey{Local: path.Clean(ref)}
		}
		return ModuleSourceKey{Local: path.Clean(path.Join(dir, ref))}
	}
	source, _, _, err := SplitModuleVersion(ref)
	if err != nil {
		return ModuleSourceKey{Repository: ref}
	}
	if repo, fragment, explicit := strings.Cut(ref, "#"); explicit {
		_, subdir, _ := strings.Cut(fragment, ":")
		key := ModuleSourceIdentity(repo, dir)
		key.Subpath = strings.Trim(path.Clean("/"+subdir), "/")
		return key
	}
	source = moduleSourceWithoutTransport(source)
	// Supplying the scheme disables Git endpoint probes in static discovery.
	// Launchpad's Bazaar-specific discovery performs network I/O; Git module
	// selection does not need that discovery.
	if !strings.HasPrefix(source, "launchpad.net/") {
		if repo, err := vcs.RepoRootForImportPathStatic(source, "https"); err == nil && repo.VCS.Cmd == "git" {
			return ModuleSourceKey{
				Repository: strings.TrimSuffix(repo.Root, ".git"),
				Subpath:    strings.Trim(path.Clean("/"+strings.TrimPrefix(source, repo.Root)), "/"),
			}
		}
	}
	return ModuleSourceKey{Repository: strings.TrimSuffix(source, ".git")}
}

func moduleSourceWithoutTransport(source string) string {
	if u, err := url.Parse(source); err == nil && u.Host != "" {
		source = strings.ToLower(u.Host) + u.Path
	} else {
		// SCP form: git@host:repository/path.
		if at := strings.IndexByte(source, '@'); at >= 0 {
			source = source[at+1:]
		}
		if colon := strings.IndexByte(source, ':'); colon >= 0 && !strings.Contains(source[:colon], "/") {
			source = source[:colon] + "/" + source[colon+1:]
		}
		if host, rest, ok := strings.Cut(source, "/"); ok {
			source = strings.ToLower(host) + "/" + rest
		}
	}
	return path.Clean(source)
}

// ModuleSourceWithVersion replaces a version request, keeping an explicit Git
// URL's literal-ref semantics and its module subdirectory.
func ModuleSourceWithVersion(source, version string) (string, error) {
	if version == "" || strings.ContainsAny(version, "#:\r\n\t ") {
		return "", fmt.Errorf("invalid module version %q", version)
	}
	if IsLocalRef(source, "") {
		return "", fmt.Errorf("local module source %q has no version; cannot set --version", source)
	}
	if repo, fragment, ok := strings.Cut(source, "#"); ok {
		_, subdir, hasSubdir := strings.Cut(fragment, ":")
		result := repo + "#" + version
		if hasSubdir {
			result += ":" + subdir
		}
		return result, nil
	}
	base, _, _, err := SplitModuleVersion(source)
	if err != nil {
		return "", err
	}
	return base + "@" + version, nil
}

// SameModuleRequest compares source identity and the requested revision. A Git
// #ref is literal; an @version can be a release query, so they remain distinct.
func SameModuleRequest(left, leftDir, right, rightDir string) bool {
	if ModuleSourceIdentity(left, leftDir) != ModuleSourceIdentity(right, rightDir) {
		return false
	}
	_, lv, lh, le := SplitModuleVersion(left)
	_, rv, rh, re := SplitModuleVersion(right)
	return le == nil && re == nil && lv == rv && lh == rh &&
		strings.Contains(left, "#") == strings.Contains(right, "#")
}

// SelectModuleUpdates prepares a complete update before any source is fetched
// or config is changed. A separate version applies to exactly one selector.
func SelectModuleUpdates(modules map[string]ModuleEntry, configDir, cwd string, selectors []string, version string) ([]ModuleSelection, error) {
	if version != "" && len(selectors) != 1 {
		return nil, fmt.Errorf("--version requires exactly one installed name or source")
	}
	if len(selectors) == 0 {
		for name := range modules {
			selectors = append(selectors, name)
		}
		sort.Strings(selectors)
	}
	selections := make([]ModuleSelection, 0, len(selectors))
	seen := map[string]string{}
	for _, selector := range selectors {
		selection, err := SelectModule(modules, configDir, cwd, selector, true)
		if err != nil {
			return nil, err
		}
		if version != "" {
			if selection.Version != "" {
				return nil, fmt.Errorf("use either a version suffix or --version, not both")
			}
			selection.Version = version
		}
		if selection.Version != "" {
			if _, err := ModuleSourceWithVersion(selection.Entry.Source, selection.Version); err != nil {
				return nil, fmt.Errorf("module %q: %w", selection.Name, err)
			}
		}
		if previous, ok := seen[selection.Name]; ok {
			if previous != selection.Version {
				return nil, fmt.Errorf("module %q has conflicting update requests", selection.Name)
			}
			continue
		}
		seen[selection.Name] = selection.Version
		selections = append(selections, selection)
	}
	return selections, nil
}
