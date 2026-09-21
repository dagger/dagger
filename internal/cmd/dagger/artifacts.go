package daggercmd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/engine/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const artifactDimensionFlag = "dagger.io/artifact-dimension"

const artifactListType = "dagger.io/list-type"
const artifactListDimension = "dagger.io/list-dimension"

var listCmd = newListCommand()

func newListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [TYPE | DIMENSION]",
		Short: "List artifacts or collection values",
		Long:  "List artifacts by type, or values for a collection dimension. Use -a to list all artifacts.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if !all {
				if len(args) > 0 {
					return fmt.Errorf("unknown list type or dimension %q; see 'dagger list --help'", args[0])
				}
				return cmd.Help()
			}
			return runArtifacts(cmd, args)
		},
		ValidArgsFunction: cobra.NoFileCompletions,
	}
	cmd.AddGroup(&cobra.Group{ID: "types", Title: "Types:"}, &cobra.Group{ID: "dimensions", Title: "Dimensions:"})
	cmd.Flags().BoolP("all", "a", false, "List all artifacts, optionally filtered by address")
	cmd.PersistentFlags().StringArrayP("type", "t", nil, "Select artifacts of this `TYPE` (repeat to select more)")
	registerArtifactListFlags(cmd)
	cmd.Flags().StringP("format", "f", "table", "Output `FORMAT`: table, link, or cli")
	setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace, mayReadWorkspaceConfig)
	return cmd
}

// Both names share one value, including when either flag is explicitly false.
func registerArtifactListFlags(cmd *cobra.Command) {
	absolute := new(bool)
	cmd.Flags().BoolVar(absolute, "absolute", false, "Show absolute links (alias: --abs)")
	cmd.Flags().BoolVar(absolute, "abs", false, "Alias for --absolute")
	cmd.Flags().Lookup("abs").Hidden = true
}

func registerArtifactDimensionFlags(cmd *cobra.Command, dimensions []string) {
	for _, dimension := range dimensions {
		if cmd.Flag(dimension) != nil {
			continue // Keep the command flag; use another dimension name or a link query.
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
	flags, err := artifactKeyFlags(cmd)
	if err != nil {
		return nil, err
	}
	return applyArtifactFilters(cmd, addr, flags, artifacts), nil
}

func applyArtifactFilters(cmd *cobra.Command, addr *dagaddress.Address, flags []dagaddress.Pair, artifacts *dagger.Artifacts) *dagger.Artifacts {
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
	filter.Query = append(filter.Query, flags...)
	return artifacts.FilterURI(filter.String())
}

func artifactKeyFlags(cmd *cobra.Command) ([]dagaddress.Pair, error) {
	var pairs []dagaddress.Pair
	var keys []string
	if flag := cmd.Flag("dimension-key"); flag != nil {
		keys = flag.Value.(pflag.SliceValue).GetSlice()
	}
	for _, key := range keys {
		dimension, value, ok := strings.Cut(key, "=")
		if !ok || dimension == "" {
			return nil, fmt.Errorf("invalid dimension key %q: expected DIMENSION=KEY", key)
		}
		pairs = append(pairs, dagaddress.Pair{Dimension: dimension, Key: value, HasKey: true})
	}
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if dimension := flag.Annotations[artifactDimensionFlag]; len(dimension) > 0 {
			// GetStringArray serializes through CSV and loses a single empty key.
			values := flag.Value.(pflag.SliceValue).GetSlice()
			for _, value := range values {
				pairs = append(pairs, dagaddress.Pair{Dimension: dimension[0], Key: value, HasKey: true})
			}
		}
	})
	return pairs, nil
}

func completeArtifactTypes(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
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
		definitions, err := readArtifactTypes(ctx, ec.Dagger(), ec.Dagger().CurrentWorkspace().Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths(addresses)}))
		types = artifactTypeNames(definitions)
		return err
	})
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return types, cobra.ShellCompDirectiveNoFileComp
}

func runArtifacts(cmd *cobra.Command, addresses []string) error {
	dimension := cmd.Annotations[artifactListDimension]
	parsed, err := parseArtifactAddresses(addresses)
	if err != nil {
		return err
	}
	params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, addresses)
	if err != nil {
		return err
	}
	flags, err := artifactKeyFlags(cmd)
	if err != nil {
		return err
	}
	return withEngine(cmd.Context(), params, func(ctx context.Context, ec *client.Client) error {
		// Flags bind in the combined path scope; address queries bind in their own scope.
		if len(flags) > 0 || dimension != "" {
			defs, err := artifactDimensions(ctx, ec.Dagger(), ec.Dagger().CurrentWorkspace().Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths(parsed)}))
			if err != nil {
				return err
			}
			if err := validateArtifactDimensionFlags(cmd, defs); err != nil {
				return err
			}
			if err := bindArtifactDimensions(flags, defs); err != nil {
				return err
			}
			if dimension != "" {
				dimension, err = defs.Resolve(dimension)
				if err != nil {
					return err
				}
			}
		}
		var lines []commandListItem
		for _, addr := range parsed {
			artifacts := ec.Dagger().CurrentWorkspace().Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths([]*dagaddress.Address{addr})})
			if len(addr.Query) > 0 {
				pathDefs, err := artifactDimensions(ctx, ec.Dagger(), artifacts)
				if err != nil {
					return err
				}
				if err := bindArtifactDimensions(addr.Query, pathDefs); err != nil {
					return err
				}
			}
			if typeName := cmd.Annotations[artifactListType]; typeName != "" {
				artifacts = artifacts.FilterTypes([]string{typeName})
			}
			artifacts = applyArtifactFilters(cmd, addr, flags, artifacts)
			var selected []string
			if dimension != "" {
				selected, err = artifacts.DimensionKeys(ctx, dimension)
			} else {
				absolute, _ := cmd.Flags().GetBool("absolute")
				var items []listedArtifact
				items, err = readListedArtifacts(ctx, ec.Dagger(), artifacts, absolute)
				for _, item := range items {
					lines = append(lines, commandListItem{Name: item.URI, Comment: firstDescriptionLine(item.Description)})
				}
			}
			if err != nil {
				return err
			}
			for _, name := range selected {
				lines = append(lines, commandListItem{Name: name})
			}
		}
		slices.SortFunc(lines, func(a, b commandListItem) int { return strings.Compare(a.Name, b.Name) })
		lines = slices.CompactFunc(lines, func(a, b commandListItem) bool { return a.Name == b.Name })
		return writeCommandList(cmd.OutOrStdout(), lines)
	})
}

func artifactDimensions(ctx context.Context, dag *dagger.Client, artifacts *dagger.Artifacts) (artifact.Dimensions, error) {
	id, err := artifacts.ID(ctx)
	if err != nil {
		return nil, err
	}
	var res struct {
		Node struct{ DimensionDefinitions artifact.Dimensions }
	}
	err = dag.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on Artifacts { dimensionDefinitions { identifier name qualifiedName } } } }`,
		Variables: map[string]any{"id": id},
	}, &dagger.Response{Data: &res})
	return res.Node.DimensionDefinitions, err
}

func validateArtifactDimensionFlags(cmd *cobra.Command, defs artifact.Dimensions) error {
	var err error
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if err != nil || len(flag.Annotations[artifactDimensionFlag]) == 0 {
			return
		}
		var id string
		id, err = defs.Resolve(flag.Name)
		if err != nil {
			return
		}
		if !slices.ContainsFunc(defs, func(def *artifact.Dimension) bool { return def.Identifier == id }) {
			err = fmt.Errorf("unknown flag: --%s", flag.Name)
		}
	})
	return err
}

func bindArtifactDimensions(pairs []dagaddress.Pair, defs artifact.Dimensions) error {
	for i := range pairs {
		id, err := defs.Resolve(pairs[i].Dimension)
		if err != nil {
			return err
		}
		pairs[i].Dimension = id
	}
	return nil
}

func printArtifactDimensions(cmd *cobra.Command, defs artifact.Dimensions) error {
	if len(defs) == 0 {
		return nil
	}
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "NAME\tIDENTIFIER"); err != nil {
		return err
	}
	for _, def := range defs {
		name := defs.DisplayName(def)
		if _, err := fmt.Fprintf(writer, "%s\t%s\n", name, def.Identifier); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func readArtifactTypes(ctx context.Context, dag *dagger.Client, artifacts *dagger.Artifacts) ([]commandListItem, error) {
	id, err := artifacts.ID(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Node struct {
			Types []struct {
				Name     string
				AsObject struct{ Description string }
			}
		}
	}
	err = dag.Do(ctx, &dagger.Request{
		Query: `query($id: ID!) { node(id: $id) { ... on Artifacts {
  types { name asObject { description } }
 } } }`,
		Variables: map[string]any{"id": id},
	}, &dagger.Response{Data: &response})
	items := make([]commandListItem, 0, len(response.Node.Types))
	for _, typ := range response.Node.Types {
		items = append(items, commandListItem{Name: typ.Name, Comment: firstDescriptionLine(typ.AsObject.Description)})
	}
	return items, err
}

func artifactTypeNames(types []commandListItem) []string {
	names := make([]string, len(types))
	for i, typ := range types {
		names[i] = typ.Name
	}
	return names
}

// artifactURIs prints one address per artifact, in the form filterUri accepts.
func artifactURIs(ctx context.Context, dag *dagger.Client, artifacts *dagger.Artifacts, absolute bool) ([]string, error) {
	items, err := readListedArtifacts(ctx, dag, artifacts, absolute)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, item.URI)
	}
	return lines, nil
}

type listedArtifact struct {
	CLIFlagsOnly     bool `json:"-"`
	URI, Description string
	DimensionKeys    []struct{ Dimension, Key string }
}

func readListedArtifacts(ctx context.Context, dag *dagger.Client, selection *dagger.Artifacts, absolute bool) ([]listedArtifact, error) {
	id, err := selection.ID(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Node struct{ Items []listedArtifact }
	}
	err = dag.Do(ctx, &dagger.Request{Query: `query($id: ID!, $absolute: Boolean!) {
  node(id: $id) { ... on Artifacts { items { uri(absolute: $absolute) description dimensionKeys { dimension key } } } }
 }`, Variables: map[string]any{"id": id, "absolute": absolute}}, &dagger.Response{Data: &response})
	return response.Node.Items, err
}
