package daggercmd

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const artifactCollectionFlag = "dagger.io/artifact-collection"

var artifactsCmd = newArtifactsCommand()

func newArtifactsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "artifacts",
		Short: "List and filter workspace artifacts",
		Long: `List artifact addresses without evaluating their values.

A path selects that path and its children. Use / between fields; : is also
accepted. Glob patterns work as with check and up. Quote them to keep the
shell from expanding them.

Use --type to select a GraphQL type. Each collection adds a flag with its name,
such as --go-module. Repeat a flag to match any of its values. Different filters
must all match. Use --collection-key COLLECTION=KEY if a collection name
conflicts with an existing flag.

Examples:
  dagger artifacts list
  dagger artifacts list engine-dev --type Container
  dagger artifacts list 'go*/**' --type Container --type Directory
  dagger artifacts types
  dagger artifacts collections
  dagger artifacts keys go-test --go-module=sdk/go`,
		Args: cobra.NoArgs,
	}
	cmd.PersistentFlags().StringArrayP("type", "t", nil, "Keep artifacts of this GraphQL type (repeat for alternatives)")
	cmd.PersistentFlags().StringArray("collection-key", nil, "Keep a collection key: COLLECTION=KEY (repeat for alternatives)")
	for _, child := range []struct{ use, short string }{
		{"list [pattern...]", "List matching artifact addresses"},
		{"types [pattern...]", "List types of matching artifacts"},
		{"collections [pattern...]", "List collections of matching artifacts"},
		{"keys COLLECTION [pattern...]", "List keys in a collection of matching artifacts"},
	} {
		args := cobra.ArbitraryArgs
		if strings.HasPrefix(child.use, "keys ") {
			args = cobra.MinimumNArgs(1)
		}
		cmd.AddCommand(&cobra.Command{
			Use:               child.use,
			Short:             child.short,
			Args:              args,
			RunE:              runArtifacts,
			ValidArgsFunction: cobra.NoFileCompletions,
		})
	}
	setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace)
	return cmd
}

func registerArtifactCollectionFlags(cmd *cobra.Command, collections []string) {
	for _, collection := range collections {
		if cmd.Flag(collection) != nil {
			continue // --collection-key still accepts this collection.
		}
		cmd.PersistentFlags().StringArray(collection, nil, "Keep this "+collection+" key (repeat for alternatives)")
		cmd.PersistentFlags().Lookup(collection).Annotations = map[string][]string{
			artifactCollectionFlag: {collection},
		}
	}
}

func selectArtifactFilters(cmd *cobra.Command, artifacts *dagger.Artifacts) (*dagger.Artifacts, error) {
	types, _ := cmd.Flags().GetStringArray("type")
	if cmd.Flags().Changed("type") {
		artifacts = artifacts.FilterTypes(types)
	}
	keys, _ := cmd.Flags().GetStringArray("collection-key")
	byCollection := map[string][]string{}
	for _, key := range keys {
		collection, value, ok := strings.Cut(key, "=")
		if !ok || collection == "" {
			return nil, fmt.Errorf("invalid collection key %q: expected COLLECTION=KEY", key)
		}
		byCollection[collection] = append(byCollection[collection], value)
	}
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if collection := flag.Annotations[artifactCollectionFlag]; len(collection) > 0 {
			values, _ := cmd.Flags().GetStringArray(flag.Name)
			byCollection[collection[0]] = append(byCollection[collection[0]], values...)
		}
	})
	for _, collection := range slices.Sorted(maps.Keys(byCollection)) {
		artifacts = artifacts.FilterCollectionKeys(collection, byCollection[collection])
	}
	return artifacts, nil
}

func runArtifacts(cmd *cobra.Command, patterns []string) error {
	var collection string
	if cmd.Name() == "keys" {
		collection, patterns = patterns[0], patterns[1:]
	}
	return withEngine(cmd.Context(), client.Params{SkipWorkspaceModules: true}, func(ctx context.Context, ec *client.Client) error {
		artifacts, err := selectArtifactFilters(cmd, ec.Dagger().CurrentWorkspace().Artifacts(dagger.WorkspaceArtifactsOpts{Include: patterns}))
		if err != nil {
			return err
		}
		var lines []string
		switch cmd.Name() {
		case "types":
			lines, err = artifacts.Types(ctx)
		case "collections":
			lines, err = artifacts.Collections(ctx)
		case "keys":
			lines, err = artifacts.CollectionKeys(ctx, collection)
		default:
			lines, err = artifacts.Pretty(ctx)
		}
		if err != nil {
			return err
		}
		for _, line := range lines {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), line); err != nil {
				return err
			}
		}
		return nil
	})
}
