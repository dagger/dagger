package modules

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// Filename is the name of the module config file.
const Filename = "dagger.json"

// Config is the module config loaded from dagger.json.
type Config struct {
	// The name of the module.
	Name string `json:"name"`

	// The name of a well-known SDK recognized by Dagger. Used for automatically
	// bumping the SDKRuntime field as the user upgrades Dagger.
	//
	// May be omitted if you're bringing your own custom SDK runtime.
	SDKName string `json:"sdk,omitempty"`

	// An image ref to a SDK module runtime.
	SDKRuntime string `json:"sdkRuntime"`

	// The root directory of the module's project, which may be above the module
	// source code.
	Root string `json:"root,omitempty"`

	// Include only these file globs when loading the module root.
	Include []string `json:"include,omitempty"`

	// Exclude these file globs when loading the module root.
	Exclude []string `json:"exclude,omitempty"`

	// Modules that this module depends on.
	Dependencies []string `json:"dependencies,omitempty"`
}

// SyncRuntime sets the SDKRuntime field to the current image ref for the
// well-known runtime for the SDKName field.
func NewConfig(name, sdkNameOrRef, rootPath string) *Config {
	cfg := &Config{
		Name: name,
		Root: rootPath,
	}
	if ref, found := WellKnownSDKRuntimes[sdkNameOrRef]; found {
		cfg.SDKName = sdkNameOrRef
		cfg.SDKRuntime = ref
	} else {
		cfg.SDKRuntime = sdkNameOrRef
	}
	return cfg
}

// SyncSDKRuntime sets the SDKRuntime field to the current image ref for the
// well-known runtime for the SDKName field.
func (cfg *Config) SyncSDKRuntime() {
	if cfg.SDKName == "" {
		// assume hand-configured
		return
	}

	cfg.SDKRuntime = WellKnownSDKRuntimes[cfg.SDKName]
}

func (cfg *Config) RootAndSubpath(moduleSourceDir string) (string, string, error) {
	modSrcDir, err := filepath.Abs(moduleSourceDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to get module root: %w", err)
	}

	modRootDir := filepath.Join(modSrcDir, cfg.Root)

	subPath, err := filepath.Rel(modRootDir, modSrcDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to get module subpath: %w", err)
	}
	if strings.HasPrefix(subPath, "..") {
		return "", "", fmt.Errorf("module subpath %q is not under module root %q", moduleSourceDir, modRootDir)
	}

	return modRootDir, subPath, nil
}

// NormalizeConfigPath appends /dagger.json to the given path if it is not
// already present.
func NormalizeConfigPath(configPath string) string {
	// figure out if we were passed a path to a dagger.json file
	// or a parent dir that may contain such a file
	baseName := path.Base(configPath)
	if baseName == Filename {
		return configPath
	}
	return path.Join(configPath, Filename)
}
