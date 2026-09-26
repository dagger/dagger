package daggercmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/artifact"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type artifactFlagNames struct{ Key string }

// Allocate all names together. A flag has the same meaning in every peer command.
func artifactDimensionFlagNames(cmd *cobra.Command, dimensions artifact.Dimensions) map[string]artifactFlagNames {
	dimensions = artifactSelectorDimensions(dimensions)
	reserved := artifactReservedFlags(cmd)
	reserved[artifact.ModuleDimension] = true
	names := map[string]artifactFlagNames{}

	type request struct {
		id         string
		candidates []string
	}
	var requests []request
	dims := slices.Clone(dimensions)
	slices.SortFunc(dims, func(a, b *artifact.Dimension) int {
		return strings.Compare(a.Identifier, b.Identifier)
	})
	for _, d := range dims {
		if d.Kind == "MODULE" {
			names[d.Identifier] = artifactFlagNames{Key: "module"}
			continue
		}
		requests = append(requests, request{d.Identifier, []string{d.Name, "artifact-" + d.Name}})
	}
	for i, req := range requests {
		chosen := ""
		for level, candidate := range req.candidates {
			if candidate == "" || reserved[candidate] || strings.HasPrefix(candidate, "by-") {
				continue
			}
			conflict := false
			for j, other := range requests {
				if i == j || level >= len(other.candidates) {
					continue
				}
				if other.candidates[level] == candidate {
					conflict = true
					break
				}
			}
			if !conflict {
				chosen = candidate
				break
			}
		}
		if chosen == "" {
			chosen = "dimension-" + cliName(strings.ReplaceAll(strings.TrimPrefix(req.id, "/"), "/", "-"))
			base := chosen
			for n := 2; reserved[chosen]; n++ {
				chosen = fmt.Sprintf("%s-%d", base, n)
			}
		}
		reserved[chosen] = true
		names[req.id] = artifactFlagNames{Key: chosen}
	}
	return names
}

func artifactReservedFlags(cmd *cobra.Command) map[string]bool {
	reserved := map[string]bool{}
	peers := append([]*cobra.Command{cmd}, cmd.Root().Commands()...)
	for _, peer := range peers {
		if peer != cmd && !slices.Contains([]string{"list", "check", "generate", "up", "shell", "agent"}, peer.Name()) {
			continue
		}
		flags := copyCommandFlags(peer, "reserved artifact flags")
		flags.VisitAll(func(f *pflag.Flag) {
			if len(f.Annotations[artifactDimensionFlag]) == 0 {
				reserved[f.Name] = true
			}
		})
	}
	return reserved
}

func artifactModuleFlagNames(cmd *cobra.Command, dimensions artifact.Dimensions, paths []artifactListPath) map[string]string {
	reserved := artifactReservedFlags(cmd)
	reserved[artifact.ModuleDimension] = true
	for _, names := range artifactDimensionFlagNames(cmd, dimensions) {
		reserved[names.Key] = true
	}
	modules := map[string]bool{}
	counts := map[string]int{}
	for _, path := range paths {
		if path.ModuleName != "" && !modules[path.ModuleName] {
			modules[path.ModuleName] = true
			counts[cliName(path.ModuleName)]++
		}
	}
	names := map[string]string{}
	for module := range modules {
		alias := cliName(module)
		names[module] = "by-" + module
		if alias != "" && !reserved[alias] && !strings.HasPrefix(alias, "by-") && counts[alias] == 1 {
			names[module] = alias
		}
	}
	return names
}

func artifactSelectorDimensions(dimensions artifact.Dimensions) artifact.Dimensions {
	dimensions = slices.Clone(dimensions)
	for _, typ := range []string{"Check", "Generator", "Service", "Container", "Directory", "Expertise"} {
		id := "type:" + typ
		if !slices.ContainsFunc(dimensions, func(d *artifact.Dimension) bool { return d.Identifier == id }) {
			dimensions = append(dimensions, &artifact.Dimension{Identifier: id, Kind: "TYPE", Name: cliName(typ), ItemType: typ})
		}
	}
	return dimensions
}

func registerArtifactModuleFlags(cmd *cobra.Command, dimensions, selected artifact.Dimensions, paths []artifactListPath) {
	names := artifactModuleFlagNames(cmd, dimensions, paths)
	for _, path := range paths {
		if path.ModuleName == "" || !slices.ContainsFunc(selected, func(d *artifact.Dimension) bool {
			return d.Kind == "TYPE" && slices.Contains(path.Dimensions, d.Identifier)
		}) {
			continue
		}
		qualified := "by-" + path.ModuleName
		if cmd.Flag(qualified) != nil {
			continue
		}
		name := names[path.ModuleName]
		usage := "Select artifacts from module " + path.ModuleName
		if name != qualified {
			usage += " (alias: --" + qualified + ")"
		}
		cmd.Flags().Bool(qualified, false, usage)
		flag := cmd.Flags().Lookup(qualified)
		flag.Hidden = name != qualified
		flag.Annotations = map[string][]string{artifactDimensionFlag: {artifact.ModuleDimension}, artifactSelectorKeyFlag: {path.ModuleName}}
		if name != qualified {
			alias := *flag
			alias.Name, alias.Hidden = name, false
			cmd.Flags().AddFlag(&alias)
		}
	}
}
