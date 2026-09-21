package daggercmd

import (
	"context"
	"fmt"
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
		Use:     "artifact",
		Aliases: []string{"artifacts"},
		Short:   "List and filter workspace artifacts",
		Long: `List artifact addresses without evaluating their values.

An address selects that path and its children. Use / between fields; : is
also accepted. The dag:// scheme is optional. Glob patterns work as with
check and up. Quote them to keep the shell from expanding them.

Use --type to select a type in CLI or GraphQL case, such as container or Container.
Each dimension adds a flag with its name,
such as --go-module. Repeat a flag to match any of its values. Different filters
must all match. Use --dimension-key DIMENSION=KEY if a dimension name
conflicts with an existing flag. A query in the address has the same meaning
as these flags: dag://<path>?<dimension>=<key>.

Examples:
  dagger artifact list
  dagger artifact list engine-dev --type Container
  dagger artifact list 'go*/**' --type Container --type Directory
  dagger artifact list 'dag://golang/modules/tests/container?go-module=sdk/go'
  dagger artifact types
  dagger artifact dimensions
  dagger artifact keys go-test --go-module=sdk/go`,
		Args: cobra.NoArgs,
	}
	cmd.PersistentFlags().StringArrayP("type", "t", nil, "Keep artifacts of this `type` (CLI or GraphQL case; repeat for alternatives)")
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
		childCmd := &cobra.Command{
			Use:               child.use,
			Short:             child.short,
			Args:              args,
			RunE:              runArtifacts,
			ValidArgsFunction: cobra.NoFileCompletions,
		}
		if strings.HasPrefix(child.use, "list ") {
			registerArtifactListFlags(childCmd)
		}
		cmd.AddCommand(childCmd)
	}
	setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace, mayReadWorkspaceConfig)
	return cmd
}

// Both names share one value, including when either flag is explicitly false.
func registerArtifactListFlags(cmd *cobra.Command) {
	absolute := new(bool)
	cmd.Flags().BoolVar(absolute, "absolute", false, "List absolute artifact addresses, including the workspace and revision (alias: --abs)")
	cmd.Flags().BoolVar(absolute, "abs", false, "Alias for --absolute")
	cmd.Flags().Lookup("abs").Hidden = true
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

// Keep each address intact: its type and dimension filters apply only to its path.
func parseArtifactAddresses(addresses []string) ([]*dagaddress.Address, error) {
	if len(addresses) == 0 {
		addresses = []string{""}
	}
	var parsed []*dagaddress.Address
	for _, address := range addresses {
		addr, err := dagaddress.Parse(address)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, addr)
	}
	return parsed, nil
}

// artifactClientParams binds an absolute address before workspace discovery.
// One command uses one workspace; relative addresses use that same workspace.
func artifactClientParams(params client.Params, addresses []string) (client.Params, error) {
	parsed, err := parseArtifactAddresses(addresses)
	if err != nil {
		return params, err
	}
	var selected string
	for _, address := range parsed {
		if !address.Absolute {
			continue
		}
		ref := address.Workspace + "@" + address.Version
		if selected != "" && selected != ref {
			return params, fmt.Errorf("addresses select different workspaces: %s and %s", selected, ref)
		}
		selected = ref
	}
	if selected != "" {
		params.Workspace = &selected
	}
	return params, nil
}

func artifactPaths(addresses []*dagaddress.Address) []string {
	var paths []string
	for _, addr := range addresses {
		if addr.Path == "" {
			return nil // An unscoped address includes every path.
		}
		paths = append(paths, addr.Path)
	}
	return paths
}

func selectArtifactFilters(cmd *cobra.Command, addr *dagaddress.Address, artifacts *dagger.Artifacts) (*dagger.Artifacts, error) {
	types, _ := cmd.Flags().GetStringArray("type")
	if cmd.Flags().Changed("type") {
		names := make([]string, 0, len(types))
		for _, typ := range types {
			if typ != "" {
				names = append(names, cliName(typ))
			}
		}
		if len(names) == 0 {
			artifacts = artifacts.FilterTypes([]string{})
		} else {
			artifacts = artifacts.FilterURI((&dagaddress.Address{HasScheme: true, Types: names}).String())
		}
	}
	// Workspace.artifacts(include:) already selected the path and its children.
	// Send the remaining address and flag filters through the engine's parser.
	filter := *addr
	filter.Path = ""
	filter.Absolute = false
	filter.Query = slices.Clone(addr.Query)
	keys, _ := cmd.Flags().GetStringArray("dimension-key")
	for _, key := range keys {
		dimension, value, ok := strings.Cut(key, "=")
		if !ok || dimension == "" {
			return nil, fmt.Errorf("invalid dimension key %q: expected DIMENSION=KEY", key)
		}
		filter.Query = append(filter.Query, dagaddress.Pair{Dimension: dimension, Key: value, HasKey: true})
	}
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if dimension := flag.Annotations[artifactDimensionFlag]; len(dimension) > 0 {
			values, _ := cmd.Flags().GetStringArray(flag.Name)
			for _, value := range values {
				filter.Query = append(filter.Query, dagaddress.Pair{Dimension: dimension[0], Key: value, HasKey: true})
			}
		}
	})
	return artifacts.FilterURI(filter.String()), nil
}

func completeArtifactTypes(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if cmd.Name() == "keys" && len(args) > 0 {
		args = args[1:]
	}
	addresses, err := parseArtifactAddresses(args)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	var types []string
	params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, args)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	err = withEngineSilent(cmd.Context(), params, func(ctx context.Context, ec *client.Client) error {
		types, err = ec.Dagger().CurrentWorkspace().Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths(addresses)}).Types(ctx)
		return err
	})
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return types, cobra.ShellCompDirectiveNoFileComp
}

func runArtifacts(cmd *cobra.Command, addresses []string) error {
	var dimension string
	if cmd.Name() == "keys" {
		dimension, addresses = addresses[0], addresses[1:]
	}
	parsed, err := parseArtifactAddresses(addresses)
	if err != nil {
		return err
	}
	params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, addresses)
	if err != nil {
		return err
	}
	return withEngine(cmd.Context(), params, func(ctx context.Context, ec *client.Client) error {
		var lines []string
		for _, addr := range parsed {
			artifacts := ec.Dagger().CurrentWorkspace().Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths([]*dagaddress.Address{addr})})
			artifacts, err := selectArtifactFilters(cmd, addr, artifacts)
			if err != nil {
				return err
			}
			var selected []string
			switch cmd.Name() {
			case "types":
				selected, err = artifacts.Types(ctx)
			case "dimensions":
				selected, err = artifacts.Dimensions(ctx)
			case "keys":
				selected, err = artifacts.DimensionKeys(ctx, dimension)
			default:
				absolute, _ := cmd.Flags().GetBool("absolute")
				selected, err = artifactURIs(ctx, ec.Dagger(), artifacts, absolute)
			}
			if err != nil {
				return err
			}
			lines = append(lines, selected...)
		}
		slices.Sort(lines)
		lines = slices.Compact(lines)
		for _, line := range lines {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), line); err != nil {
				return err
			}
		}
		return nil
	})
}

// artifactURIs prints one address per artifact, in the form filterUri accepts.
func artifactURIs(ctx context.Context, dag *dagger.Client, artifacts *dagger.Artifacts, absolute bool) ([]string, error) {
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
		Query:     `query($id: ID!, $absolute: Boolean!) { node(id: $id) { ... on Artifacts { items { uri(absolute: $absolute) } } } }`,
		Variables: map[string]any{"id": id, "absolute": absolute},
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
