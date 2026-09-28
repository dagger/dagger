package workspace

import "slices"

// GeneratorModule identifies the implementation and file context of a module.
type GeneratorModule struct {
	Name, Source, Context string
}

type GeneratorPolicy struct {
	Replaced bool
	Skip     []string
}

// GeneratorPolicies keeps the contextual module in place of its raw toolchain.
// The contextual module also inherits skip patterns from that toolchain.
func GeneratorPolicies(modules []GeneratorModule, config *Config) map[string]GeneratorPolicy {
	wrapped := map[string]bool{}
	rawSkips := map[string][]string{}
	for _, mod := range modules {
		if mod.Context != "" && mod.Context != mod.Source {
			wrapped[mod.Source] = true
		} else {
			rawSkips[mod.Source] = append(rawSkips[mod.Source], config.Modules[mod.Name].Generate.Skip...)
		}
	}
	policies := map[string]GeneratorPolicy{}
	for _, mod := range modules {
		contextual := mod.Context != "" && mod.Context != mod.Source
		policy := GeneratorPolicy{
			Replaced: wrapped[mod.Source] && !contextual,
			Skip:     slices.Clone(config.Modules[mod.Name].Generate.Skip),
		}
		if contextual {
			policy.Skip = append(policy.Skip, rawSkips[mod.Source]...)
		}
		policies[mod.Name] = policy
	}
	return policies
}
