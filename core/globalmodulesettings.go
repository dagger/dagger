package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core/gitref"
)

// Global module settings are the configuration-only entries of the workspace
// dagger.toml: `[modules."<source ref>".settings]` entries with no source
// field. They are carried through module loading as a (canonical key ->
// settings) map and applied to any module whose resolved source matches a
// key, at any depth of the dependency tree.

// EncodeGlobalSettings encodes the global settings map as the JSON carried by
// the asModule globalSettingsJson argument. json.Marshal sorts map keys, so
// equal maps produce byte-identical encodings and one module identity.
func EncodeGlobalSettings(settings map[string]map[string]any) (string, error) {
	if len(settings) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("encoding global module settings: %w", err)
	}
	return string(encoded), nil
}

// DecodeGlobalSettings decodes the asModule globalSettingsJson argument.
func DecodeGlobalSettings(encoded string) (map[string]map[string]any, error) {
	if encoded == "" {
		return nil, nil
	}
	var settings map[string]map[string]any
	if err := json.Unmarshal([]byte(encoded), &settings); err != nil {
		return nil, fmt.Errorf("decoding global module settings: %w", err)
	}
	return settings, nil
}

// MergeModuleSettings merges matched global settings under a module's own
// instance settings: every key of instance wins over a case-insensitive
// equivalent in global.
func MergeModuleSettings(global, instance map[string]any) map[string]any {
	merged := make(map[string]any, len(global)+len(instance))
	for key, value := range global {
		hasInstanceKey := false
		for instanceKey := range instance {
			if strings.EqualFold(instanceKey, key) {
				hasInstanceKey = true
				break
			}
		}
		if !hasInstanceKey {
			merged[key] = value
		}
	}
	for key, value := range instance {
		merged[key] = value
	}
	return merged
}

// CanonicalGlobalSettingsKey returns the canonical matching key for a
// configuration-only module settings entry key. Local keys must already be
// resolved to an absolute path by the caller. Git keys are normalized so that
// scheme and user variants of the same ref produce the same key.
func CanonicalGlobalSettingsKey(ctx context.Context, key string) (string, error) {
	if FastModuleSourceKindCheck(key, "") == ModuleSourceKindLocal {
		return filepath.Clean(key), nil
	}
	parsed, err := gitref.Parse(ctx, key)
	if err != nil {
		return "", err
	}
	version := ""
	if parsed.HasVersion {
		version = parsed.ModVersion
	}
	return GitRefString(normalizeCloneRef(parsed.CloneRef), parsed.RepoRootSubdir, version), nil
}

// GlobalSettingsForSource returns the settings of the most specific
// configuration-only entry matching src, or nil. Git sources match their
// version-restricted key first, then the version-agnostic one; local sources
// match by context directory + subpath.
func GlobalSettingsForSource(src *ModuleSource, settings map[string]map[string]any) map[string]any {
	if len(settings) == 0 {
		return nil
	}
	for _, key := range globalSettingsMatchKeys(src) {
		if matched, ok := settings[key]; ok {
			return matched
		}
	}
	return nil
}

// globalSettingsMatchKeys returns the canonical keys a loaded module source is
// matched under, most specific first.
func globalSettingsMatchKeys(src *ModuleSource) []string {
	switch src.Kind {
	case ModuleSourceKindLocal:
		return []string{filepath.Clean(src.AsString())}
	case ModuleSourceKindGit:
		cloneRef := normalizeCloneRef(src.Git.CloneRef)
		var keys []string
		if src.Git.Version != "" {
			keys = append(keys, GitRefString(cloneRef, src.SourceRootSubpath, src.Git.Version))
		}
		return append(keys, GitRefString(cloneRef, src.SourceRootSubpath, ""))
	default:
		return nil
	}
}

// normalizeCloneRef reduces the scheme, user and ".git" variants of a git
// clone ref to one canonical form, e.g. "https://github.com/dagger/dagger",
// "git@github.com:dagger/dagger" and "github.com/dagger/dagger.git" all
// normalize to "github.com/dagger/dagger".
func normalizeCloneRef(cloneRef string) string {
	ref := cloneRef
	for _, scheme := range []gitref.SchemeType{gitref.SchemeHTTPS, gitref.SchemeHTTP, gitref.SchemeSSH} {
		if strings.HasPrefix(ref, scheme.Prefix()) {
			ref = strings.TrimPrefix(ref, scheme.Prefix())
			break
		}
	}
	if user, rest, ok := strings.Cut(ref, "@"); ok && !strings.ContainsAny(user, "/:") {
		ref = rest
	}
	ref = strings.Replace(ref, ":", "/", 1)
	return strings.TrimSuffix(ref, ".git")
}
