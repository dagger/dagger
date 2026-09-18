package daggercmd

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const artifactDimensionFlag = "dagger.io/artifact-dimension"

var artifactsCmd = newArtifactsCommand()

func newArtifactsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "artifacts",
		Short: "List and filter workspace artifacts",
		Long: `List artifact addresses without evaluating their values.

An address selects that path and its children. Use / between fields; : is
also accepted. The dag:// scheme is optional. Glob patterns work as with
check and up. Quote them to keep the shell from expanding them.

Use --type to select a GraphQL type. Each dimension adds a flag with its name,
such as --go-module. Repeat a flag to match any of its values. Different filters
must all match. Use --dimension-key DIMENSION=KEY if a dimension name
conflicts with an existing flag. A query in the address has the same meaning
as these flags: dag://<path>?<dimension>=<key>.

Examples:
  dagger artifacts list
  dagger artifacts list engine-dev --type Container
  dagger artifacts list 'go*/**' --type Container --type Directory
  dagger artifacts list 'dag://golang/modules/tests/container?go-module=sdk/go'
  dagger artifacts types
  dagger artifacts dimensions
  dagger artifacts keys go-test --go-module=sdk/go`,
		Args: cobra.NoArgs,
	}
	cmd.PersistentFlags().StringArrayP("type", "t", nil, "Keep artifacts of this GraphQL type (repeat for alternatives)")
	cmd.PersistentFlags().StringArray("dimension-key", nil, "Keep a dimension key: DIMENSION=KEY (repeat for alternatives)")
	for _, child := range []struct{ use, short string }{
		{"list [address...]", "List matching artifact addresses"},
		{"types [address...]", "List types of matching artifacts"},
		{"dimensions [address...]", "List dimensions of matching artifacts"},
		{"keys DIMENSION [address...]", "List keys in a dimension of matching artifacts"},
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

func registerArtifactDimensionFlags(cmd *cobra.Command, dimensions []string) {
	for _, dimension := range dimensions {
		if cmd.Flag(dimension) != nil {
			continue // --dimension-key still accepts this dimension.
		}
		cmd.PersistentFlags().StringArray(dimension, nil, "Keep this "+dimension+" key (repeat for alternatives)")
		cmd.PersistentFlags().Lookup(dimension).Annotations = map[string][]string{
			artifactDimensionFlag: {dimension},
		}
	}
}

// artifactSelection is the selection a command line describes: include
// patterns from the addresses' paths, and the type and dimension filters from
// the addresses and flags.
type artifactSelection struct {
	include []string
	types   []string
	// keys maps a dimension to its alternatives; nil selects any key.
	keys map[string][]string
}

// parseArtifactAddresses reads the positional addresses. Each path is an
// include pattern, so it selects that path and its children; the scheme's
// types and the query's dimension keys become filters.
func parseArtifactAddresses(addresses []string) (*artifactSelection, error) {
	sel := &artifactSelection{keys: map[string][]string{}}
	for _, address := range addresses {
		addr, err := dagaddress.Parse(address)
		if err != nil {
			return nil, err
		}
		if addr.Absolute {
			return nil, fmt.Errorf("absolute addresses are not supported yet: %s", address)
		}
		if addr.Path != "" {
			sel.include = append(sel.include, addr.Path)
		}
		for _, typ := range addr.Types {
			if !slices.Contains(sel.types, typ) {
				sel.types = append(sel.types, typ)
			}
		}
		for _, filter := range addr.DimensionFilters() {
			sel.addKeys(filter.Dimension, filter.Keys)
		}
	}
	return sel, nil
}

func (sel *artifactSelection) addKeys(dimension string, keys []string) {
	existing, seen := sel.keys[dimension]
	if seen && existing == nil {
		return
	}
	if keys == nil {
		sel.keys[dimension] = nil
		return
	}
	sel.keys[dimension] = append(existing, keys...)
}

func selectArtifactFilters(cmd *cobra.Command, sel *artifactSelection, artifacts *dagger.Artifacts) (*dagger.Artifacts, error) {
	types, _ := cmd.Flags().GetStringArray("type")
	if cmd.Flags().Changed("type") {
		artifacts = artifacts.FilterTypes(types)
	}
	if len(sel.types) > 0 {
		// Address types are in CLI case; filterUri matches them.
		artifacts = artifacts.FilterURI((&dagaddress.Address{Types: sel.types}).String())
	}
	keys, _ := cmd.Flags().GetStringArray("dimension-key")
	for _, key := range keys {
		dimension, value, ok := strings.Cut(key, "=")
		if !ok || dimension == "" {
			return nil, fmt.Errorf("invalid dimension key %q: expected DIMENSION=KEY", key)
		}
		sel.addKeys(dimension, []string{value})
	}
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if dimension := flag.Annotations[artifactDimensionFlag]; len(dimension) > 0 {
			values, _ := cmd.Flags().GetStringArray(flag.Name)
			sel.addKeys(dimension[0], values)
		}
	})
	for _, dimension := range slices.Sorted(maps.Keys(sel.keys)) {
		if sel.keys[dimension] == nil {
			artifacts = artifacts.FilterDimensions([]string{dimension})
		} else {
			artifacts = artifacts.FilterDimensionKeys(dimension, sel.keys[dimension])
		}
	}
	return artifacts, nil
}

func runArtifacts(cmd *cobra.Command, addresses []string) error {
	var dimension string
	if cmd.Name() == "keys" {
		dimension, addresses = addresses[0], addresses[1:]
	}
	sel, err := parseArtifactAddresses(addresses)
	if err != nil {
		return err
	}
	return withEngine(cmd.Context(), client.Params{SkipWorkspaceModules: true}, func(ctx context.Context, ec *client.Client) error {
		artifacts, err := selectArtifactFilters(cmd, sel, ec.Dagger().CurrentWorkspace().Artifacts(dagger.WorkspaceArtifactsOpts{Include: sel.include}))
		if err != nil {
			return err
		}
		var lines []string
		switch cmd.Name() {
		case "types":
			lines, err = artifacts.Types(ctx)
		case "dimensions":
			lines, err = artifacts.Dimensions(ctx)
		case "keys":
			lines, err = artifacts.DimensionKeys(ctx, dimension)
		default:
			lines, err = artifactURIs(ctx, ec.Dagger(), artifacts)
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

// artifactURIs prints one address per artifact, in the form filterUri accepts.
func artifactURIs(ctx context.Context, dag *dagger.Client, artifacts *dagger.Artifacts) ([]string, error) {
	id, err := artifacts.ID(ctx)
	if err != nil {
		return nil, err
	}
	var res struct {
		Node struct {
			Items []struct{ URI string }
		}
	}
	err = dag.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on Artifacts { items { uri } } } }`,
		Variables: map[string]any{"id": id},
	}, &dagger.Response{Data: &res})
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(res.Node.Items))
	for _, item := range res.Node.Items {
		lines = append(lines, item.URI)
	}
	return lines, nil
}
