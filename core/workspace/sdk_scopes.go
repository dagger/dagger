package workspace

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/dagger/dagger/engine/slog"
)

// SDKScopeKey returns the original key for a resolved workspace path. Only a
// new scope receives a new key. Callers must reconcile the config first.
func SDKScopeKey(entry SDKEntry, configDir, workspacePath string) (string, bool, error) {
	var match string
	found := false
	for _, key := range slices.Sorted(maps.Keys(entry.Scopes)) {
		resolved, err := ResolveSDKManagedPath(configDir, key)
		if err != nil {
			return "", false, err
		}
		if resolved != workspacePath {
			continue
		}
		if found {
			return "", false, fmt.Errorf("scope keys %q and %q resolve to %q; reconcile the scope records first", match, key, workspacePath)
		}
		match, found = key, true
	}
	if found {
		return match, true, nil
	}
	key, err := SDKManagedPathFor(configDir, workspacePath)
	return key, false, err
}

// ReconcileSDKScopes merges compatible records for the same SDK and resolved
// workspace path. It retains the first key in sorted order. On conflict, cfg
// remains unchanged. Returned messages describe every reconciled record.
func ReconcileSDKScopes(cfg *Config, configDir string) ([]string, error) {
	if cfg == nil {
		return nil, nil
	}
	updated := maps.Clone(cfg.SDKs)
	var messages []string
	for _, sdkName := range slices.Sorted(maps.Keys(cfg.SDKs)) {
		entry := cfg.SDKs[sdkName]
		entry.Scopes = maps.Clone(entry.Scopes)
		keysByPath := map[string][]string{}
		for _, key := range slices.Sorted(maps.Keys(entry.Scopes)) {
			resolved, err := ResolveSDKManagedPath(configDir, key)
			if err != nil {
				return nil, fmt.Errorf("SDK %q scope %q: %w", sdkName, key, err)
			}
			previousKeys := keysByPath[resolved]
			keysByPath[resolved] = append(previousKeys, key)
			if len(previousKeys) == 0 {
				continue
			}
			previous := previousKeys[0]
			merged, conflicts := mergeSDKScopes(entry.Scopes[previous], entry.Scopes[key])
			if len(conflicts) > 0 {
				return nil, fmt.Errorf("SDK %q scope keys %q resolve to %q but conflict in %s; correct dagger.toml", sdkName, keysByPath[resolved], resolved, strings.Join(conflicts, ", "))
			}
			entry.Scopes[previous] = merged
			delete(entry.Scopes, key)
			messages = append(messages, fmt.Sprintf("SDK %q: reconciled scope keys %q and %q at %q; the next configuration write will retain %q", sdkName, previous, key, resolved, previous))
		}
		updated[sdkName] = entry
	}
	cfg.SDKs = updated
	return messages, nil
}

func mergeSDKScopes(first, second SDKScope) (SDKScope, []string) {
	var conflicts []string
	if first.IsModule != second.IsModule {
		conflicts = append(conflicts, "is-module")
	}
	if first.Name != "" && second.Name != "" && first.Name != second.Name {
		conflicts = append(conflicts, "name")
	}
	merged := first
	if merged.Name == "" {
		merged.Name = second.Name
	}
	merged.Settings = maps.Clone(first.Settings)
	for _, key := range slices.Sorted(maps.Keys(second.Settings)) {
		value := second.Settings[key]
		if previous, exists := merged.Settings[key]; exists && !reflect.DeepEqual(previous, value) {
			conflicts = append(conflicts, JoinConfigPath("settings", key))
			continue
		}
		if merged.Settings == nil {
			merged.Settings = map[string]any{}
		}
		merged.Settings[key] = value
	}
	merged.Clients = nil
	for _, target := range append(slices.Clone(first.Clients), second.Clients...) {
		if !slices.Contains(merged.Clients, target) {
			merged.Clients = append(merged.Clients, target)
		}
	}
	return merged, conflicts
}

// ParseConfigAt resolves and reconciles SDK scope paths once the workspace
// config directory is known. This only changes the in-memory configuration.
func ParseConfigAt(ctx context.Context, data []byte, configDir string) (*Config, error) {
	cfg, err := ParseConfig(data)
	if err != nil {
		return nil, err
	}
	if err := reconcileSDKScopes(ctx, cfg, configDir); err != nil {
		return nil, err
	}
	return cfg, nil
}

// UpdateConfigBytesAt checks scope uniqueness at the configuration write
// boundary, including writes through the generic workspace config commands.
func UpdateConfigBytesAt(ctx context.Context, data []byte, cfg *Config, configDir string) ([]byte, error) {
	if err := reconcileSDKScopes(ctx, cfg, configDir); err != nil {
		return nil, err
	}
	return UpdateConfigBytes(data, cfg)
}

func reconcileSDKScopes(ctx context.Context, cfg *Config, configDir string) error {
	messages, err := ReconcileSDKScopes(cfg, configDir)
	if err != nil {
		return err
	}
	for _, message := range messages {
		slog.GlobalLogger(ctx, "dagger/workspace").Warn(message)
	}
	return nil
}
