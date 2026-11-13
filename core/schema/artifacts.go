package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/artifact/flattening"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/hashutil"
)

type artifactsSchema struct{}

func (s *artifactsSchema) Install(srv *dagql.Server) {
	srv.InstallObject(dagql.NewClass[*core.ArtifactPath](srv).View(AfterVersion("v1.0.0-0")))
	dagql.Fields[*core.ArtifactPath]{}.Install(srv)
	dimensionClass := srv.InstallObject(dagql.NewClass[*core.ArtifactDimension](srv).View(AfterVersion("v1.0.0-0"))).(dagql.Class[*core.ArtifactDimension])
	artifactDimensionKinds.Install(srv, AfterVersion("v1.0.0-0"))
	dimensionFields := dagql.Fields[*core.ArtifactDimension]{
		dagql.Func("kind", func(_ context.Context, d *core.ArtifactDimension, _ struct{}) (artifactDimensionKind, error) {
			if d.Kind == "MODULE" {
				return moduleDimension, nil
			}
			if d.Kind == "NAME" {
				return nameDimension, nil
			}
			if d.Kind == "TYPE" {
				return typeDimension, nil
			}
			return collectionDimension, nil
		}).Doc("How this dimension gets its keys."),
		dagql.Func("collectionType", func(_ context.Context, d *core.ArtifactDimension, _ struct{}) (dagql.Nullable[dagql.String], error) {
			if d.Kind != "COLLECTION" {
				return dagql.Null[dagql.String](), nil
			}
			return dagql.NonNull(dagql.String(d.CollectionType)), nil
		}).Doc("The collection type, or null for a static dimension."),
	}
	// These custom accessors only read metadata, just like the reflected
	// dimension fields. Keep their spans as internal trace detail too.
	for _, field := range dimensionFields {
		field.Spec.Trivial = true
	}
	dimensionFields.Install(srv)
	// Cloud can show internal spans at its default verbosity. Use its existing
	// passthrough marker for these pure metadata getters, including reflected
	// fields, so they do not occupy rows even without receiver call payloads.
	for _, spec := range dimensionClass.FieldSpecs("") {
		if !spec.Trivial {
			continue
		}
		field, _ := dimensionClass.Field(spec.Name, "")
		field.Spec = &spec
		dimensionClass.Install(field.PassthroughTelemetry())
	}
	srv.InstallObject(dagql.NewClass[*core.ArtifactDimensionKey](srv).View(AfterVersion("v1.0.0-0")))
	artifactClass := dagql.NewClass[*core.Artifact](srv).View(AfterVersion("v1.0.0-0"))
	srv.InstallObject(artifactClass)
	srv.InstallObject(dagql.NewClass[*core.Artifacts](srv).View(AfterVersion("v1.0.0-0")))
	dagql.Fields[*core.ArtifactDimensionKey]{}.Install(srv)
	dagql.Fields[*core.Artifacts]{
		dagql.NodeFunc("asExpertise", s.asExpertise).Doc("Convert the selection to expertise without running the functions. Each must take one LLM! argument and return LLM!; any other arguments must be optional or auto-filled. Agent arguments receive the agent that owns the conversation. A module load failure fails with its load error."),
		dagql.NodeFunc("asGenerators", s.asGenerators).Doc("Convert the selection to Generators without running them. Fail if any artifact is not a Generator. Module load failures are skipped: generating can repair them."),
		dagql.NodeFunc("asChecks", s.asChecks).DoNotCache("Each selection gets checks that run per their own cache policy.").Doc("Convert the selection to Checks. Fail if any artifact is not a Check. A module load failure is a Check that fails with its load error. Does not apply command filters or run the checks."),
		dagql.NodeFunc("asChangesets", s.asChangesets).Doc("Convert the selection to Changesets. Fail if any artifact is not a Changeset; a module load failure fails with its load error. Does not apply command filters."),
		dagql.NodeFunc("asServices", s.asServices).Doc("Convert the selection to Services. Fail if any artifact is not a Service; a module load failure fails with its load error. Does not apply command filters or start the services."),
		dagql.Func("pathDefinitions", s.pathDefinitions).View(AllVersion).Doc("List selected schema paths, including empty collections. Does not read runtime values. Applies type keys and collection presence; collection key values require items.").Args(dagql.Arg("absolute").Doc("Prefix each address with the workspace's Git address and commit."), dagql.Arg("typeAssertion").Doc("Include the artifact type in each address scheme."), dagql.Arg("dimension").Doc("Project paths to the items of this collection dimension. Preserve parent dimensions and remove descendant dimensions.")),
		dagql.Func("dimensionDefinitions", s.dimensionDefinitions).View(AllVersion).Doc("List dimensions on the selected schema paths, including empty collections. Does not read runtime values."),
		dagql.Func("modules", s.modules).Doc("List the modules represented in this selection without evaluating artifact values."),
		dagql.Func("types", s.types).Doc("List concrete type definitions represented in this selection, sorted by name with no duplicates."),
		dagql.Func("filterDirectives", s.filterDirectives).Doc("Keep artifacts with any listed directive. Does not filter by type or workspace settings.").Args(dagql.Arg("directives"), dagql.Arg("exclude").Doc("Remove the matching artifacts instead.")),
		dagql.Func("filterParentTypes", s.filterParentTypes).Doc("Keep artifacts whose immediate parent has any listed object type. Artifacts without a typed parent do not match.").Args(dagql.Arg("types"), dagql.Arg("exclude").Doc("Remove the matching artifacts instead.")),
		dagql.Func("filterParentDirectives", s.filterParentDirectives).Doc("Keep artifacts whose immediate parent has any listed directive. Artifacts without a parent do not match.").Args(dagql.Arg("directives"), dagql.Arg("exclude").Doc("Remove the matching artifacts instead.")),
		dagql.Func("withoutUri", s.withoutURI).Doc("Remove artifacts selected by a DAG address. Retain the collection view."),
		dagql.Func("filterTypes", s.filterTypes).Doc("Keep artifacts of any listed concrete GraphQL type.").Args(dagql.Arg("types"), dagql.Arg("exclude").Doc("Remove the matching artifacts instead.")),
		dagql.Func("filterPath", s.filterPath).Doc("Match one complete, ordered field sequence exactly."),
		dagql.Func("filterPathPattern", s.filterPathPattern).Doc("Keep paths that match a glob pattern. A literal path matches exactly. Both module-qualified and workspace main paths match."),
		dagql.Func("filterDimensions", s.filterDimensionsLegacy).View(BeforeVersion(artifactDimensionViewVersion)).Doc("Keep artifacts selected through any listed dimension."),
		dagql.Func("filterDimensions", s.filterDimensions).View(AfterVersion(artifactDimensionViewVersion)).Doc("Keep artifacts selected through any listed dimension.").Args(dagql.Arg("exclude").Doc("Remove artifacts with any listed dimension.")),
		dagql.Func("filterDimensionKeys", s.filterDimensionKeys).Doc("Keep artifacts with any listed key in this dimension. Replace collection references with matching subsets. Skip absent keys and leave the source view unchanged."),
		dagql.Func("filterUri", s.filterURI).
			Doc("Apply a DAG address as one filter: the chain of path, type, and dimension-key filters it encodes.",
				"The scheme is optional. The path may be a pattern; an empty path selects all artifacts.",
				"A module that failed to load keeps its load failure when the module could hold what the address selects, as with every filter (see Artifacts).").
			Args(dagql.Arg("uri").Doc("A DAG address: [dag[+<type>]://][<path>][?<dimension>=<key>&...]")),
		dagql.Func("dimensions", s.legacyDimensions).View(BeforeVersion(artifactDimensionViewVersion)).Doc("List dimension identifiers represented in this selection, sorted with no duplicates."),
		dagql.NodeFunc("dimensions", s.selectionDimensions).View(AfterVersion(artifactDimensionViewVersion)).Doc("List dimensions whose cells differ across selected rows. Missing cells differ from present keys.").Args(dagql.Arg("all").Doc("List all workspace schema dimensions without runtime discovery.")),
		dagql.Func("dimensionKeys", s.dimensionKeys).Doc("List keys represented in this selection for the given dimension, sorted with no duplicates."),
		dagql.Func("dimensionItems", s.dimensionItems).View(BeforeVersion(artifactDimensionViewVersion)).Doc("List collection items represented in this selection for the given dimension. Preserve parent keys and remove duplicate item addresses. Does not evaluate item values."),
		dagql.NodeFunc("dimensionItems", s.selectionDimensionItems).View(AfterVersion(artifactDimensionViewVersion)).Doc("List collection items represented in this selection. Retain the source selection’s visible dimensions without evaluating item values."),
		keptFromContent(dagql.Func("__evaluationItems", func(ctx context.Context, parent *core.Artifacts, _ struct{}) (core.ArtifactItems, error) {
			items, err := keptExpansion(ctx, parent.EvaluationItems)
			return core.ArtifactItems{Array: items}, err
		}), artifactsFromContent),
		dagql.Func("items", s.items).Doc("List artifact descriptions with their collection view. Discover collection keys without evaluating leaf values."),
		keptFromContent(dagql.Func("__listing", func(ctx context.Context, parent *core.Artifacts, args artifactListingArgs) (core.JSON, error) {
			return keptExpansion(ctx, func(ctx context.Context) (core.JSON, error) {
				return s.listing(ctx, parent, args)
			})
		}).View(core.AllVersion).
			Doc("Internal listing metadata, serialized without per-item GraphQL object materialization.").
			Args(dagql.Arg("absolute").Doc("Prefix each address with the workspace's Git address and commit."), dagql.Arg("typeAssertion").Doc("Include the artifact type in each address scheme.")), artifactsFromContent),
		dagql.Func("one", s.one).View(BeforeVersion(artifactDimensionViewVersion)).Doc("Require exactly one artifact; fail if there are zero or multiple matches. Several matches are listed, one address per line."),
		dagql.NodeFunc("one", s.selectionOne).View(AfterVersion(artifactDimensionViewVersion)).Doc("Require exactly one artifact from the selected items snapshot; fail if there are zero or multiple matches."),
		dagql.Func("uri", s.uri).Doc("The DAG address that selects this whole selection: filterUri(uri) selects the same set."),
	}.Install(srv)
	dagql.Fields[*core.Artifact]{
		dagql.Func("__generator", func(ctx context.Context, a *core.Artifact, args struct{ Arguments core.JSON }) (*core.Generator, error) {
			return newArtifactGenerator(ctx, a, args.Arguments)
		}),
		dagql.Func("__expertise", func(_ context.Context, a *core.Artifact, _ struct{}) (*core.Expertise, error) {
			return core.NewExpertise(a)
		}),
		dagql.NodeFunc("__remoteCheck", s.remoteCheck),
		dagql.Func("__failedCheck", s.failedCheck),
		dagql.Func("__checkGroup", func(_ context.Context, a *core.Artifact, _ struct{}) (string, error) {
			if a.TypeName != "Check" || !a.IsCollectionItem() {
				return "", nil
			}
			return strings.Join(a.Path, "/"), nil
		}).View(AfterVersion(checkCollectionMetadataVersion)),
		dagql.Func("dimensionKeys", s.legacyArtifactDimensionKeys).View(BeforeVersion(artifactDimensionViewVersion)).Doc("The module name, collection keys, and full path key in the artifact type dimension."),
		dagql.Func("dimensionKeys", s.artifactDimensionKeys).View(AfterVersion(artifactDimensionViewVersion)).Doc("The artifact coordinates in its source selection's visible dimensions. Without a source selection, return all coordinates.").Args(dagql.Arg("all").Doc("Return all artifact coordinates, including constant dimensions.")),
		dagql.Func("loadError", s.loadError).Doc("A module load failure, or an empty string if discovery succeeded."),
		dagql.Func("arguments", s.arguments).Doc("The arguments accepted by the artifact field."),
		dagql.Func("description", s.description).Doc("The description of the field that supplies this artifact."),
		dagql.Func("workspace", s.artifactWorkspace).Doc("The workspace this artifact was discovered in; its address is relative to it. Null for an artifact without a workspace, such as one of an LLM's bound tool objects (see LLM.artifacts)."),
		dagql.Func("uri", s.artifactURI).View(core.AllVersion).
			Doc("The artifact's DAG address, such as dag://engine-dev/playground.").
			Args(
				dagql.Arg("absolute").Doc("Prefix the workspace's Git address and commit: dag://<workspace>@<commit>:<path>. Fails if the artifact has no workspace, or its workspace has no Git address."),
				dagql.Arg("dimensionKeys").Doc("Include the dimension keys as a query. Without them, the address is a path selector."),
				dagql.Arg("typeAssertion").Doc("Include the artifact type in the scheme: dag+container://."),
			),
	}.Install(srv)
	artifactClass.Extend(dagql.FieldSpec{
		Name: "value", Type: nodeInterfaceType{}, Args: dagql.NewInputSpecs(dagql.InputSpec{Name: "arguments", Type: core.JSON{}, Default: core.JSON("{}"), Description: "Field arguments as a JSON object."}),
		Description: "Evaluate the target in the workspace that supplied this artifact.",
		ViewFilter:  AfterVersion("v1.0.0-0"),
		DoNotCache:  "Evaluate the field with its own cache policy.",
	}, s.value)
}

func (*artifactsSchema) modules(_ context.Context, parent *core.Artifacts, _ struct{}) (dagql.ObjectResultArray[*core.Module], error) {
	parent, err := parent.SchemaSelection()
	if err != nil {
		return nil, err
	}
	result := dagql.ObjectResultArray[*core.Module]{}
	seen := map[uint64]bool{}
	for _, item := range parent.Entries {
		if item.Node == nil || item.Node.Module.Self() == nil {
			continue
		}
		module := item.Node.Module
		id, err := module.ID()
		if err != nil {
			return nil, err
		}
		if !seen[id.EngineResultID()] {
			seen[id.EngineResultID()] = true
			result = append(result, module)
		}
	}
	return result, nil
}

func (*artifactsSchema) types(ctx context.Context, parent *core.Artifacts, _ struct{}) (dagql.ObjectResultArray[*core.TypeDef], error) {
	parent, err := parent.SchemaSelection()
	if err != nil {
		return nil, err
	}
	byName := map[string]dagql.ObjectResult[*core.TypeDef]{}
	for _, artifact := range parent.Entries {
		if artifact.Node != nil && artifact.Node.Type.Self() != nil {
			byName[artifact.TypeName] = artifact.Node.Type
		}
	}
	names := parent.Types()
	if len(byName) < len(names) {
		// Module-load failures have no schema node. Their Check type is
		// already served by the core schema; no module discovery is needed.
		srv, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return nil, err
		}
		var definitions dagql.ObjectResultArray[*core.TypeDef]
		if err := srv.Select(ctx, srv.Root(), &definitions, dagql.Selector{Field: "currentTypeDefs"}); err != nil {
			return nil, err
		}
		for _, def := range definitions {
			if name := def.Self().Name; slices.Contains(names, name) {
				if _, ok := byName[name]; !ok {
					byName[name] = def
				}
			}
		}
	}
	result := make(dagql.ObjectResultArray[*core.TypeDef], 0, len(names))
	for _, name := range names {
		def, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("artifact type definition %q is missing", name)
		}
		result = append(result, def)
	}
	return result, nil
}

type artifactTypeFilterArgs struct {
	Types   []string
	Exclude bool `default:"false"`
}

type artifactDirectiveFilterArgs struct {
	Directives []string
	Exclude    bool `default:"false"`
}

func (*artifactsSchema) filterTypes(_ context.Context, parent *core.Artifacts, args artifactTypeFilterArgs) (*core.Artifacts, error) {
	return parent.FilterTypes(args.Types, args.Exclude), nil
}

func (*artifactsSchema) filterParentTypes(_ context.Context, parent *core.Artifacts, args artifactTypeFilterArgs) (*core.Artifacts, error) {
	return parent.FilterParentTypes(args.Types, args.Exclude), nil
}

func (*artifactsSchema) filterParentDirectives(_ context.Context, parent *core.Artifacts, args artifactDirectiveFilterArgs) (*core.Artifacts, error) {
	return parent.FilterParentDirectives(args.Directives, args.Exclude), nil
}

func (*artifactsSchema) filterPath(_ context.Context, parent *core.Artifacts, args struct{ Path []string }) (*core.Artifacts, error) {
	return parent.FilterPath(args.Path), nil
}
func (*artifactsSchema) filterPathPattern(_ context.Context, parent *core.Artifacts, args struct{ Pattern string }) (*core.Artifacts, error) {
	return parent.FilterPattern(args.Pattern)
}

func (*artifactsSchema) filterDimensionsLegacy(_ context.Context, parent *core.Artifacts, args struct{ Dimensions []string }) (*core.Artifacts, error) {
	return parent.FilterDimensions(args.Dimensions), nil
}
func (*artifactsSchema) filterDimensions(_ context.Context, parent *core.Artifacts, args struct {
	Dimensions []string
	Exclude    bool `default:"false"`
}) (*core.Artifacts, error) {
	if args.Exclude {
		return parent.WithoutDimensions(args.Dimensions), nil
	}
	return parent.FilterDimensions(args.Dimensions), nil
}

func (*artifactsSchema) filterDimensionKeys(_ context.Context, parent *core.Artifacts, args struct {
	Dimension string
	Keys      []string
}) (*core.Artifacts, error) {
	return parent.FilterDimensionKeys(args.Dimension, args.Keys), nil
}
func (*artifactsSchema) filterURI(_ context.Context, parent *core.Artifacts, args struct{ URI string }) (*core.Artifacts, error) {
	addr, err := dagaddress.Parse(args.URI)
	if err != nil {
		return nil, err
	}
	if err := checkArtifactAddressWorkspace(parent.Entries, addr, args.URI); err != nil {
		return nil, err
	}
	return parent.FilterURI(addr)
}

// checkArtifactAddressWorkspace reserves the absolute form. Only the current
// workspace, at its own commit, is accepted. Artifacts without a workspace
// (see core.BoundArtifacts) never match an absolute address, so they do not
// constrain it.
func checkArtifactAddressWorkspace(entries []*core.Artifact, addr *dagaddress.Address, uri string) error {
	if !addr.Absolute {
		return nil
	}
	notSupported := fmt.Errorf("address selects another workspace; filters cannot change workspace: %s", uri)
	for _, artifact := range entries {
		if artifact.Workspace.Self() == nil {
			continue
		}
		address, commit, err := artifact.Workspace.Self().GitAddress()
		if err != nil || address != addr.Workspace || commit != addr.Version {
			return notSupported
		}
	}
	return nil
}

func (*artifactsSchema) dimensionDefinitions(ctx context.Context, parent *core.Artifacts, _ struct{}) ([]*core.ArtifactDimension, error) {
	var err error
	parent, err = parent.SchemaSelection()
	if err != nil {
		return nil, err
	}
	definitions := parent.DimensionDefinitions()
	if core.Supports(ctx, artifactDimensionViewVersion) && len(definitions) > 0 {
		legacy := definitions
		module := ""
		all := parent.AllDimensions
		if all == nil {
			all = legacy
		}
		metadata := make(flattening.Dimensions, len(all))
		for i, dimension := range all {
			metadata[i] = (*flattening.Dimension)(dimension)
			if dimension.Identifier == artifact.ModuleDimension {
				module = "workspace"
			}
		}
		named, err := flattening.NameDimensions(flattening.CanonicalDimensions(module, metadata))
		if err != nil {
			return nil, err
		}
		selected := map[string]bool{}
		for _, dimension := range legacy {
			id := dimension.Identifier
			if dimension.CollectionType != "" {
				id = dimension.CollectionType
			}
			selected[id] = true
		}
		definitions = nil
		for _, dimension := range named {
			if selected[dimension.Identifier] || dimension.Identifier == artifact.NameDimension {
				definitions = append(definitions, (*core.ArtifactDimension)(dimension))
			}
		}
		for _, dimension := range legacy {
			if dimension.Kind == "TYPE" {
				definitions = append(definitions, dimension)
			}
		}
	}
	return definitions, nil
}

func (*artifactsSchema) pathDefinitions(ctx context.Context, parent *core.Artifacts, args struct {
	Absolute      bool `default:"false"`
	TypeAssertion bool `default:"false"`
	Dimension     dagql.Optional[dagql.String]
}) ([]*core.ArtifactPath, error) {
	var err error
	parent, err = parent.SchemaSelection()
	if err != nil {
		return nil, err
	}
	if args.Dimension.Valid {
		dimension, err := parent.ResolveDimension(args.Dimension.Value.String())
		if err != nil {
			return nil, err
		}
		items, err := parent.DimensionItems(dimension)
		if err != nil {
			return nil, err
		}
		parent = &core.Artifacts{Entries: items}
	}
	return parent.PathDefinitions(core.ArtifactURIOpts{Absolute: args.Absolute, TypeAssertion: args.TypeAssertion, Prune: core.Supports(ctx, artifactDimensionViewVersion)})
}

// artifactDimensionViewVersion is the first table presentation API view.
const artifactDimensionViewVersion = core.ArtifactTypeColumnsVersion

type artifactDimensionViewArgs struct {
	All bool `default:"false"`
}

// selectedItems uses the public cached items result as the discovery snapshot.
// It retains normal dagql ownership and adds no separate session cache.
func selectedItems(ctx context.Context, parent dagql.ObjectResult[*core.Artifacts]) ([]*core.Artifact, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var items dagql.ObjectResultArray[*core.Artifact]
	if err := srv.Select(ctx, parent, &items, dagql.Selector{Field: "items"}); err != nil {
		return nil, err
	}
	entries := make([]*core.Artifact, 0, len(items))
	for _, item := range items {
		entries = append(entries, item.Self())
	}
	return entries, nil
}

func (*artifactsSchema) selectionDimensions(ctx context.Context, parent dagql.ObjectResult[*core.Artifacts], args artifactDimensionViewArgs) ([]string, error) {
	if args.All {
		return parent.Self().SchemaDimensions(), nil
	}
	entries, err := selectedItems(ctx, parent)
	if err != nil {
		return nil, err
	}
	return (&core.Artifacts{Entries: entries}).Dimensions(), nil
}

func (*artifactsSchema) selectionOne(ctx context.Context, parent dagql.ObjectResult[*core.Artifacts], _ struct{}) (*core.Artifact, error) {
	entries, err := selectedItems(ctx, parent)
	if err != nil {
		return nil, err
	}
	snapshot := *parent.Self()
	snapshot.Entries = entries
	return snapshot.One()
}

func (*artifactsSchema) legacyArtifactDimensionKeys(_ context.Context, parent *core.Artifact, _ struct{}) ([]*core.ArtifactDimensionKey, error) {
	return parent.DimensionKeys, nil
}
func (*artifactsSchema) artifactDimensionKeys(ctx context.Context, parent *core.Artifact, args artifactDimensionViewArgs) ([]*core.ArtifactDimensionKey, error) {
	if !args.All && parent.DimensionDisplaySource().Self() != nil {
		entries, err := selectedItems(ctx, parent.DimensionDisplaySource())
		if err != nil {
			return nil, err
		}
		parent = parent.WithDimensionDisplay((&core.Artifacts{Entries: entries}).Dimensions())
	}
	return parent.DisplayDimensionKeys(args.All), nil
}

func (*artifactsSchema) legacyDimensions(ctx context.Context, parent *core.Artifacts, _ struct{}) ([]string, error) {
	expanded, err := expandArtifacts(ctx, parent)
	if err != nil {
		return nil, err
	}
	return expanded.RepresentedDimensions(), nil
}
func (*artifactsSchema) dimensionKeys(ctx context.Context, parent *core.Artifacts, args struct{ Dimension string }) ([]string, error) {
	return parent.ExpandDimensionKeys(ctx, args.Dimension)
}

func (*artifactsSchema) dimensionItems(ctx context.Context, parent *core.Artifacts, args struct{ Dimension string }) ([]*core.Artifact, error) {
	return parent.ExpandDimensionItems(ctx, args.Dimension)
}
func (*artifactsSchema) selectionDimensionItems(ctx context.Context, parent dagql.ObjectResult[*core.Artifacts], args struct{ Dimension string }) ([]*core.Artifact, error) {
	items, err := parent.Self().ExpandDimensionItems(ctx, args.Dimension)
	if err != nil || len(items) == 0 {
		return items, err
	}
	for i, item := range items {
		items[i] = item.WithDimensionDisplaySource(parent)
	}
	return items, nil
}

func (*artifactsSchema) items(ctx context.Context, parent *core.Artifacts, _ struct{}) ([]*core.Artifact, error) {
	expanded, err := expandArtifacts(ctx, parent)
	if err != nil {
		return nil, err
	}
	parent = expanded
	dimensions := parent.Dimensions()
	items := make([]*core.Artifact, 0, len(parent.Entries))
	for _, artifact := range parent.Entries {
		items = append(items, artifact.WithDimensionDisplay(dimensions))
	}
	if core.Supports(ctx, artifactDimensionViewVersion) {
		catalog := *parent
		catalog.Entries = items
		catalog.PrepareAddressPlans()
	}
	return items, nil
}

type artifactListingArgs struct {
	Schema        bool `default:"false"`
	Absolute      bool `default:"false"`
	TypeAssertion bool `default:"false"`
}

// listing uses the same collection discovery as items. Its result contains
// metadata only: no leaf values, client state, or executable object handles.
func (*artifactsSchema) listing(ctx context.Context, parent *core.Artifacts, args artifactListingArgs) (core.JSON, error) {
	var expanded *core.Artifacts
	var err error
	if args.Schema {
		expanded, err = parent.SchemaSelection()
	} else {
		expanded, err = expandArtifacts(ctx, parent)
	}
	if err != nil {
		return nil, err
	}
	items := expanded.Entries
	if !args.Schema {
		items, err = prepareArtifactAddresses(ctx, items, args.TypeAssertion)
		if err != nil {
			return nil, err
		}
	}
	return marshalArtifactListing(ctx, items, args)
}

func marshalArtifactListing(ctx context.Context, items []*core.Artifact, args artifactListingArgs) (core.JSON, error) {
	type row struct {
		URI           string                       `json:"uri"`
		Description   string                       `json:"description"`
		ModuleName    string                       `json:"moduleName"`
		LoadError     string                       `json:"loadError,omitempty"`
		Path          []string                     `json:"path"`
		DimensionKeys []*core.ArtifactDimensionKey `json:"dimensionKeys"`
		Name          string                       `json:"name"`
		Coordinates   []*core.ArtifactDimensionKey `json:"coordinates,omitempty"`
		Presence      []string                     `json:"presence,omitempty"`
		Projection    *flattening.Field            `json:"projection,omitempty"`
	}
	prune := core.Supports(ctx, artifactDimensionViewVersion)
	rows := make([]row, len(items))
	for i, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		uri, err := item.URI(core.ArtifactURIOpts{Absolute: args.Absolute, TypeAssertion: args.TypeAssertion, DimensionKeys: !args.Schema, Prune: prune, Full: args.Schema && !prune})
		if err != nil {
			return nil, err
		}
		if args.Schema {
			address, err := dagaddress.Parse(uri)
			if err != nil {
				return nil, err
			}
			address.Path = strings.Join(item.Path, "/")
			if prune {
				address.Query = slices.DeleteFunc(address.Query, func(pair dagaddress.Pair) bool {
					return pair.HasKey && pair.Dimension != artifact.ModuleDimension
				})
			} else {
				address.Query = nil
			}
			uri = address.String()
		}
		keys := item.DimensionKeys
		if args.Schema {
			keys = []*core.ArtifactDimensionKey{}
			if item.ModuleName != "" {
				keys = append(keys, &core.ArtifactDimensionKey{Dimension: "module", Key: item.ModuleName})
			}
			if item.TypeName != "" {
				keys = append(keys, &core.ArtifactDimensionKey{Dimension: "type:" + item.TypeName, Key: strings.Join(item.Path, "/")})
			}
		}
		rows[i] = row{URI: uri, Description: item.Description(), ModuleName: item.ModuleName, Path: item.Path, DimensionKeys: keys}
		if item.LoadFailure != nil {
			rows[i].LoadError = item.LoadFailure.Message
		}
		if prune {
			rows[i].Coordinates = item.CanonicalDimensionKeys()
			rows[i].Name = item.StaticDimensionKey("name")
		}
		if args.Schema {
			if prune {
				field := item.ProjectionField()
				rows[i].Projection = &field
			}
			for _, d := range item.DimensionDefinitions() {
				if !artifact.IsStaticDimension(d.Identifier) {
					identifier := d.Identifier
					if prune && d.CollectionType != "" {
						identifier = d.CollectionType
					}
					rows[i].Presence = append(rows[i].Presence, identifier)
				}
			}
		}
	}
	data, err := json.Marshal(rows)
	return core.JSON(data), err
}

func (*artifactsSchema) one(ctx context.Context, parent *core.Artifacts, _ struct{}) (*core.Artifact, error) {
	expanded, err := expandArtifacts(ctx, parent)
	if err != nil {
		return nil, err
	}
	return expanded.One()
}
func (*artifactsSchema) uri(_ context.Context, parent *core.Artifacts, _ struct{}) (string, error) {
	var workspaceID uint64
	for _, entry := range parent.Entries {
		// Module and type keys are static. Only collection keys are resolved.
		if slices.ContainsFunc(entry.DimensionKeys, func(key *core.ArtifactDimensionKey) bool {
			return key.Dimension != artifact.ModuleDimension && !strings.HasPrefix(key.Dimension, "type:")
		}) {
			return "", fmt.Errorf("a selection with resolved collection keys has no single DAG address; use the individual artifact addresses")
		}
		// A relative address has no workspace, so it can mix one workspace's
		// artifacts with artifacts that have none (see core.BoundArtifacts),
		// such as an LLM's scope. It cannot tell two workspaces apart.
		if entry.Workspace.Self() == nil {
			continue
		}
		id, err := entry.Workspace.ID()
		if err != nil {
			return "", err
		}
		if workspaceID != 0 && id.EngineResultID() != workspaceID {
			return "", fmt.Errorf("a selection from multiple workspaces has no single DAG address; use the individual artifact addresses")
		}
		workspaceID = id.EngineResultID()
	}
	if len(parent.Selector.ExcludedURIs) > 0 {
		return "", fmt.Errorf("one DAG address cannot express collection key exclusions")
	}
	bound, err := parent.BindDimensions()
	if err != nil {
		return "", err
	}
	if len(bound.Selector.DimensionAlternatives) > 0 {
		group := bound.Selector.DimensionAlternatives[0]
		if len(group) == 0 {
			return "dag://{}", nil
		}
		return "", fmt.Errorf("one DAG address cannot express alternatives across dimensions %q", group)
	}
	return bound.URI(), nil
}

// expandArtifacts enumerates the selected collections' keys. Each collection
// receiver evaluates in its own artifact's workspace (see
// core.Artifact.WorkspaceContext), so a selection may mix workspaces and
// artifacts without one.
func expandArtifacts(ctx context.Context, parent *core.Artifacts) (*core.Artifacts, error) {
	return parent.Expand(ctx)
}
func (*artifactsSchema) artifactURI(ctx context.Context, parent *core.Artifact, args struct {
	Absolute      bool `default:"false"`
	DimensionKeys bool `default:"true"`
	TypeAssertion bool `default:"false"`
}) (string, error) {
	if args.DimensionKeys && core.Supports(ctx, artifactDimensionViewVersion) {
		items, err := prepareArtifactAddresses(ctx, []*core.Artifact{parent}, args.TypeAssertion)
		if err != nil {
			return "", err
		}
		parent = items[0]
	}
	return parent.URI(core.ArtifactURIOpts{
		Absolute:      args.Absolute,
		DimensionKeys: args.DimensionKeys,
		TypeAssertion: args.TypeAssertion,
		Prune:         core.Supports(ctx, artifactDimensionViewVersion),
	})
}
func (s *artifactsSchema) value(ctx context.Context, parent dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
	result, err := s.evaluateValue(ctx, parent, args)
	if err != nil {
		return nil, err
	}
	if _, ok := dagql.UnwrapAs[*core.Check](result); !ok {
		return result, nil
	}
	artifact, _ := dagql.UnwrapAs[*core.Artifact](parent)
	address, err := artifact.URI(core.ArtifactURIOpts{DimensionKeys: true})
	if err != nil {
		return nil, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var check dagql.ObjectResult[*core.Check]
	// This engine-owned call uses the new signature even for older clients.
	// Record its version so saved calls can keep using that signature later.
	err = srv.Select(ctx, result.(dagql.AnyObjectResult), &check, dagql.Selector{
		Field: "__withAddress",
		View:  call.View(checkCollectionMetadataVersion),
		Args: []dagql.NamedInput{
			{Name: "address", Value: dagql.String(address)},
			{Name: "collectionItem", Value: dagql.Boolean(artifact.IsCollectionItem())},
		},
	})
	return check, err
}

func (*artifactsSchema) evaluateValue(ctx context.Context, parent dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
	artifact, ok := dagql.UnwrapAs[*core.Artifact](parent)
	if !ok {
		return nil, fmt.Errorf("expected Artifact, got %T", parent.Unwrap())
	}
	arguments := args["arguments"].(core.JSON)
	inputs, err := artifactInputs(ctx, artifact, arguments)
	if err != nil {
		return nil, err
	}
	// A bound artifact (no workspace) is rooted at a live local value, which a
	// cloud engine cannot reconstruct: its checks always run locally.
	if artifact.TypeName == "Check" && artifact.LoadFailure == nil && artifact.Workspace.Self() != nil {
		md, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, err
		}
		if md.EnableCloudScaleOut {
			srv, err := core.CurrentDagqlServer(ctx)
			if err != nil {
				return nil, err
			}
			values := make(map[string]any, len(inputs))
			for _, input := range inputs {
				values[input.Name], err = artifactCloudInput(ctx, srv, input.Value)
				if err != nil {
					return nil, fmt.Errorf("cloud argument %s: %w", input.Name, err)
				}
			}
			remoteArguments, err := json.Marshal(values)
			if err != nil {
				return nil, fmt.Errorf("encode cloud arguments: %w", err)
			}
			var check dagql.ObjectResult[*core.Check]
			err = srv.Select(ctx, parent.(dagql.AnyObjectResult), &check, dagql.Selector{Field: "__remoteCheck", Args: []dagql.NamedInput{{Name: "arguments", Value: core.JSON(remoteArguments)}}})
			return check, err
		}
	}
	var result dagql.AnyObjectResult
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	switch artifact.TypeName {
	case "Generator":
		err = srv.Select(ctx, parent.(dagql.AnyObjectResult), &result, dagql.Selector{Field: "__generator", Args: []dagql.NamedInput{{Name: "arguments", Value: arguments}}})
		return result, err
	case "Expertise":
		if len(inputs) != 0 {
			return nil, fmt.Errorf("artifacts of type Expertise receive their LLM through LLM.spawn(expertise:) or LLM.compose; artifact arguments are not supported")
		}
		err = srv.Select(ctx, parent.(dagql.AnyObjectResult), &result, dagql.Selector{Field: "__expertise"})
		return result, err
	case "Check":
		if artifact.Node != nil && artifact.Node.Name == "stale" && artifact.Node.Parent.ObjectType() != nil && artifact.Node.Parent.ObjectType().Name == "Generator" {
			var gen dagql.ObjectResult[*core.Generator]
			// Use a concrete constructor so retained IDs can reconstruct the target.
			err = srv.Select(ctx, parent.(dagql.AnyObjectResult), &gen, dagql.Selector{Field: "__generator", Args: []dagql.NamedInput{{Name: "arguments", Value: arguments}}})
			if err != nil {
				return nil, err
			}
			err = srv.Select(ctx, gen, &result, dagql.Selector{Field: "stale"})
			return result, err
		}
	}
	// A conversation transform is listed without arguments because
	// composition supplies its conversation. Evaluated on its own, say so
	// rather than failing on the missing argument.
	if name := core.ConversationArgument(artifact); name != "" && artifact.LoadFailure == nil &&
		!slices.ContainsFunc(inputs, func(input dagql.NamedInput) bool { return input.Name == name }) {
		uri, err := artifact.URI(core.ArtifactURIOpts{DimensionKeys: true})
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%s transforms a conversation: compose it with LLM.compose or LLM.spawn(expertise:) through Artifacts.asExpertise, or pass the conversation as its %q argument", uri, name)
	}
	if err := evaluateArtifact(ctx, artifact, &result, inputs...); err != nil {
		if artifact.TypeName != "Check" {
			return nil, err
		}
		failure := err.Error()
		srv, err := core.CurrentDagqlServer(ctx)
		if err != nil {
			return nil, err
		}
		if err := srv.Select(ctx, parent.(dagql.AnyObjectResult), &result, dagql.Selector{Field: "__failedCheck", Args: []dagql.NamedInput{{Name: "message", Value: dagql.String(failure)}}}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Construct checks through concrete Check fields. An Artifact.value call has
// the interface type Node, which cannot supply the identity of a new Check.
func (*artifactsSchema) remoteCheck(ctx context.Context, artifact dagql.ObjectResult[*core.Artifact], args struct{ Arguments core.JSON }) (*core.Check, error) {
	address, err := artifact.Self().URI(core.ArtifactURIOpts{DimensionKeys: true})
	if err != nil {
		return nil, err
	}
	return &core.Check{RemoteArtifact: artifact, RemoteArguments: args.Arguments, Address: address}, nil
}

func (*artifactsSchema) failedCheck(ctx context.Context, _ *core.Artifact, args struct{ Message string }) (*core.Check, error) {
	return &core.Check{Failure: args.Message}, nil
}

// Retain object arguments while the generator remains unevaluated.
func newArtifactGenerator(ctx context.Context, a *core.Artifact, arguments core.JSON) (*core.Generator, error) {
	g, err := core.NewGenerator(a, arguments)
	if err != nil {
		return nil, err
	}
	inputs, err := artifactInputs(ctx, g.Artifact, arguments)
	if err != nil {
		return nil, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var retain func(dagql.Input) error
	retain = func(input dagql.Input) error {
		switch input := input.(type) {
		case dagql.IDable:
			id, err := input.ID()
			if err != nil {
				return err
			}
			value, err := srv.Load(ctx, id)
			if err != nil {
				return err
			}
			g.Inputs = append(g.Inputs, value)
		case dagql.DynamicOptional:
			if input.Valid {
				return retain(input.Value)
			}
		case dagql.DynamicArrayInput:
			for _, value := range input.Values {
				if err := retain(value); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, input := range inputs {
		if err := retain(input.Value); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// Object inputs need recipes on another engine. Scalar strings remain opaque.
func artifactCloudInput(ctx context.Context, srv *dagql.Server, input dagql.Input) (any, error) {
	switch input := input.(type) {
	case dagql.IDable:
		id, err := input.ID()
		if err != nil {
			return nil, err
		}
		if id.IsHandle() {
			value, err := srv.Load(ctx, id)
			if err != nil {
				return nil, err
			}
			id, err = value.RecipeID(ctx)
			if err != nil {
				return nil, err
			}
		}
		return id.Encode()
	case dagql.DynamicOptional:
		if !input.Valid {
			return nil, nil
		}
		return artifactCloudInput(ctx, srv, input.Value)
	case dagql.DynamicArrayInput:
		values := make([]any, len(input.Values))
		for i, value := range input.Values {
			var err error
			values[i], err = artifactCloudInput(ctx, srv, value)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", i, err)
			}
		}
		return values, nil
	default:
		return input, nil
	}
}

// evaluateArtifact selects the artifact's value in its own workspace, or in
// the caller's context for an artifact without one.
func evaluateArtifact(ctx context.Context, artifact *core.Artifact, dest any, inputs ...dagql.NamedInput) error {
	ctx, err := artifact.WorkspaceContext(ctx)
	if err != nil {
		return err
	}
	var value dagql.AnyObjectResult
	err = artifact.Evaluate(ctx, &value, inputs...)
	if artifact.LoadFailure != nil {
		err = fmt.Errorf("%s", artifact.LoadFailure.Message)
	}
	if err != nil {
		return err
	}
	srv, srvErr := core.CurrentDagqlServer(ctx)
	if srvErr != nil {
		return srvErr
	}
	if llm, ok := value.Unwrap().(*core.LLM); ok {
		llm.WarnToolNameCollisions(ctx)
	}
	return srv.Select(ctx, value, dest)
}

func (*artifactsSchema) arguments(_ context.Context, artifact *core.Artifact, _ struct{}) (dagql.ObjectResultArray[*core.FunctionArg], error) {
	if artifact.Node != nil && artifact.Node.Parent != nil {
		if obj := artifact.Node.Parent.ObjectType(); obj != nil {
			if fn, ok := obj.FunctionByName(artifact.Node.Name); ok {
				return fn.Args, nil
			}
		}
	}
	return dagql.ObjectResultArray[*core.FunctionArg]{}, nil
}

func artifactInputs(ctx context.Context, artifact *core.Artifact, raw core.JSON) ([]dagql.NamedInput, error) {
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("artifact arguments: %w", err)
	}
	if len(values) == 0 {
		return nil, nil
	}
	args, _ := (&artifactsSchema{}).arguments(ctx, artifact, struct{}{})
	inputs := make([]dagql.NamedInput, 0, len(values))
	for _, arg := range args {
		value, ok := values[arg.Self().Name]
		if !ok {
			continue
		}
		input, err := arg.Self().TypeDef.Self().ToInput().Decoder().DecodeInput(value)
		if err != nil {
			return nil, fmt.Errorf("argument %s: %w", arg.Self().Name, err)
		}
		inputs = append(inputs, dagql.NamedInput{Name: arg.Self().Name, Value: input})
		delete(values, arg.Self().Name)
	}
	if len(values) != 0 {
		return nil, fmt.Errorf("unknown artifact arguments: %v", values)
	}
	return inputs, nil
}

func (*artifactsSchema) withoutURI(_ context.Context, parent *core.Artifacts, args struct{ URI string }) (*core.Artifacts, error) {
	address, err := dagaddress.Parse(args.URI)
	if err != nil {
		return nil, err
	}
	if err := checkArtifactAddressWorkspace(parent.Entries, address, args.URI); err != nil {
		return nil, err
	}
	return parent.WithoutURI(address)
}

type workspaceArtifactsArgs struct {
	Include dagql.Optional[dagql.ArrayInput[dagql.String]]
}

func (s *workspaceSchema) artifacts(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceArtifactsArgs) (*core.Artifacts, error) {
	return s.collectArtifacts(ctx, parent, workspaceIncludePatterns(args.Include))
}

// artifactsCacheKey keys discovery by what it reads besides the workspace.
// Discovery through a workspace identified by its content goes through the
// calling client's loaded modules (see core.workspaceClientMetadata), so it
// is keyed by those, and shared by every client and session that loads them
// the same way. Any other workspace is keyed per call anyway (see
// currentWorkspace), so its discovery stays per client and is not kept.
func (s *workspaceSchema) artifactsCacheKey(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args workspaceArtifactsArgs, req *dagql.CallRequest) error {
	if !parent.Self().HasContentIdentity() {
		client, err := dagql.PerClientInput.Resolver(ctx, nil)
		if err != nil {
			return err
		}
		return req.SetImplicitInput(ctx, dagql.PerClientInput.Name, client)
	}
	ctx, err := s.withWorkspaceClientContext(ctx, parent.Self())
	if err != nil {
		return err
	}
	inputs, err := s.artifactDiscoveryInputs(ctx, parent, artifactModuleIncludes(workspaceIncludePatterns(args.Include)))
	if err != nil {
		return err
	}
	key, err := inputs.digest(ctx)
	if err != nil {
		return err
	}
	req.IsPersistable = true
	return req.SetImplicitInput(ctx, artifactDiscoveryInput, dagql.NewString(key))
}

const artifactDiscoveryInput = "artifactDiscovery"

// artifactDiscoveryInputs is what discovery reads besides the workspace's
// identity: the effective configuration, which the calling client's selected
// environment and a remote include's current content can change, and from
// the calling client the modules it lists after overlay resolution, which of
// them are workspace mains, and the modules that failed to load.
type artifactDiscoveryInputs struct {
	cfg      *workspace.Config
	mods     []dagql.ObjectResult[*core.Module]
	failures []core.ModuleLoadFailure
	mains    map[string]bool
}

// artifactDiscoveryInputs reads them with ctx already in the workspace's
// client context (see withWorkspaceClientContext).
func (s *workspaceSchema) artifactDiscoveryInputs(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], include []string) (*artifactDiscoveryInputs, error) {
	cfg, err := workspaceEffectiveConfig(ctx, parent.Self())
	if err != nil {
		return nil, err
	}
	mods, failures, err := s.workspaceTargetModules(ctx, parent, include, core.ModuleLoadRepairing)
	if err != nil {
		return nil, err
	}
	if include != nil && core.Supports(ctx, artifactDimensionViewVersion) {
		// Column names need every schema. Only selected modules belong in
		// the command's skipped-module report.
		mods, failures, err = s.workspaceTargetModules(ctx, parent, nil, core.ModuleLoadMetadata)
		if err != nil {
			return nil, err
		}
	}
	mains, err := workspaceMainNames(ctx, parent.Self())
	if err != nil {
		return nil, err
	}
	return &artifactDiscoveryInputs{cfg: cfg, mods: mods, failures: failures, mains: mains}, nil
}

// digest identifies the inputs. A module counts by its implementation,
// which is the same wherever the same module is loaded, as a workspace
// records its client's modules (see Server.CurrentClientModules).
func (inputs *artifactDiscoveryInputs) digest(ctx context.Context) (string, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return "", err
	}
	cfg, err := json.Marshal(inputs.cfg)
	if err != nil {
		return "", err
	}
	parts := []string{artifactDiscoveryInput + "/v6", "config", string(cfg)}
	mods := make([]string, 0, len(inputs.mods))
	for _, mod := range inputs.mods {
		var scoped dagql.ObjectResult[*core.Module]
		if err := dag.Select(ctx, mod, &scoped, dagql.Selector{Field: "_implementationScoped"}); err != nil {
			return "", fmt.Errorf("module %s: %w", mod.Self().Name(), err)
		}
		dgst, err := scoped.ContentPreferredDigest(ctx)
		if err != nil {
			return "", fmt.Errorf("module %s: %w", mod.Self().Name(), err)
		}
		mods = append(mods, mod.Self().Name()+"="+dgst.String())
	}
	slices.Sort(mods)
	parts = append(parts, "modules")
	parts = append(parts, mods...)
	mains := make([]string, 0, len(inputs.mains))
	for name, main := range inputs.mains {
		mains = append(mains, fmt.Sprintf("%s=%t", name, main))
	}
	slices.Sort(mains)
	parts = append(parts, "mains")
	parts = append(parts, mains...)
	failures, err := json.Marshal(inputs.failures)
	if err != nil {
		return "", err
	}
	parts = append(parts, "failures", string(failures))
	return hashutil.HashStrings(parts...).String(), nil
}

// artifactModuleIncludes turns include paths into the module tree patterns
// that narrow module loading.
func artifactModuleIncludes(include []string) []string {
	if include == nil {
		return nil
	}
	patterns := make([]string, len(include))
	for i, pattern := range include {
		patterns[i] = strings.ReplaceAll(pattern, "/", ":")
	}
	return patterns
}

// artifactSchemaIncludes keeps modern column names independent of source
// selection. Loading schema metadata does not evaluate collection items.
func artifactSchemaIncludes(ctx context.Context, include []string) []string {
	if core.Supports(ctx, artifactDimensionViewVersion) {
		return nil
	}
	return include
}

// collectArtifacts discovers the workspace's static artifacts. Include
// patterns select each path and its children. Modern views load all module
// schemas first so column names remain stable across selections.
func (s *workspaceSchema) collectArtifacts(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], include []string) (*core.Artifacts, error) {
	result := &core.Artifacts{Entries: []*core.Artifact{}}
	if include != nil {
		result.Selector.Paths = make([]string, 0, len(include))
		for _, pattern := range include {
			result.Selector.Paths = append(result.Selector.Paths, core.IncludePatterns(pattern)...)
		}
	}
	include = artifactModuleIncludes(include)
	ctx, err := s.withWorkspaceClientContext(ctx, parent.Self())
	if err != nil {
		return nil, err
	}
	inputs, err := s.artifactDiscoveryInputs(ctx, parent, include)
	if err != nil {
		return nil, err
	}
	cfg, mods, failures, mains := inputs.cfg, inputs.mods, inputs.failures, inputs.mains
	sdkProviders := map[string]bool{}
	for _, sdk := range cfg.SDKs {
		sdkProviders[sdk.Module] = true
	}
	var nodes []*core.ModTreeNode
	for _, mod := range mods {
		root, targets, err := core.ModuleArtifactNodes(ctx, mod)
		if err != nil {
			return nil, err
		}
		reparentWorkspaceTreeRoot(root, mod.Self().Name(), mains[mod.Self().Name()])
		for _, node := range targets {
			if node == root {
				continue
			}
			// Installed SDKs supply their engine-managed generator below.
			if sdkProviders[mod.Self().Name()] && artifactGeneratorNode(node) != nil {
				continue
			}
			nodes = append(nodes, node)
		}
	}
	if parent.Self().ConfigFile != "" && len(cfg.SDKs) > 0 {
		sdkNodes, err := s.sdkArtifactNodes(ctx, parent, cfg, mains)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, sdkNodes...)
	}
	// Names resolve against every loaded node, including those the include
	// patterns skip.
	var loaded []*core.Artifact
	for _, node := range nodes {
		path := node.CommandCLIPath()
		if len(path) == 0 {
			path = node.CLIPath()
		}
		entry := &core.Artifact{
			ModuleName: node.Path()[0], Path: path, DimensionKeys: []*core.ArtifactDimensionKey{},
			Directives: node.Directives, TypeName: node.ObjectType().Name, Node: node, Workspace: parent,
		}
		loaded = append(loaded, entry)
		match, err := matchWorkspaceInclude(node, include)
		if err != nil {
			return nil, err
		}
		if match {
			result.Entries = append(result.Entries, entry)
		}
	}
	for _, entry := range loadFailureArtifacts(parent, cfg, mains, mods, failures) {
		loaded = append(loaded, entry)
		match, err := matchLoadFailureInclude(entry, include)
		if err != nil {
			return nil, err
		}
		if match {
			result.Entries = append(result.Entries, entry)
		}
	}
	if core.Supports(ctx, artifactDimensionViewVersion) {
		fields := make([]flattening.Field, len(loaded))
		for i, entry := range loaded {
			fields[i] = entry.ProjectionField()
		}
		if _, err := flattening.NewSchema(fields); err != nil {
			return nil, fmt.Errorf("flatten workspace schema: %w", err)
		}
	}
	result.AllDimensions = (&core.Artifacts{Entries: loaded}).DimensionDefinitions()
	result.AllPaths = (&core.Artifacts{Entries: loaded}).PathIndex()
	result.CompleteSchema = artifactSchemaIncludes(ctx, include) == nil && len(failures) == 0
	result.SetCollectionRoots(loaded)
	if err := result.NameEntries(); err != nil {
		return nil, err
	}
	skipped, err := workspaceSkippedPaths(ctx, result, mods, cfg)
	if err != nil {
		return nil, err
	}
	result.Entries = slices.DeleteFunc(result.Entries, func(entry *core.Artifact) bool {
		return entry.LoadFailure == nil && skipped[strings.Join(entry.Path, "/")]
	})
	slices.SortFunc(result.Entries, func(a, b *core.Artifact) int { return slices.Compare(a.Path, b.Path) })
	if err := validateArtifactPaths(result.Entries); err != nil {
		return nil, err
	}
	return result, nil
}

// matchLoadFailureInclude keeps an unavailable module only when it could
// contain an included path. Core filtering retains main-module shorthand and
// excludes known sibling module names without requiring the missing schema.
func matchLoadFailureInclude(entry *core.Artifact, include []string) (bool, error) {
	if len(include) == 0 {
		return true, nil
	}
	selection := &core.Artifacts{Entries: []*core.Artifact{entry}}
	for _, include := range include {
		for _, pattern := range core.IncludePatterns(include) {
			selected, err := selection.FilterPattern(pattern)
			if err != nil {
				return false, err
			}
			if len(selected.Entries) > 0 {
				return true, nil
			}
		}
	}
	return false, nil
}

// loadFailureArtifacts stands each module that failed to load up as a
// <module>/load check, named like the module's artifacts would be.
func loadFailureArtifacts(
	parent dagql.ObjectResult[*core.Workspace],
	cfg *workspace.Config,
	mains map[string]bool,
	mods []dagql.ObjectResult[*core.Module],
	failures []core.ModuleLoadFailure,
) []*core.Artifact {
	// A main's shorthand paths cannot start with another module's
	// address name. Name every module the workspace has, not only those the
	// include patterns loaded.
	var moduleNames []string
	addModule := func(name string) {
		if name := core.ArtifactTypeName(name); !slices.Contains(moduleNames, name) {
			moduleNames = append(moduleNames, name)
		}
	}
	for name := range cfg.Modules {
		addModule(name)
	}
	for name := range mains {
		addModule(name)
	}
	for _, mod := range mods {
		addModule(mod.Self().Name())
	}
	for _, failure := range failures {
		addModule(failure.Name)
	}
	slices.Sort(moduleNames)
	var entries []*core.Artifact
	seen := map[string]bool{}
	for _, failure := range failures {
		if seen[failure.Name] {
			continue
		}
		seen[failure.Name] = true
		name := core.ArtifactTypeName(failure.Name)
		// The loader reports whether the failed module is the main;
		// the policy that names healthy mains covers the rest.
		if failure.Entrypoint || mains[failure.Name] {
			failure.Entrypoint = true
			failure.Siblings = slices.DeleteFunc(slices.Clone(moduleNames), func(sibling string) bool { return sibling == name })
		}
		entries = append(entries, &core.Artifact{
			ModuleName: failure.Name, Path: []string{name, "load"}, DimensionKeys: []*core.ArtifactDimensionKey{},
			Directives: []string{"check"}, TypeName: "Check", LoadFailure: &failure, Workspace: parent,
		})
	}
	return entries
}

func (s *workspaceSchema) sdkArtifactNodes(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], cfg *workspace.Config, mains map[string]bool) ([]*core.ModTreeNode, error) {
	var nodes []*core.ModTreeNode
	sdks, err := s.sdks(ctx, parent, struct{}{})
	if err != nil {
		return nil, err
	}
	for _, sdk := range sdks {
		root, err := core.NewArtifactTree(ctx, sdk)
		if err != nil {
			return nil, err
		}
		provider := cfg.SDKs[sdk.Self().Name].Module
		reparentWorkspaceTreeRoot(root, provider, mains[provider])
		targets, err := core.ArtifactNodes(ctx, root)
		if err != nil {
			return nil, err
		}
		sdkName := sdk.Self().Name
		if displayName := map[string]string{
			"go": "Go", "python": "Python", "typescript": "TypeScript",
			"php": "PHP", "dang": "Dang", "java": "Java", "elixir": "Elixir",
		}[sdkName]; displayName != "" {
			sdkName = displayName
		}
		for _, target := range targets {
			if target.Parent == root && target.Name == "generate" {
				target.Description = fmt.Sprintf("re-generate modules and clients managed by the %s SDK", sdkName)
			}
			if target != root {
				nodes = append(nodes, target)
			}
		}
	}
	return nodes, nil
}

// Entries must be sorted by path before checking for duplicate paths.
func validateArtifactPaths(entries []*core.Artifact) error {
	for i := 1; i < len(entries); i++ {
		if slices.Equal(entries[i-1].Path, entries[i].Path) && len(entries[i-1].DimensionDefinitions()) == len(entries[i].DimensionDefinitions()) {
			return fmt.Errorf("ambiguous artifact path %q", strings.Join(entries[i].Path, "/"))
		}
	}
	return nil
}

func artifactGeneratorNode(node *core.ModTreeNode) *core.ModTreeNode {
	for ; node != nil; node = node.Parent {
		if slices.Contains(node.Directives, "generate") {
			return node
		}
	}
	return nil
}

func (*artifactsSchema) filterDirectives(_ context.Context, parent *core.Artifacts, args artifactDirectiveFilterArgs) (*core.Artifacts, error) {
	return parent.FilterDirectives(args.Directives, args.Exclude), nil
}

func (*artifactsSchema) description(_ context.Context, parent *core.Artifact, _ struct{}) (string, error) {
	return parent.Description(), nil
}

func (*artifactsSchema) artifactWorkspace(_ context.Context, parent *core.Artifact, _ struct{}) (dagql.Nullable[dagql.ObjectResult[*core.Workspace]], error) {
	if parent.Workspace.Self() == nil {
		return dagql.Null[dagql.ObjectResult[*core.Workspace]](), nil
	}
	return dagql.NonNull(parent.Workspace), nil
}

func (*artifactsSchema) loadError(_ context.Context, artifact *core.Artifact, _ struct{}) (string, error) {
	if artifact.LoadFailure != nil {
		return artifact.LoadFailure.Message, nil
	}
	return "", nil
}

func (*artifactsSchema) asExpertise(ctx context.Context, parent dagql.ObjectResult[*core.Artifacts], _ struct{}) (dagql.ObjectResultArray[*core.Expertise], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var items dagql.ObjectResultArray[*core.Artifact]
	if err := srv.Select(ctx, parent, &items, dagql.Selector{Field: "items"}); err != nil {
		return nil, err
	}
	// Validate every signature before creating any entry. Artifact types still
	// reflect return types; an explicit selection may include plain LLM transforms.
	for _, item := range items {
		// A broken module under the selection fails with its load error, not
		// as an artifact that is not a source of expertise.
		if failure := item.Self().LoadFailure; failure != nil {
			return nil, fmt.Errorf("%s", failure.Message)
		}
		if _, err := core.NewExpertise(item.Self()); err != nil {
			return nil, err
		}
	}
	entries := make(dagql.ObjectResultArray[*core.Expertise], len(items))
	for i, item := range items {
		if err := srv.Select(ctx, item, &entries[i], dagql.Selector{Field: "__expertise"}); err != nil {
			return nil, err
		}
	}
	return entries, nil
}
func (*artifactsSchema) asGenerators(ctx context.Context, parent dagql.ObjectResult[*core.Artifacts], _ struct{}) (dagql.ObjectResultArray[*core.Generator], error) {
	return artifactValuesAs[*core.Generator](ctx, parent)
}

// Generation can repair a module that fails to load, so a load failure does
// not stop the other generators. Other conversions report it.
func skipsLoadFailures(typeName string) bool {
	return typeName == "Generator"
}

func (*artifactsSchema) asChecks(ctx context.Context, parent dagql.ObjectResult[*core.Artifacts], _ struct{}) (dagql.ObjectResultArray[*core.Check], error) {
	return artifactValuesAs[*core.Check](ctx, parent)
}
func (*artifactsSchema) asChangesets(ctx context.Context, parent dagql.ObjectResult[*core.Artifacts], _ struct{}) (dagql.ObjectResultArray[*core.Changeset], error) {
	return artifactValuesAs[*core.Changeset](ctx, parent)
}
func (*artifactsSchema) asServices(ctx context.Context, parent dagql.ObjectResult[*core.Artifacts], _ struct{}) (dagql.ObjectResultArray[*core.Service], error) {
	return artifactValuesAs[*core.Service](ctx, parent)
}

func artifactValuesAs[T dagql.Typed](ctx context.Context, parent dagql.ObjectResult[*core.Artifacts]) (dagql.ObjectResultArray[T], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var value T
	typeName := value.Type().NamedType
	field := "items"
	if typeName == "Check" || typeName == "Generator" {
		field = "__evaluationItems"
	}
	var items dagql.ObjectResultArray[*core.Artifact]
	if err := srv.Select(ctx, parent, &items, dagql.Selector{Field: field}); err != nil {
		return nil, err
	}
	if skipsLoadFailures(typeName) {
		items = slices.DeleteFunc(slices.Clone(items), func(item dagql.ObjectResult[*core.Artifact]) bool {
			return item.Self().LoadFailure != nil
		})
	}
	// Validate the whole selection before evaluating any artifact.
	for _, item := range items {
		if err := item.Self().AssertType([]string{core.ArtifactTypeName(typeName)}); err != nil {
			return nil, err
		}
	}
	values := make(dagql.ObjectResultArray[T], len(items))
	for i, item := range items {
		if err := srv.Select(ctx, item, &values[i], dagql.Selector{Field: "value"}); err != nil {
			return nil, err
		}
	}
	return values, nil
}
