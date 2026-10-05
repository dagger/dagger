package daggercmd

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/core/workspace"
	"github.com/spf13/cobra"
)

func commandArtifactTypes(cmd *cobra.Command) []string {
	switch cmd.Name() {
	case "check":
		return []string{"Check"}
	case "generate":
		return []string{"Generator"}
	case "up":
		return []string{"Service"}
	case "agent":
		return []string{"Expertise"}
	case "shell":
		return []string{"Container", "Directory"}
	}
	return nil
}

func commandArtifactTargets(ctx context.Context, dag *dagger.Client, cmd *cobra.Command, all *dagger.Artifacts) (*dagger.Artifacts, error) {
	types := commandArtifactTypes(cmd)
	if len(types) == 0 {
		return nil, fmt.Errorf("command %q does not select artifacts", cmd.Name())
	}
	selected := all.FilterTypes(types)
	if cmd.Name() == "generate" {
		// Type filters keep load failures. Generate reports them as warnings
		// and runs the other generators.
		failures, err := artifactLoadFailures(ctx, dag, selected)
		if err != nil {
			return nil, err
		}
		for _, failure := range failures {
			selected = selected.WithoutURI(failure.URI)
		}
	}
	if cmd.Name() == "agent" || cmd.Name() == "shell" {
		return selected, nil
	}
	cfg, err := artifactWorkspaceConfig(ctx, dag.CurrentWorkspace())
	if err != nil {
		return nil, err
	}
	generated := true
	if cfg.CheckGenerated != nil {
		generated = *cfg.CheckGenerated
	}
	if cmd.Name() == "check" && cmd.Flags().Changed("generated") {
		generated, _ = cmd.Flags().GetBool("generated")
	}
	if cmd.Name() == "check" && !generated {
		selected = selected.FilterParentTypes([]string{"Generator"}, dagger.ArtifactsFilterParentTypesOpts{Exclude: true})
	}
	policies := map[string]workspace.GeneratorPolicy{}
	if cmd.Name() == "generate" || cmd.Name() == "check" && generated {
		policies, err = commandGeneratorPolicies(ctx, dag, all, cfg)
		if err != nil {
			return nil, err
		}
	}
	excluded, err := commandSkippedPaths(ctx, dag, selected, commandPathExclusions(cmd.Name(), generated, cfg, policies))
	if err != nil {
		return nil, err
	}
	for _, uri := range excluded {
		selected = selected.WithoutURI(uri)
	}
	if cmd.Name() == "check" {
		for _, skip := range checksSkip {
			address, err := dagaddress.Parse(skip)
			if err != nil {
				return nil, err
			}
			address.Absolute = false
			if address.Path != "" && !strings.ContainsAny(address.Path, "*?[{") {
				address.Path += "/**"
			}
			selected = selected.WithoutURI(address.String())
		}
	}
	return selected, nil
}

type commandPathExclusion struct {
	module     string
	patterns   []string
	generators bool
}

func commandPathExclusions(command string, generated bool, cfg *workspace.Config, policies map[string]workspace.GeneratorPolicy) []commandPathExclusion {
	names := map[string]bool{}
	for name := range cfg.Modules {
		names[name] = true
	}
	for name := range policies {
		names[name] = true
	}
	var exclusions []commandPathExclusion
	for _, name := range slices.Sorted(maps.Keys(names)) {
		entry := cfg.Modules[name]
		var skip []string
		switch command {
		case "check":
			skip = entry.Check.Skip
		case "up":
			skip = entry.Up.Skip
		}
		if len(skip) > 0 {
			exclusions = append(exclusions, commandPathExclusion{module: name, patterns: skip})
		}
		if command != "generate" && (command != "check" || !generated) {
			continue
		}
		policy, ok := policies[name]
		if !ok {
			policy.Skip = entry.Generate.Skip
		}
		if !policy.Replaced && len(policy.Skip) == 0 {
			continue
		}
		if policy.Replaced {
			policy.Skip = nil // Select the complete generator namespace.
		}
		exclusions = append(exclusions, commandPathExclusion{module: name, patterns: policy.Skip, generators: command == "check"})
	}
	return exclusions
}

// Settings skip schema paths, not collection keys. Batch the metadata reads.
func commandSkippedPaths(ctx context.Context, dag *dagger.Client, selected *dagger.Artifacts, exclusions []commandPathExclusion) ([]string, error) {
	if len(exclusions) == 0 {
		return nil, nil
	}
	variables := map[string]any{}
	var declarations []string
	var fields strings.Builder
	for i, exclusion := range exclusions {
		var patterns []string
		for _, pattern := range exclusion.patterns {
			pattern = strings.ReplaceAll(pattern, ":", "/")
			patterns = append(patterns, pattern+"/**", exclusion.module+"/"+pattern+"/**")
		}
		pattern := "**"
		if len(patterns) > 0 {
			pattern = "{" + strings.Join(patterns, ",") + "}"
		}
		declarations = append(declarations, fmt.Sprintf("$module%d: String!, $pattern%d: String!", i, i))
		variables[fmt.Sprintf("module%d", i)] = "dag://" + exclusion.module + "/**"
		variables[fmt.Sprintf("pattern%d", i)] = pattern
		selection := "selected"
		if exclusion.generators {
			selection = "generators"
		}
		if _, found := variables[selection]; !found {
			scope := selected
			if exclusion.generators {
				scope = scope.FilterParentTypes([]string{"Generator"})
			}
			id, err := scope.ID(ctx)
			if err != nil {
				return nil, err
			}
			variables[selection] = id
			declarations = append(declarations, "$"+selection+": ID!")
		}
		fmt.Fprintf(&fields, `skip%d: node(id: $%s) { ... on Artifacts {
 filterUri(uri: $module%d) { filterPathPattern(pattern: $pattern%d) {
  pathDefinitions { uri loadError }
 } }
} }
`, i, selection, i, i)
	}
	var response map[string]struct {
		FilterURI struct {
			FilterPathPattern struct{ PathDefinitions []artifactListPath }
		}
	}
	err := dag.Do(ctx, &dagger.Request{
		Query:     "query(" + strings.Join(declarations, ", ") + ") {" + fields.String() + "}",
		Variables: variables,
	}, &dagger.Response{Data: &response})
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, matched := range response {
		for _, path := range matched.FilterURI.FilterPathPattern.PathDefinitions {
			if path.LoadError == "" {
				paths = append(paths, path.URI)
			}
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths), nil
}

func commandGeneratorPolicies(ctx context.Context, dag *dagger.Client, all *dagger.Artifacts, cfg *workspace.Config) (map[string]workspace.GeneratorPolicy, error) {
	id, err := all.ID(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Node struct {
			Modules []struct {
				Name          string
				Source        *struct{ Digest string }
				ContextSource *struct{ Digest string }
			}
		}
	}
	err = dag.Do(ctx, &dagger.Request{Query: `query($id: ID!) {
 node(id: $id) { ... on Artifacts { modules {
  name source { digest } contextSource { digest }
 } } }
}`, Variables: map[string]any{"id": id}}, &dagger.Response{Data: &response})
	if err != nil {
		return nil, err
	}
	var modules []workspace.GeneratorModule
	for _, mod := range response.Node.Modules {
		if mod.Source == nil {
			continue
		}
		info := workspace.GeneratorModule{Name: mod.Name, Source: mod.Source.Digest}
		if mod.ContextSource != nil {
			info.Context = mod.ContextSource.Digest
		}
		modules = append(modules, info)
	}
	return workspace.GeneratorPolicies(modules, cfg), nil
}
