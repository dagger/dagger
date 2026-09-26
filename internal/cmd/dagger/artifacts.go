package daggercmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/engine/client"
	"github.com/jinzhu/inflection"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const artifactSelectorKeyFlag = "dagger.io/artifact-selector-key"

const artifactDimensionFlag = "dagger.io/artifact-dimension"

const artifactListType = "dagger.io/list-type"

var listCmd = newListCommand()

func init() {
	inflection.AddUncountable("expertise")
}

func newListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [TYPE]",
		Short: "List artifacts",
		Long:  "List artifacts by type.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && !cmd.Flags().Changed("type") && len(artifactKeyFlags(cmd)) == 0 {
				return cmd.Help()
			}
			return runArtifacts(cmd, args)
		},
		ValidArgsFunction: cobra.NoFileCompletions,
	}
	cmd.AddGroup(&cobra.Group{ID: "types", Title: "Types:"})
	cmd.PersistentFlags().StringArrayP("type", "t", nil, "Select artifacts of this `TYPE` (repeat to select more)")
	registerArtifactListFlags(cmd)
	cmd.Flags().StringP("format", "f", "table", "Output `FORMAT`: table, link, or cli")
	setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace, mayReadWorkspaceConfig)
	return cmd
}

func registerArtifactListFlags(cmd *cobra.Command) {
	cmd.Flags().StringArray("module", nil, "Select artifacts from module `NAME`")
	cmd.Flags().Lookup("module").Annotations = map[string][]string{artifactDimensionFlag: {artifact.ModuleDimension}}
	// Both aliases share one value, including explicit false.
	absolute := new(bool)
	cmd.Flags().BoolVar(absolute, "absolute", false, "Show absolute links (alias: --abs)")
	cmd.Flags().BoolVar(absolute, "abs", false, "Alias for --absolute")
	cmd.Flags().Lookup("abs").Hidden = true
}

// Register only the allocated names.
func registerArtifactDimensionHelp(cmd *cobra.Command, dimensions, selected artifact.Dimensions) {
	names := artifactDimensionFlagNames(cmd, dimensions)
	var types []string
	for _, dimension := range dimensions {
		if dimension.Kind == "TYPE" {
			types = append(types, dimension.ItemType)
		}
	}
	slices.Sort(types)
	typeCommands := artifactTypeCommands(slices.Compact(types))
	for _, dimension := range selected {
		allocated := names[dimension.Identifier]
		if dimension.Kind == "MODULE" {
			continue
		}
		if cmd.Flag(allocated.Key) == nil {
			values := "dagger list --type=" + dimension.ItemType
			for name, typ := range typeCommands {
				if typ == dimension.ItemType {
					values = "dagger list " + name
					break
				}
			}
			usage := fmt.Sprintf("Select %s by `name`. values: '%s'", artifactItemLabel(dimension.ItemType), values)
			cmd.Flags().StringArray(allocated.Key, nil, usage)
			cmd.Flags().Lookup(allocated.Key).Annotations = map[string][]string{artifactDimensionFlag: {dimension.Identifier}}
		}
	}
}

func artifactItemLabel(typeName string) string {
	if typeName == "" {
		return "items"
	}
	words := strings.Split(cliName(typeName), "-")
	if len(words) > 1 {
		// Keep the author's prefix casing: GoModule -> Go modules.
		words[0] = typeName[:len(words[0])]
	}
	words[len(words)-1] = inflection.Plural(words[len(words)-1])
	return strings.Join(words, " ")
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

func artifactPaths(addresses []*dagaddress.Address, keys ...dagaddress.Pair) []string {
	var paths []string
	for _, addr := range addresses {
		if addr.Path == "" {
			// Module selectors are known before schema discovery. Reuse include
			// narrowing without guessing which module owns a type.
			var modules []string
			for _, pair := range slices.Concat(addr.Query, keys) {
				if pair.Dimension != artifact.ModuleDimension {
					return nil // Preserve the schema scope used to bind other dimensions.
				}
				if pair.HasKey {
					modules = append(modules, pair.Key)
				}
			}
			if len(modules) == 0 {
				return nil // An unscoped address includes every path.
			}
			paths = append(paths, modules...)
			continue
		}
		paths = append(paths, addr.Path)
	}
	return paths
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

func artifactKeyFlags(cmd *cobra.Command) []dagaddress.Pair {
	var pairs []dagaddress.Pair
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if dimension := flag.Annotations[artifactDimensionFlag]; len(dimension) > 0 {
			if key := flag.Annotations[artifactSelectorKeyFlag]; len(key) > 0 {
				if flag.Value.String() == "true" {
					pairs = append(pairs, dagaddress.Pair{Dimension: dimension[0], Key: key[0], HasKey: true})
				}
				return
			}
			// GetStringArray serializes through CSV and loses a single empty key.
			values := flag.Value.(pflag.SliceValue).GetSlice()
			for _, value := range values {
				pairs = append(pairs, dagaddress.Pair{Dimension: dimension[0], Key: value, HasKey: true})
			}
		}
	})
	return pairs
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
	for _, typ := range types {
		if name := cliName(typ); name != typ {
			types = append(types, name)
		}
	}
	return types, cobra.ShellCompDirectiveNoFileComp
}

func runArtifacts(cmd *cobra.Command, addresses []string) error {
	parsed, err := parseArtifactAddresses(addresses)
	if err != nil {
		return err
	}
	params, err := artifactClientParams(client.Params{SkipWorkspaceModules: true}, addresses)
	if err != nil {
		return err
	}
	flags := artifactKeyFlags(cmd)
	return withEngine(cmd.Context(), params, func(ctx context.Context, ec *client.Client) error {
		workspaceID, err := ec.Dagger().CurrentWorkspace().ID(ctx)
		if err != nil {
			return err
		}
		ws := dagger.Ref[*dagger.Workspace](ec.Dagger(), workspaceID)
		// Flags bind in the combined path scope; address queries bind in their own scope.
		defs, err := artifactDimensions(ctx, ec.Dagger(), ws.Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths(parsed, flags...)}))
		if err != nil {
			return err
		}
		if err := bindArtifactDimensions(flags, defs); err != nil {
			return err
		}
		var selected *dagger.Artifacts
		for _, addr := range parsed {
			artifacts := ws.Artifacts(dagger.WorkspaceArtifactsOpts{Include: artifactPaths([]*dagaddress.Address{addr}, flags...)})
			var pathDefs artifact.Dimensions
			if len(addr.Query) > 0 {
				pathDefs, err = artifactDimensions(ctx, ec.Dagger(), artifacts)
				if err != nil {
					return err
				}
				if err := bindArtifactDimensions(addr.Query, pathDefs); err != nil {
					return err
				}
			}
			artifacts = listArtifactTargets(artifacts, cmd)
			filter := *addr
			filter.Query = append(slices.Clone(addr.Query), flags...)
			if slices.ContainsFunc(filter.Query, func(p dagaddress.Pair) bool { return p.HasKey && strings.HasPrefix(p.Dimension, "type:") }) {
				paths, err := readArtifactListPaths(ctx, ec.Dagger(), artifacts)
				if err != nil {
					return err
				}
				artifacts, err = filterArtifactTypeKeys(artifacts, paths, &filter)
				if err != nil {
					return err
				}
			}
			artifacts = applyArtifactFilters(cmd, &filter, nil, artifacts)
			if selected == nil {
				selected = artifacts
			} else {
				selected = selected.WithArtifacts(artifacts)
			}
		}
		return listArtifactSelection(ctx, ec.Dagger(), selected, cmd)
	})
}

func listArtifactTargets(all *dagger.Artifacts, cmd *cobra.Command) *dagger.Artifacts {
	if typeName := cmd.Annotations[artifactListType]; typeName != "" {
		return all.FilterTypes([]string{typeName})
	}
	return all
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
		Query:     `query($id: ID!) { node(id: $id) { ... on Artifacts { dimensionDefinitions { kind identifier name qualifiedName itemType } } } }`,
		Variables: map[string]any{"id": id},
	}, &dagger.Response{Data: &res})
	return res.Node.DimensionDefinitions, err
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
	items, err := readListedArtifacts(ctx, dag, artifacts, absolute, false)
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
	ModuleFlag       string            `json:"-"`
	OmitCLITypeKey   bool              `json:"-"`
	DisplayKeys      map[string]string `json:"-"`
	URI, Description string
	DimensionKeys    []struct{ Dimension, Key string }
}

func readListedArtifacts(ctx context.Context, dag *dagger.Client, selection *dagger.Artifacts, absolute, typed bool) ([]listedArtifact, error) {
	id, err := selection.ID(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Node struct{ Items []listedArtifact }
	}
	err = dag.Do(ctx, &dagger.Request{Query: `query($id: ID!, $absolute: Boolean!, $typeAssertion: Boolean!) {
  node(id: $id) { ... on Artifacts { items { uri(absolute: $absolute, typeAssertion: $typeAssertion) description dimensionKeys { dimension key } } } }
 }`, Variables: map[string]any{"id": id, "absolute": absolute, "typeAssertion": typed}}, &dagger.Response{Data: &response})
	return response.Node.Items, err
}

func registerArtifactFilters(ctx context.Context, dag *dagger.Client, cmd *cobra.Command, dimensions, selected artifact.Dimensions, all *dagger.Artifacts) error {
	dimensions = artifactSelectorDimensions(dimensions)
	selected = slices.Clone(selected)
	types := commandArtifactTypes(cmd)
	if cmd.Name() == "list" {
		selected = artifactSelectorDimensions(selected)
	}
	if typ := cmd.Annotations[artifactListType]; typ != "" {
		types = append(types, typ)
	}
	for _, d := range dimensions {
		if slices.Contains(types, d.ItemType) && !slices.ContainsFunc(selected, func(s *artifact.Dimension) bool { return s.Identifier == d.Identifier }) {
			selected = append(selected, d)
		}
	}
	if !slices.ContainsFunc(selected, func(d *artifact.Dimension) bool { return d.Kind == "MODULE" }) {
		selected = append(selected, &artifact.Dimension{Identifier: artifact.ModuleDimension, Kind: "MODULE", Name: "module"})
	}
	registerArtifactDimensionHelp(cmd, dimensions, selected)
	paths, err := readArtifactListPaths(ctx, dag, all)
	if err != nil {
		return err
	}
	registerArtifactModuleFlags(cmd, dimensions, selected, paths)
	return nil
}
