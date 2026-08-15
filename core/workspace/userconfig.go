package workspace

import (
	"fmt"
	"strings"

	toml "github.com/pelletier/go-toml"

	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/util/gitutil"
)

// UserConfig is the workspace-relevant portion of the user-level Dagger config
// file (~/.config/dagger/config.toml, or $DAGGER_CONFIG). Only the
// [workspaces.*] section is modeled here; other sections (e.g. [llm]) are
// owned by other subsystems and ignored during parsing.
type UserConfig struct {
	Workspaces map[string]UserWorkspaceOverlay `toml:"workspaces"`
}

// UserWorkspaceOverlay is one workspace's user-level overlay, keyed in the
// user config by the workspace's normalized Git remote (see
// NormalizeGitRemote). It is a constrained subset of the workspace config:
// always-applied module overlays plus personal environments. User-level
// values take precedence over the repository's dagger.toml.
type UserWorkspaceOverlay struct {
	Modules map[string]EnvModuleOverlay `toml:"modules"`
	Env     map[string]EnvOverlay       `toml:"env"`
}

// ParseUserConfig parses user-level Dagger config bytes. Unknown sections are
// ignored so the file can be shared with other subsystems.
func ParseUserConfig(data []byte) (*UserConfig, error) {
	var cfg UserConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse user config: %w", err)
	}
	return &cfg, nil
}

// MatchWorkspaceOverlay returns the overlay whose key identifies the same
// remote as workspaceKey, or nil when none matches. Both sides are normalized
// before comparison so equivalent remote URL forms (https, ssh, scp-style,
// with or without .git) all match.
func (c *UserConfig) MatchWorkspaceOverlay(workspaceKey string) *UserWorkspaceOverlay {
	if c == nil || len(c.Workspaces) == 0 {
		return nil
	}
	workspaceKey = NormalizeGitRemote(workspaceKey)
	if workspaceKey == "" {
		return nil
	}
	for key, overlay := range c.Workspaces {
		if NormalizeGitRemote(key) == workspaceKey {
			overlay := overlay
			return &overlay
		}
	}
	return nil
}

// ApplyUserOverlay returns a copy of cfg with the user-level workspace overlay
// merged over it.
//
// Module overlays follow the same value semantics as environment overlays:
// settings shadow the base values key by key, and a source (with its pin) adds
// or replaces a module. Unlike environment overlays, an entry naming a module
// that is neither installed nor given a source is skipped rather than an
// error: the same workspace key spans every checkout and branch of a repo, so
// an always-on user entry must not break checkouts where the module does not
// exist. Environments are added to the config's env set; a user env that
// shares a name with a repository env is merged over it with user values
// winning.
func ApplyUserOverlay(cfg *Config, overlay *UserWorkspaceOverlay) (*Config, error) {
	if cfg == nil || overlay == nil {
		return cfg, nil
	}

	applied := cloneConfig(cfg)
	known := make(map[string]EnvModuleOverlay, len(overlay.Modules))
	for name, moduleOverlay := range overlay.Modules {
		if _, ok := applied.Modules[name]; !ok && moduleOverlay.Source == "" {
			continue
		}
		known[name] = moduleOverlay
	}
	if err := applyModuleOverlays(applied, known, "user config"); err != nil {
		return nil, err
	}

	if len(overlay.Env) > 0 {
		if applied.Env == nil {
			applied.Env = make(map[string]EnvOverlay, len(overlay.Env))
		}
		for name, userEnv := range overlay.Env {
			applied.Env[name] = mergeEnvOverlay(applied.Env[name], userEnv)
		}
	}

	return applied, nil
}

// mergeEnvOverlay merges over on top of base, with over's values winning at
// the module-setting key level.
func mergeEnvOverlay(base, over EnvOverlay) EnvOverlay {
	merged := EnvOverlay{}
	if len(base.Modules)+len(over.Modules) > 0 {
		merged.Modules = make(map[string]EnvModuleOverlay, len(base.Modules)+len(over.Modules))
	}
	for name, overlay := range base.Modules {
		merged.Modules[name] = EnvModuleOverlay{
			Source:   overlay.Source,
			Pin:      overlay.Pin,
			Settings: cloneConfigMap(overlay.Settings),
		}
	}
	for name, overlay := range over.Modules {
		entry := merged.Modules[name]
		if overlay.Source != "" {
			entry.Source = overlay.Source
			entry.Pin = overlay.Pin
		} else if overlay.Pin != "" {
			entry.Pin = overlay.Pin
		}
		if len(overlay.Settings) > 0 {
			if entry.Settings == nil {
				entry.Settings = make(map[string]any, len(overlay.Settings))
			}
			for key, value := range overlay.Settings {
				entry.Settings[key] = value
			}
		}
		merged.Modules[name] = entry
	}
	return merged
}

// NormalizeGitRemote converts a Git remote URL in any common form into the
// canonical workspace key form used by user-level config: host/path with no
// scheme, no user, no trailing .git, and a lowercased host. For example,
// "git@github.com:acme/api.git", "https://github.com/acme/api", and
// "ssh://git@github.com/acme/api" all normalize to "github.com/acme/api".
//
// Local filesystem remotes have no stable cross-machine identity and
// normalize to "".
func NormalizeGitRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	// Filesystem remotes are not usable as keys: POSIX paths, file:// URLs,
	// and Windows drive-letter or UNC paths (checked with separators
	// normalized, so C:\Users, C:/Users, and \\server\share are all caught).
	slashed := strings.ReplaceAll(remote, "\\", "/")
	if strings.HasPrefix(slashed, "/") || strings.HasPrefix(slashed, ".") ||
		strings.HasPrefix(slashed, "file://") || strings.HasPrefix(slashed, "~") ||
		pathutil.GetDrive(slashed) != "" {
		return ""
	}

	if u, err := gitutil.ParseURL(remote); err == nil {
		host := strings.ToLower(u.Host)
		path := strings.Trim(u.Path, "/")
		path = strings.TrimSuffix(path, ".git")
		if host == "" {
			return ""
		}
		if path == "" {
			return host
		}
		return host + "/" + path
	}

	// Already scheme-less ("github.com/acme/api"): apply the same trims so
	// equivalent spellings still match.
	remote = strings.Trim(remote, "/")
	remote = strings.TrimSuffix(remote, ".git")
	host, path, ok := strings.Cut(remote, "/")
	if !ok {
		return strings.ToLower(remote)
	}
	return strings.ToLower(host) + "/" + path
}

// GitRemoteURL extracts the URL of the named remote from git config file
// contents (.git/config format, with any includes already expanded — see
// ResolveGitConfigIncludes). Like `git config --get`, the last value wins
// when the key appears more than once.
func GitRemoteURL(gitConfig []byte, remoteName string) (string, bool) {
	section := fmt.Sprintf("[remote %q]", remoteName)
	inRemote := false
	url := ""
	for _, line := range strings.Split(string(gitConfig), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			inRemote = line == section
			continue
		}
		if !inRemote {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "url" {
			if v := strings.TrimSpace(value); v != "" {
				url = v
			}
		}
	}
	return url, url != ""
}

// ParseGitDirFile parses a .git *file* (as written for worktrees and
// submodules) and returns the gitdir path it points to.
func ParseGitDirFile(data []byte) (string, bool) {
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return "", false
	}
	gitDir = strings.TrimSpace(gitDir)
	return gitDir, gitDir != ""
}

// userOverlayKeyParts validates that key addresses user-overridable config and
// returns its parsed path segments. Only module settings may be stored in a
// user-level overlay, always applied or scoped to an environment.
func userOverlayKeyParts(key string) ([]string, error) {
	parts, err := splitConfigPath(key)
	if err != nil {
		return nil, err
	}
	rest := parts
	if len(rest) > 0 && rest[0] == "env" {
		if len(rest) < 2 {
			return nil, fmt.Errorf("key %q is missing an environment name", key)
		}
		rest = rest[2:]
	}
	if len(rest) < 4 || rest[0] != "modules" || rest[2] != "settings" {
		return nil, fmt.Errorf("key %q cannot be stored in user-level config; only modules.<name>.settings.* and env.<name>.modules.<name>.settings.* are supported", key)
	}
	return parts, nil
}

// WriteUserConfigValue sets a config value under [workspaces.<workspaceKey>]
// in user-level config bytes, preserving unrelated sections (e.g. [llm]) and
// other workspace entries. The workspace key is canonicalized; when an entry
// for an equivalent remote spelling already exists, it is updated in place
// rather than duplicated. A non-nil values slice stores a string array
// verbatim; otherwise rawValue is typed like repository config writes.
func WriteUserConfigValue(existing []byte, workspaceKey, key, rawValue string, values []string) ([]byte, error) {
	parts, err := userOverlayKeyParts(key)
	if err != nil {
		return nil, err
	}
	tree, entryKey, err := userConfigTreeAndEntryKey(existing, workspaceKey)
	if err != nil {
		return nil, err
	}

	var value any
	if values != nil {
		value = values
	} else {
		value = parseValueString(parts, rawValue)
	}
	tree.SetPath(append([]string{"workspaces", entryKey}, parts...), value)

	out, err := tree.ToTomlString()
	if err != nil {
		return nil, fmt.Errorf("serialize user config: %w", err)
	}
	return []byte(out), nil
}

// DeleteUserConfigValue removes a config value under
// [workspaces.<workspaceKey>] in user-level config bytes, pruning any tables
// the removal leaves empty. It errors when the key is not set for that
// workspace.
func DeleteUserConfigValue(existing []byte, workspaceKey, key string) ([]byte, error) {
	parts, err := userOverlayKeyParts(key)
	if err != nil {
		return nil, err
	}
	tree, entryKey, err := userConfigTreeAndEntryKey(existing, workspaceKey)
	if err != nil {
		return nil, err
	}

	fullPath := append([]string{"workspaces", entryKey}, parts...)
	if tree.GetPath(fullPath) == nil {
		return nil, fmt.Errorf("key %q is not set in user-level config for workspace %q", key, entryKey)
	}
	if err := tree.DeletePath(fullPath); err != nil {
		return nil, fmt.Errorf("unset %q: %w", key, err)
	}
	// Prune tables the removal left empty, innermost first.
	for prefix := fullPath[:len(fullPath)-1]; len(prefix) > 0; prefix = prefix[:len(prefix)-1] {
		parent, ok := tree.GetPath(prefix).(*toml.Tree)
		if !ok || len(parent.Keys()) > 0 {
			break
		}
		if err := tree.DeletePath(prefix); err != nil {
			return nil, fmt.Errorf("prune empty table %q: %w", strings.Join(prefix, "."), err)
		}
	}

	out, err := tree.ToTomlString()
	if err != nil {
		return nil, fmt.Errorf("serialize user config: %w", err)
	}
	return []byte(out), nil
}

// userConfigTreeAndEntryKey parses user config bytes and resolves the
// [workspaces.*] entry key to operate on: an existing entry whose key
// normalizes to the same remote, or the canonical key when none exists.
func userConfigTreeAndEntryKey(existing []byte, workspaceKey string) (*toml.Tree, string, error) {
	canonical := NormalizeGitRemote(workspaceKey)
	if canonical == "" {
		return nil, "", fmt.Errorf("workspace key %q is not a usable git remote", workspaceKey)
	}
	tree, err := toml.LoadBytes(existing)
	if err != nil {
		return nil, "", fmt.Errorf("parse user config: %w", err)
	}
	if workspaces, ok := tree.Get("workspaces").(*toml.Tree); ok {
		for _, existingKey := range workspaces.Keys() {
			if NormalizeGitRemote(existingKey) == canonical {
				return tree, existingKey, nil
			}
		}
	}
	return tree, canonical, nil
}
