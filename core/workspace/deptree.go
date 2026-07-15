package workspace

import (
	"strings"

	"github.com/dagger/dagger/core/modules"
)

// LocalModuleRefs returns the local-path module references declared by cfg,
// drawn from its toolchains and dependencies in a stable order (toolchains
// first, then dependencies, each in declaration order). Remote (git/registry)
// references and the blueprint are skipped: migration recurses into the
// locally-defined modules a config points at, and blueprints are out of scope.
func LocalModuleRefs(cfg *modules.ModuleConfig) []*modules.ModuleConfigDependency {
	if cfg == nil {
		return nil
	}
	refs := make([]*modules.ModuleConfigDependency, 0, len(cfg.Toolchains)+len(cfg.Dependencies))
	for _, tc := range cfg.Toolchains {
		if tc != nil && IsLocalRef(tc.Source, tc.Pin) {
			refs = append(refs, tc)
		}
	}
	for _, dep := range cfg.Dependencies {
		if dep != nil && IsLocalRef(dep.Source, dep.Pin) {
			refs = append(refs, dep)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	return refs
}

// HasOwnWorkspaceSemantics reports whether cfg defines workspace-level
// semantics of its own — its own toolchains or a blueprint. Such a config
// cannot be migrated by in-place format conversion; it would need the full
// workspace treatment (PlanMigration moves and deletes its dagger.json), which
// would break a referrer that expects the module to stay put. This is
// deliberately narrower than mustMigrateToWorkspaceConfig, which also fires for
// a plain module whose source is a non-root subdir (a normal toolchain).
func HasOwnWorkspaceSemantics(cfg *modules.ModuleConfig) bool {
	return cfg != nil && (cfg.Blueprint != nil || len(cfg.Toolchains) > 0)
}

// ParseLegacyModuleConfigTolerant parses a legacy dagger.json into a module
// config, falling back to a shape-only parse when the config declares a newer
// engine version than this binary supports. Migration still needs to read such
// a config's dependency graph to recurse, and to convert what it can.
func ParseLegacyModuleConfigTolerant(data []byte) (*modules.ModuleConfig, error) {
	cfg, err := parseLegacyConfig(data)
	if err != nil {
		if !strings.Contains(err.Error(), "module requires dagger") {
			return nil, err
		}
		return parseLegacyConfigShape(data)
	}
	return cfg, nil
}
