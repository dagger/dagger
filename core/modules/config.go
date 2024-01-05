package modules

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"dagger.io/dagger"
	"github.com/vektah/gqlparser/v2/ast"
)

// Filename is the name of the module config file.
const Filename = "dagger.json"

// Config is the module config loaded from dagger.json.
type Config struct {
	Name         string   `json:"name" field:"true" doc:"The name of the module."`
	Root         string   `json:"root,omitempty" field:"true" doc:"The root directory of the module's project, which may be above the module source code."`
	SDK          string   `json:"sdk" field:"true" doc:"Either the name of a built-in SDK ('go', 'python', etc.) OR a module reference pointing to the SDK's module implementation."`
	Include      []string `json:"include,omitempty" field:"true" doc:"Include only these file globs when loading the module root."`
	Exclude      []string `json:"exclude,omitempty" field:"true" doc:"Exclude these file globs when loading the module root."`
	Dependencies []string `json:"dependencies,omitempty" field:"true" doc:"Modules that this module depends on."`
}

func NewConfig(name, sdkNameOrRef, rootPath string) *Config {
	cfg := &Config{
		Name: name,
		Root: rootPath,
		SDK:  sdkNameOrRef,
	}
	return cfg
}

func (cfg *Config) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ModuleConfig",
		NonNull:   true,
	}
}

func (cfg *Config) TypeDescription() string {
	return "Static configuration for a module (e.g. parsed contents of dagger.json)"
}

func (cfg *Config) RootAndSubpath(modSourceDir string) (string, string, error) {
	modRootDir := filepath.Join(modSourceDir, cfg.Root)
	subPath, err := filepath.Rel(modRootDir, modSourceDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to get module subpath: %w", err)
	}
	if strings.HasPrefix(subPath+"/", "../") {
		return "", "", fmt.Errorf("module subpath %q is not under module root %q", modSourceDir, modRootDir)
	}

	return modRootDir, subPath, nil
}

// Use adds the given module references to the module's dependencies.
func (cfg *Config) Use(ctx context.Context, dag *dagger.Client, ref *Ref, refs ...string) error {
	var deps []string
	deps = append(deps, cfg.Dependencies...)
	deps = append(deps, refs...)
	depSet := make(map[string]*Ref)
	for _, dep := range deps {
		depMod, err := ResolveModuleDependency(ctx, dag, ref, dep)
		if err != nil {
			return fmt.Errorf("failed to get module: %w", err)
		}
		depSet[depMod.Symbolic()] = depMod
	}

	cfg.Dependencies = nil
	for _, dep := range depSet {
		cfg.Dependencies = append(cfg.Dependencies, dep.String())
	}
	sort.Strings(cfg.Dependencies)

	return nil
}

// NormalizeConfigPath appends /dagger.json to the given path if it is not
// already present.
func NormalizeConfigPath(configPath string) string {
	// figure out if we were passed a path to a dagger.json file
	// or a parent dir that may contain such a file
	baseName := filepath.Base(configPath)
	if baseName == Filename {
		return configPath
	}
	return filepath.Join(configPath, Filename)
}
