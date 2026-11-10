package daggercmd

import (
	"context"
	"maps"
	"slices"
	"strings"

	"dagger.io/dagger/core"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const artifactPresenceFlag = "dagger.io/artifact-presence"

type artifactFlagNames = flattening.FlagNames

// Allocate all names together. A flag has the same meaning in every peer command.
func artifactDimensionFlagNames(cmd *cobra.Command, dimensions artifact.Dimensions) map[string]artifactFlagNames {
	return flattening.DimensionFlags(artifactFlatteningDimensions(dimensions), artifactReservedFlags(cmd), cliName)
}

func artifactReservedFlags(cmd *cobra.Command) map[string]bool {
	reserved := map[string]bool{}
	peers := append([]*cobra.Command{cmd}, cmd.Root().Commands()...)
	for _, peer := range peers {
		if peer != cmd && !slices.Contains([]string{"list", "check", "generate", "start", "shell", "agent"}, peer.Name()) {
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
	modules := make([]string, 0, len(paths))
	for _, path := range paths {
		modules = append(modules, path.ModuleName)
	}
	return flattening.ModuleFlags(modules, artifactFlatteningDimensions(dimensions), artifactReservedFlags(cmd), cliName)
}

func artifactSelectorDimensions(dimensions artifact.Dimensions) artifact.Dimensions {
	selectors := flattening.SelectorDimensions(artifactFlatteningDimensions(dimensions), cliName)
	dimensions = slices.Clone(dimensions)
	for _, d := range selectors[len(dimensions):] {
		dimensions = append(dimensions, &artifact.Dimension{
			Identifier: d.Identifier, Kind: d.Kind, Name: d.Name, ItemType: d.ItemType,
		})
	}
	return dimensions
}

func artifactFlatteningDimensions(dimensions artifact.Dimensions) flattening.Dimensions {
	result := make(flattening.Dimensions, len(dimensions))
	for i, d := range dimensions {
		result[i] = &flattening.Dimension{
			Kind: d.Kind, CollectionType: d.CollectionType, Identifier: d.Identifier,
			Name: d.Name, QualifiedName: d.QualifiedName, ItemType: d.ItemType,
			KeyName: d.KeyName, KeyDescription: d.KeyDescription,
		}
	}
	return result
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

// artifactFlagModules returns the workspace modules that args select with
// module flags (--module=NAME, --by-NAME or the --NAME alias), before
// discovery has registered those flags. It returns nil when args select no
// module, or name one that modules doesn't have.
func artifactFlagModules(cmd *cobra.Command, args, modules []string) []string {
	flags := copyCommandFlags(cmd, "module selectors")
	reserved := artifactReservedFlags(cmd)
	counts := map[string]int{}
	for _, module := range modules {
		counts[cliName(module)]++
	}
	selectors := map[string]string{}
	for _, module := range modules {
		names := []string{"by-" + module}
		if alias := cliName(module); alias != "" && !reserved[alias] && !strings.HasPrefix(alias, "by-") && counts[alias] == 1 {
			names = append(names, alias)
		}
		for _, name := range names {
			if flags.Lookup(name) == nil {
				flags.Bool(name, false, "")
				selectors[name] = module
			}
		}
	}
	var selected []string
	known := true
	err := flags.ParseAll(args, func(flag *pflag.Flag, value string) error {
		if module, ok := selectors[flag.Name]; ok && value == "true" {
			selected = append(selected, module)
		} else if flag.Name == "module" {
			known = known && slices.Contains(modules, value)
			selected = append(selected, value)
		}
		return nil
	})
	if err != nil || !known {
		return nil
	}
	slices.Sort(selected)
	return slices.Compact(selected)
}

// artifactNamingModules adds to modules every workspace module whose
// artifacts can change the flag names of theirs. Names are allocated across
// every dimension at once, and each module's dimension flags start with its
// own name, so two modules' flags can meet only when one name extends the
// other at a hyphen: a module go-sdk can take a name from go. It returns nil
// when that is every module.
func artifactNamingModules(modules, all []string) []string {
	related := func(a, b string) bool {
		a, b = cliName(a), cliName(b)
		return a == b || strings.HasPrefix(a, b+"-") || strings.HasPrefix(b, a+"-")
	}
	scope := slices.Clone(modules)
	for i := 0; i < len(scope); i++ {
		for _, module := range all {
			if !slices.Contains(scope, module) && related(scope[i], module) {
				scope = append(scope, module)
			}
		}
	}
	if !slices.ContainsFunc(all, func(module string) bool { return !slices.Contains(scope, module) }) {
		return nil
	}
	slices.Sort(scope)
	return scope
}

// workspaceModuleNames lists the modules of the workspace's effective config.
func workspaceModuleNames(ctx context.Context, ws *core.Workspace) ([]string, error) {
	cfg, err := artifactWorkspaceConfig(ctx, ws)
	if err != nil {
		return nil, err
	}
	return slices.Sorted(maps.Keys(cfg.Modules)), nil
}
