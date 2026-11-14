package daggercmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"dagger.io/dagger/core"

	"dagger.io/dagger"
	"github.com/Khan/genqlient/graphql"
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
const artifactListCollection = "dagger.io/list-collection"

var listCmd = newListCommand()

func init() {
	inflection.AddUncountable("expertise")
}

func newListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [TYPE | COLLECTION]",
		Short: "List artifacts or collection items",
		Long:  "List artifacts by type or collection. Use -a to expand collections.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && !cmd.Flags().Changed("type") && len(artifactKeyFlags(cmd)) == 0 {
				return cmd.Help()
			}
			return runArtifacts(cmd, args)
		},
		ValidArgsFunction: cobra.NoFileCompletions,
	}
	cmd.AddGroup(&cobra.Group{ID: "types", Title: "Types:"}, &cobra.Group{ID: "collections", Title: "Collections:"})
	cmd.PersistentFlags().StringArrayP("type", "t", nil, "Select artifacts of this `TYPE` (repeat to select more)")
	registerArtifactListFlags(cmd)
	cmd.Flags().StringP("format", "f", "table", "Output `FORMAT`: table, link, or cli")
	setCommandCapabilities(cmd, mayCallEngine, maySelectWorkspace, mayReadWorkspaceConfig)
	return cmd
}

func registerArtifactListFlags(cmd *cobra.Command) {
	cmd.Flags().StringArray("module", nil, "Select artifacts from module `NAME`")
	cmd.Flags().Lookup("module").Annotations = map[string][]string{artifactDimensionFlag: {artifact.ModuleDimension}}
	cmd.Flags().BoolP("all", "a", false, "Expand collections and list each item")
	// Both aliases share one value, including explicit false.
	absolute := new(bool)
	cmd.Flags().BoolVar(absolute, "absolute", false, "Show absolute links (alias: --abs)")
	cmd.Flags().BoolVar(absolute, "abs", false, "Alias for --absolute")
	cmd.Flags().Lookup("abs").Hidden = true
}

// Register only the allocated names. Plural flags select a collection; they do not take keys.
func registerArtifactDimensionHelp(cmd *cobra.Command, dimensions, selected artifact.Dimensions) {
	names := artifactDimensionFlagNames(cmd, dimensions)
	var types []string
	collections := map[string]bool{}
	for _, dimension := range dimensions {
		if dimension.Kind == "TYPE" {
			types = append(types, dimension.ItemType)
		} else if dimension.CollectionType != "" {
			types = append(types, dimension.CollectionType)
			collections[dimension.CollectionType] = true
		}
	}
	slices.Sort(types)
	typeCommands := artifactTypeCommands(slices.Compact(types), collections)
	for _, dimension := range selected {
		allocated := names[dimension.Identifier]
		if dimension.Kind == "MODULE" {
			continue
		}
		if dimension.Kind == "NAME" {
			if cmd.Flag(allocated.Key) == nil {
				cmd.Flags().StringArray(allocated.Key, nil, "Select artifacts by their full local field path `NAME`")
				cmd.Flags().Lookup(allocated.Key).Annotations = map[string][]string{artifactDimensionFlag: {dimension.Identifier}}
			}
			continue
		}
		if cmd.Flag(allocated.Key) == nil {
			key := cliName(dimension.KeyName)
			if key == "" {
				key = "key"
			}
			values := "dagger list " + cliName(dimension.CollectionType) + " -a"
			if dimension.Kind == "TYPE" {
				values = "dagger list --type=" + dimension.ItemType
				if !collections[dimension.ItemType] {
					for name, typ := range typeCommands {
						if typ == dimension.ItemType {
							values = "dagger list " + name
							break
						}
					}
				}
				key = "name"
			}
			usage := fmt.Sprintf("Select %s by `%s`. values: '%s'", artifactItemLabel(dimension.ItemType), key, values)
			if description := strings.TrimSpace(dimension.KeyDescription); description != "" {
				usage += "\n" + strings.ToUpper(key) + ": " + description
			}
			cmd.Flags().StringArray(allocated.Key, nil, usage)
			cmd.Flags().Lookup(allocated.Key).Annotations = map[string][]string{artifactDimensionFlag: {dimension.Identifier}}
		}
		if allocated.Presence != "" && cmd.Flag(allocated.Presence) == nil {
			cmd.Flags().Bool(allocated.Presence, false, "Select all "+artifactItemLabel(dimension.ItemType))
			cmd.Flags().Lookup(allocated.Presence).Annotations = map[string][]string{artifactDimensionFlag: {dimension.Identifier}, artifactPresenceFlag: {"true"}}
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
		// A type key can be a suffix. Resolve it against schema paths before
		// narrowing discovery; only an explicit address path is exact here.
		path := addr.Path
		if path == "" {
			// Module selectors are known before schema discovery. Reuse include
			// narrowing without guessing which module owns a type or collection.
			// A type needs no binding, and its key resolves among the selected
			// modules' paths either way.
			var modules []string
			for _, pair := range slices.Concat(addr.Query, keys) {
				if strings.HasPrefix(pair.Dimension, "type:") {
					continue
				}
				if pair.Dimension != artifact.ModuleDimension {
					return nil // Preserve the schema scope used to bind other dimensions.
				}
				if pair.Absent {
					return nil
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
		paths = append(paths, path)
	}
	return paths
}

func applyArtifactFilters(cmd *cobra.Command, addr *dagaddress.Address, artifacts *core.Artifacts) *core.Artifacts {
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
			artifacts = filterArtifactAddress(artifacts, &dagaddress.Address{Types: names})
		}
	}
	return filterArtifactAddress(artifacts, addr)
}

// filterArtifactAddress applies the type and dimension filters of an address.
// It does not apply the path: Workspace.artifacts(include:) selects it.
func filterArtifactAddress(artifacts *core.Artifacts, addr *dagaddress.Address) *core.Artifacts {
	if len(addr.Types) > 0 {
		// Only the engine knows which concrete types these names match.
		artifacts = artifacts.FilterURI((&dagaddress.Address{Types: addr.Types}).String())
	}
	for _, filter := range addr.DimensionFilters() {
		if filter.Absent {
			artifacts = artifacts.FilterURI((&dagaddress.Address{Query: []dagaddress.Pair{{Dimension: filter.Dimension, Absent: true}}}).String())
		} else if filter.Keys == nil {
			artifacts = artifacts.FilterDimensions([]string{filter.Dimension})
		} else {
			artifacts = artifacts.FilterDimensionKeys(filter.Dimension, filter.Keys)
		}
	}
	return artifacts
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
			if len(flag.Annotations[artifactPresenceFlag]) > 0 {
				if flag.Value.String() == "true" {
					pairs = append(pairs, dagaddress.Pair{Dimension: dimension[0]})
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
		definitions, err := readArtifactTypes(ctx, ec.Dagger(), core.NewQuery(ec.Dagger()).CurrentWorkspace().Artifacts(core.WorkspaceArtifactsOpts{Include: artifactPaths(addresses)}))
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
	return withArtifactEngine(cmd.Context(), params, func(ctx context.Context, ec *client.Client) error {
		workspaceID, err := core.NewQuery(ec.Dagger()).CurrentWorkspace().ID(ctx)
		if err != nil {
			return err
		}
		ws := core.Ref[*core.Workspace](core.NewQuery(ec.Dagger()), workspaceID)
		// Flags bind in the combined path scope; address queries bind in their own scope.
		defs, err := artifactDimensions(ctx, ec.Dagger(), ws.Artifacts(core.WorkspaceArtifactsOpts{Include: artifactPaths(parsed, flags...)}))
		if err != nil {
			return err
		}
		if err := validateArtifactDimensionFlags(cmd, defs); err != nil {
			return err
		}
		if err := bindArtifactDimensions(flags, defs); err != nil {
			return err
		}
		selected, err := commandArtifacts(ctx, ec.Dagger(), ws, cmd, addresses, false, flags...)
		if err != nil {
			return err
		}
		selected = listArtifactTargets(selected, cmd, defs)
		selected = applyArtifactFilters(cmd, &dagaddress.Address{}, selected)
		return listArtifactSelection(ctx, ec.Dagger(), selected, cmd)
	})
}

func listArtifactTargets(all *core.Artifacts, cmd *cobra.Command, definitions artifact.Dimensions) *core.Artifacts {
	if typeName := cmd.Annotations[artifactListType]; typeName != "" {
		return all.FilterTypes([]string{typeName})
	}
	if collectionType := cmd.Annotations[artifactListCollection]; collectionType != "" {
		var dimensions []string
		for _, dimension := range definitions {
			if dimension.CollectionType == collectionType {
				dimensions = append(dimensions, dimension.Identifier)
			}
		}
		return all.FilterDimensions(dimensions)
	}
	return all
}

func artifactDimensions(ctx context.Context, dag *dagger.Client, artifacts *core.Artifacts) (artifact.Dimensions, error) {
	id, err := artifacts.ID(ctx)
	if err != nil {
		return nil, err
	}
	var res struct {
		Node struct {
			DimensionDefinitions artifact.Dimensions
			Types                []struct{ Name string }
		}
	}
	err = dag.Do(ctx, &dagger.Request{
		Query:     `query($id: ID!) { node(id: $id) { ... on Artifacts { dimensionDefinitions { kind identifier name qualifiedName collectionType itemType keyName keyDescription } types { name } } } }`,
		Variables: map[string]any{"id": id},
	}, &dagger.Response{Data: &res})
	if err != nil {
		return nil, err
	}
	definitions := res.Node.DimensionDefinitions
	for _, typ := range res.Node.Types {
		id := "type:" + typ.Name
		if !slices.ContainsFunc(definitions, func(d *artifact.Dimension) bool { return d.Identifier == id }) {
			definitions = append(definitions, &artifact.Dimension{
				Identifier: id, Kind: "TYPE", Name: cliName(typ.Name),
				QualifiedName: "artifact-" + cliName(typ.Name), ItemType: typ.Name,
			})
		}
	}
	return definitions, nil
}

func validateArtifactDimensionFlags(cmd *cobra.Command, defs artifact.Dimensions) error {
	var err error
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if err != nil || len(flag.Annotations[artifactDimensionFlag]) == 0 {
			return
		}
		if artifact.IsStaticDimension(flag.Annotations[artifactDimensionFlag][0]) {
			return // A static selector can match no artifacts in this path scope.
		}
		var id string
		id, err = defs.Resolve(flag.Annotations[artifactDimensionFlag][0])
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

func readArtifactTypes(ctx context.Context, dag *dagger.Client, artifacts *core.Artifacts) ([]commandListItem, error) {
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
func artifactURIs(ctx context.Context, dag *dagger.Client, artifacts *core.Artifacts, absolute bool) ([]string, error) {
	items, err := readListedArtifactsMetadata(ctx, dag, artifacts, absolute, false)
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
	OmitTypeKey      bool              `json:"-"`
	OmitCLITypeKey   bool              `json:"-"`
	CLIPath          string            `json:"-"`
	Presence         []string          `json:"presence,omitempty"`
	DisplayKeys      map[string]string `json:"-"`
	PresenceNames    map[string]string `json:"-"`
	CollectionItem   bool              `json:"-"`
	URI, Description string
	ModuleName       string
	LoadError        string
	Name             string
	Coordinates      []struct{ Dimension, Key string }
	Path             []string
	DimensionKeys    []struct{ Dimension, Key string }
}

func (item *listedArtifact) keysFromAddress(definitions artifact.Dimensions) error {
	if item.DimensionKeys != nil {
		return nil
	}
	address, err := dagaddress.Parse(item.URI)
	if err != nil {
		return err
	}
	if item.ModuleName != "" {
		item.DimensionKeys = append(item.DimensionKeys, struct{ Dimension, Key string }{artifact.ModuleDimension, item.ModuleName})
	}
	var typeDimension string
	for _, pair := range address.Query {
		dimension, err := definitions.Resolve(pair.Dimension)
		if err != nil {
			return err
		}
		if strings.HasPrefix(dimension, "type:") {
			typeDimension = dimension
			continue
		}
		if pair.HasKey && dimension != artifact.ModuleDimension {
			item.DimensionKeys = append(item.DimensionKeys, struct{ Dimension, Key string }{dimension, pair.Key})
		}
	}
	if typeDimension != "" {
		item.DimensionKeys = append(item.DimensionKeys, struct{ Dimension, Key string }{typeDimension, strings.Join(item.Path, "/")})
	}
	return nil
}

// fullPath is the full path in the artifact's type key. URI is a projection,
// whose type key can be a short suffix; the dimension keys hold the full path.
func (item listedArtifact) fullPath() string {
	for _, key := range item.DimensionKeys {
		if strings.HasPrefix(key.Dimension, "type:") {
			return key.Key
		}
	}
	if len(item.Path) > 0 {
		return strings.Join(item.Path, "/")
	}
	if addr, err := dagaddress.Parse(item.URI); err == nil {
		return addr.ArtifactPath(addr.Types...)
	}
	return ""
}

func readListedArtifacts(ctx context.Context, dag *dagger.Client, selection *core.Artifacts, absolute, typed bool) ([]listedArtifact, error) {
	id, err := selection.ID(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Node struct {
			Items       []listedArtifact
			Definitions artifact.Dimensions
		}
	}
	err = dag.Do(ctx, &dagger.Request{Query: `query($id: ID!, $absolute: Boolean!, $typeAssertion: Boolean!) {
	 node(id: $id) { ... on Artifacts {
	   definitions: dimensionDefinitions { kind identifier name qualifiedName itemType }
	   items { uri(absolute: $absolute, typeAssertion: $typeAssertion) description moduleName path }
	 } }
 }`, Variables: map[string]any{"id": id, "absolute": absolute, "typeAssertion": typed}}, &dagger.Response{Data: &response})
	if err != nil {
		return nil, err
	}
	for i := range response.Node.Items {
		if err := response.Node.Items[i].keysFromAddress(response.Node.Definitions); err != nil {
			return nil, err
		}
	}
	return response.Node.Items, nil
}

// isMissingArtifactListingField reports whether an older engine rejected the
// query because it lacks __listing. The client returns validation failures as
// an HTTP error, before discovery runs. A resolver error must not trigger
// another query, even if its message quotes the validator.
func isMissingArtifactListingField(err error) bool {
	const missing = `Cannot query field "__listing" on type "Artifacts".`
	var httpErr *graphql.HTTPError
	if !errors.As(err, &httpErr) || len(httpErr.Response.Errors) != 1 {
		return false
	}
	gqlErr := httpErr.Response.Errors[0]
	return gqlErr != nil && len(gqlErr.Path) == 0 && gqlErr.Extensions["code"] == "GRAPHQL_VALIDATION_FAILED" && strings.HasPrefix(gqlErr.Message, missing)
}

func isMissingArtifactSchemaListing(err error) bool {
	if isMissingArtifactListingField(err) {
		return true
	}
	var httpErr *graphql.HTTPError
	if !errors.As(err, &httpErr) || len(httpErr.Response.Errors) != 1 {
		return false
	}
	gqlErr := httpErr.Response.Errors[0]
	return gqlErr != nil && len(gqlErr.Path) == 0 && gqlErr.Extensions["code"] == "GRAPHQL_VALIDATION_FAILED" &&
		strings.HasPrefix(gqlErr.Message, `Unknown argument "schema" on field "Artifacts.__listing".`)
}

func readArtifactSchemaListing(ctx context.Context, dag *dagger.Client, selection *core.Artifacts, absolute bool) ([]listedArtifact, bool, error) {
	id, err := selection.ID(ctx)
	if err != nil {
		return nil, false, err
	}
	var response struct{ Node struct{ Listing string } }
	err = dag.Do(ctx, &dagger.Request{Query: `query($id: ID!, $absolute: Boolean!) {
  node(id:$id) { ... on Artifacts { listing:__listing(schema:true, absolute:$absolute, typeAssertion:true) } }
}`, Variables: map[string]any{"id": id, "absolute": absolute}}, &dagger.Response{Data: &response})
	if isMissingArtifactSchemaListing(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var items []listedArtifact
	if err := json.Unmarshal([]byte(response.Node.Listing), &items); err != nil {
		return nil, true, err
	}
	return items, true, nil
}

func readListedArtifactsMetadata(ctx context.Context, dag *dagger.Client, selection *core.Artifacts, absolute, typed bool) ([]listedArtifact, error) {
	id, err := selection.ID(ctx)
	if err != nil {
		return nil, err
	}
	var response struct {
		Node struct {
			Listing     string
			Definitions artifact.Dimensions
		}
	}
	err = dag.Do(ctx, &dagger.Request{Query: `query($id: ID!, $absolute: Boolean!, $typeAssertion: Boolean!) {
	 node(id: $id) { ... on Artifacts {
	   definitions: dimensionDefinitions { kind identifier name qualifiedName itemType }
	   listing: __listing(absolute: $absolute, typeAssertion: $typeAssertion)
	 } }
 }`, Variables: map[string]any{"id": id, "absolute": absolute, "typeAssertion": typed}}, &dagger.Response{Data: &response})
	if err != nil {
		if isMissingArtifactListingField(err) {
			return readListedArtifacts(ctx, dag, selection, absolute, typed)
		}
		return nil, err
	}
	var items []listedArtifact
	if err := json.Unmarshal([]byte(response.Node.Listing), &items); err != nil {
		return nil, fmt.Errorf("decode artifact listing metadata: %w", err)
	}
	for i := range items {
		if err := items[i].keysFromAddress(response.Node.Definitions); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func readListedDimensionItems(ctx context.Context, dag *dagger.Client, selection *core.Artifacts, dimension string, absolute bool) ([]listedArtifact, error) {
	id, err := selection.ID(ctx)
	if err != nil {
		return nil, err
	}
	definitions, err := artifactDimensions(ctx, dag, selection)
	if err != nil {
		return nil, err
	}
	var itemType string
	for _, definition := range definitions {
		if definition.Identifier == dimension {
			itemType = definition.ItemType
			break
		}
	}
	var response struct {
		Node struct{ Items []listedArtifact }
	}
	query := `query($id: ID!, $dimension: String!, $absolute: Boolean!) {
  node(id: $id) { ... on Artifacts {
    items: dimensionItems(dimension: $dimension) { uri(absolute: $absolute, typeAssertion: true) description moduleName path coordinates:dimensionKeys(all:true) { dimension key } }
  } }
 }`
	variables := map[string]any{"id": id, "dimension": dimension, "absolute": absolute}
	err = dag.Do(ctx, &dagger.Request{Query: query, Variables: variables}, &dagger.Response{Data: &response})
	if err != nil && strings.Contains(err.Error(), "Unknown argument") && strings.Contains(err.Error(), "all") {
		query = strings.Replace(query, "dimensionKeys(all:true)", "dimensionKeys", 1)
		err = dag.Do(ctx, &dagger.Request{Query: query, Variables: variables}, &dagger.Response{Data: &response})
	}
	if err != nil {
		return nil, err
	}
	for i := range response.Node.Items {
		item := &response.Node.Items[i]
		item.CollectionItem = true
		legacy := slices.ContainsFunc(item.Coordinates, func(k struct{ Dimension, Key string }) bool { return strings.HasPrefix(k.Dimension, "type:") })
		if legacy {
			item.DimensionKeys = item.Coordinates
			item.Coordinates = nil
			continue
		}
		for _, coordinate := range item.Coordinates {
			if coordinate.Dimension == "name" {
				item.Name = coordinate.Key
				continue
			}
			item.DimensionKeys = append(item.DimensionKeys, coordinate)
		}
		if itemType != "" {
			item.DimensionKeys = append(item.DimensionKeys, struct{ Dimension, Key string }{"type:" + itemType, strings.Join(item.Path, "/")})
		}
	}
	return response.Node.Items, nil
}

func registerArtifactFilters(ctx context.Context, dag *dagger.Client, cmd *cobra.Command, dimensions, selected artifact.Dimensions, all *core.Artifacts) error {
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
		if (d.Identifier == artifact.NameDimension || slices.Contains(types, d.ItemType)) && !slices.ContainsFunc(selected, func(s *artifact.Dimension) bool { return s.Identifier == d.Identifier }) {
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
