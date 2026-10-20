package schema

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

// workspaceArtifactSkip removes the artifacts in scope whose path matches a
// module's skip patterns.
type workspaceArtifactSkip struct {
	module   string
	patterns []string
	scope    func(*core.Artifacts) *core.Artifacts
}

// workspaceSkippedPaths lists the artifact paths the workspace settings skip:
// check.skip skips Checks, up.skip skips Services, and generate.skip skips
// Generators and their staleness checks. A contextual module also skips what
// generate.skip skips in its raw toolchain. Settings skip schema paths, not
// collection keys, and never a module's load failure.
func workspaceSkippedPaths(ctx context.Context, selection *core.Artifacts, mods []dagql.ObjectResult[*core.Module], cfg *workspace.Config) (map[string]bool, error) {
	policies, err := workspaceGeneratorPolicies(ctx, mods, cfg)
	if err != nil {
		return nil, err
	}
	ofTypes := func(types ...string) func(*core.Artifacts) *core.Artifacts {
		return func(a *core.Artifacts) *core.Artifacts { return a.FilterTypes(types, false) }
	}
	underGenerators := func(a *core.Artifacts) *core.Artifacts {
		return a.FilterParentTypes([]string{"Generator"}, false)
	}
	names := map[string]bool{}
	for name := range cfg.Modules {
		names[name] = true
	}
	for name := range policies {
		names[name] = true
	}
	var skips []workspaceArtifactSkip
	for _, name := range slices.Sorted(maps.Keys(names)) {
		entry := cfg.Modules[name]
		generate := entry.Generate.Skip
		if policy, ok := policies[name]; ok {
			generate = policy.Skip
		}
		for _, skip := range []workspaceArtifactSkip{
			{name, entry.Check.Skip, ofTypes("Check")},
			{name, entry.Up.Skip, ofTypes("Service")},
			{name, generate, ofTypes("Generator")},
			{name, generate, underGenerators},
		} {
			if len(skip.patterns) > 0 {
				skips = append(skips, skip)
			}
		}
	}
	skipped := map[string]bool{}
	for _, skip := range skips {
		var patterns []string
		for _, pattern := range skip.patterns {
			pattern = strings.ReplaceAll(pattern, ":", "/")
			patterns = append(patterns, pattern+"/**", skip.module+"/"+pattern+"/**")
		}
		module, err := dagaddress.Parse("dag://" + skip.module + "/**")
		if err != nil {
			return nil, err
		}
		matched, err := skip.scope(selection).FilterURI(module)
		if err != nil {
			return nil, err
		}
		if matched, err = matched.FilterPattern("{" + strings.Join(patterns, ",") + "}"); err != nil {
			return nil, err
		}
		if matched, err = matched.SchemaSelection(); err != nil {
			return nil, err
		}
		for _, entry := range matched.Entries {
			if entry.LoadFailure == nil {
				skipped[strings.Join(entry.Path, "/")] = true
			}
		}
	}
	return skipped, nil
}

// workspaceGeneratorPolicies reads the module identities that decide which
// generate.skip patterns a contextual module inherits.
func workspaceGeneratorPolicies(ctx context.Context, mods []dagql.ObjectResult[*core.Module], cfg *workspace.Config) (map[string]workspace.GeneratorPolicy, error) {
	var modules []workspace.GeneratorModule
	for _, res := range mods {
		mod := res.Self()
		if mod == nil || !mod.Source.Valid || mod.Source.Value.Self() == nil {
			continue
		}
		source, err := mod.Source.Value.Self().SourceImplementationDigest(ctx)
		if err != nil {
			return nil, err
		}
		info := workspace.GeneratorModule{Name: mod.Name(), Source: source.String()}
		if mod.ContextSource.Valid && mod.ContextSource.Value.Self() != nil {
			contextSource, err := mod.ContextSource.Value.Self().SourceImplementationDigest(ctx)
			if err != nil {
				return nil, err
			}
			info.Context = contextSource.String()
		}
		modules = append(modules, info)
	}
	return workspace.GeneratorPolicies(modules, cfg), nil
}
