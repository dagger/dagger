package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/parallel"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type artifactsSchema struct{}

func (s *artifactsSchema) Install(srv *dagql.Server) {
	srv.InstallObject(dagql.NewClass[*core.ArtifactPath](srv).View(AfterVersion("v1.0.0-0")))
	dagql.Fields[*core.ArtifactPath]{}.Install(srv)
	srv.InstallObject(dagql.NewClass[*core.ArtifactDimension](srv).View(AfterVersion("v1.0.0-0")))
	artifactDimensionKinds.Install(srv, AfterVersion("v1.0.0-0"))
	dagql.Fields[*core.ArtifactDimension]{
		dagql.Func("kind", func(_ context.Context, d *core.ArtifactDimension, _ struct{}) (artifactDimensionKind, error) {
			if d.Kind == "MODULE" {
				return moduleDimension, nil
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
	}.Install(srv)
	srv.InstallObject(dagql.NewClass[*core.ArtifactDimensionKey](srv).View(AfterVersion("v1.0.0-0")))
	artifactClass := dagql.NewClass[*core.Artifact](srv).View(AfterVersion("v1.0.0-0"))
	srv.InstallObject(artifactClass)
	srv.InstallObject(dagql.NewClass[*core.Artifacts](srv).View(AfterVersion("v1.0.0-0")))
	resultClass := dagql.NewClass[*core.ArtifactResult](srv).View(AfterVersion("v1.0.0-0"))
	srv.InstallObject(resultClass)
	dagql.Fields[*core.ArtifactResult]{}.Install(srv)
	resultClass.Extend(dagql.FieldSpec{Name: "value", Type: dagql.Null[nodeInterfaceType](), Args: dagql.NewInputSpecs(),
		Description: "The evaluated value, or null when evaluation failed."}, s.resultValue)
	dagql.Fields[*core.ArtifactDimensionKey]{}.Install(srv)
	dagql.Fields[*core.Artifacts]{
		dagql.NodeFunc("asExpertise", s.asExpertise).Doc("Convert the selection to expertise without running the functions. Fail if any artifact is not a source of expertise."),
		dagql.NodeFunc("asGenerators", s.asGenerators).Doc("Convert the selection to Generators without running them. Fail if any artifact is not a Generator."),
		dagql.NodeFunc("asChecks", s.asChecks).Doc("Convert the selection to Checks. Fail if any artifact is not a Check. Does not apply command filters or run the checks."),
		dagql.NodeFunc("asChangesets", s.asChangesets).Doc("Convert the selection to Changesets. Fail if any artifact is not a Changeset. Does not apply command filters."),
		dagql.NodeFunc("asServices", s.asServices).Doc("Convert the selection to Services. Fail if any artifact is not a Service. Does not apply command filters or start the services."),
		dagql.Func("pathDefinitions", s.pathDefinitions).Doc("List selected schema paths, including empty collections. Does not read runtime values. Applies type keys and collection presence; collection key values require items.").Args(dagql.Arg("absolute").Doc("Prefix each address with the workspace's Git address and commit."), dagql.Arg("typeAssertion").Doc("Include the artifact type in each address scheme."), dagql.Arg("dimension").Doc("Project paths to the items of this collection dimension. Preserve parent dimensions and remove descendant dimensions.")),
		dagql.Func("dimensionDefinitions", s.dimensionDefinitions).Doc("List dimensions on the selected schema paths, including empty collections. Does not read runtime values."),
		// Each invocation gets a new cache key. Retain its results so SDK clients can load their IDs.
		dagql.NodeFunc("values", s.values).WithInput(dagql.PerCallInput).Doc("Evaluate the selection in parallel, retaining each result and error.").Args(dagql.Arg("failFast").Doc("Cancel remaining work after the first failure."), dagql.Arg("arguments").Doc("Field arguments applied to each artifact, as a JSON object."), dagql.Arg("maxConcurrency").Doc("Evaluate at most this many artifacts at once on this engine; the rest wait. Checks that scale out to cloud engines are not counted. 0 means no limit.")),
		dagql.Func("modules", s.modules).Doc("List the modules represented in this selection without evaluating artifact values."),
		dagql.Func("types", s.types).Doc("List concrete type definitions represented in this selection, sorted by name with no duplicates."),
		dagql.Func("filterDirectives", s.filterDirectives).Doc("Keep artifacts with any listed directive. Does not filter by type or workspace settings.").Args(dagql.Arg("directives"), dagql.Arg("exclude").Doc("Remove the matching artifacts instead.")),
		dagql.Func("filterParentTypes", s.filterParentTypes).Doc("Keep artifacts whose immediate parent has any listed object type. Artifacts without a typed parent do not match.").Args(dagql.Arg("types"), dagql.Arg("exclude").Doc("Remove the matching artifacts instead.")),
		dagql.Func("filterParentDirectives", s.filterParentDirectives).Doc("Keep artifacts whose immediate parent has any listed directive. Artifacts without a parent do not match.").Args(dagql.Arg("directives"), dagql.Arg("exclude").Doc("Remove the matching artifacts instead.")),
		dagql.Func("withArtifacts", s.withArtifacts).Doc("Combine two selections, keeping each workspace address once. Different addresses remain distinct even if they return the same object."),
		dagql.Func("withoutUri", s.withoutURI).Doc("Remove artifacts selected by a DAG address."),
		dagql.Func("filterTypes", s.filterTypes).Doc("Keep artifacts of any listed concrete GraphQL type.").Args(dagql.Arg("types"), dagql.Arg("exclude").Doc("Remove the matching artifacts instead.")),
		dagql.Func("filterPath", s.filterPath).Doc("Match one complete, ordered field sequence exactly."),
		dagql.Func("filterPathPattern", s.filterPathPattern).Doc("Keep paths that match a glob pattern. A literal path matches exactly. Both module-qualified and entrypoint paths match."),
		dagql.Func("filterDimensions", s.filterDimensions).Doc("Keep artifacts selected through any listed dimension."),
		dagql.Func("filterDimensionKeys", s.filterDimensionKeys).Doc("Keep artifacts with any listed key in this dimension."),
		dagql.Func("filterUri", s.filterURI).
			Doc("Apply a DAG address as one filter: the chain of path, type, and dimension-key filters it encodes.",
				"The scheme is optional. The path may be a pattern; an empty path selects all artifacts.").
			Args(dagql.Arg("uri").Doc("A DAG address: [dag[+<type>]://][<path>][?<dimension>=<key>&...]")),
		dagql.Func("dimensions", s.dimensions).Doc("List dimension identifiers represented in this selection, sorted with no duplicates."),
		dagql.Func("dimensionKeys", s.dimensionKeys).Doc("List keys represented in this selection for the given dimension, sorted with no duplicates."),
		dagql.Func("dimensionItems", s.dimensionItems).Doc("List collection items represented in this selection for the given dimension. Preserve parent keys and remove duplicate item addresses. Does not evaluate item values."),
		dagql.Func("__evaluationItems", s.evaluationItems),
		dagql.Func("items", s.items).Doc("Enumerate complete artifacts without evaluating their values."),
		dagql.Func("one", s.one).Doc("Require exactly one artifact; fail if there are zero or multiple matches. Several matches are listed, one address per line."),
		dagql.Func("uri", s.uri).Doc("The DAG address that selects this whole selection: filterUri(uri) selects the same set."),
	}.Install(srv)
	dagql.Fields[*core.Artifact]{
		dagql.Func("__generator", func(ctx context.Context, a *core.Artifact, args struct{ Arguments core.JSON }) (*core.Generator, error) {
			return newArtifactGenerator(ctx, a, args.Arguments)
		}),
		dagql.Func("__expertise", func(_ context.Context, a *core.Artifact, _ struct{}) (*core.Expertise, error) {
			return core.NewExpertise(a)
		}),
		dagql.Func("__remoteCheck", s.remoteCheck),
		dagql.Func("__failedCheck", s.failedCheck),
		dagql.Func("loadError", s.loadError).Doc("A module load failure, or an empty string if discovery succeeded."),
		dagql.Func("arguments", s.arguments).Doc("The arguments accepted by the artifact field."),
		dagql.Func("description", s.description).Doc("The description of the field that supplies this artifact."),
		dagql.Func("uri", s.artifactURI).
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

func (*artifactsSchema) withArtifacts(ctx context.Context, parent *core.Artifacts, args struct{ Artifacts dagql.ID[*core.Artifacts] }) (*core.Artifacts, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	other, err := args.Artifacts.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	left, err := expandArtifacts(ctx, parent)
	if err != nil {
		return nil, err
	}
	right, err := expandArtifacts(ctx, other.Self())
	if err != nil {
		return nil, err
	}
	return left.WithArtifacts(right)
}
func (*artifactsSchema) filterPath(_ context.Context, parent *core.Artifacts, args struct{ Path []string }) (*core.Artifacts, error) {
	return parent.FilterPath(args.Path), nil
}
func (*artifactsSchema) filterPathPattern(_ context.Context, parent *core.Artifacts, args struct{ Pattern string }) (*core.Artifacts, error) {
	return parent.FilterPattern(args.Pattern)
}

func (*artifactsSchema) filterDimensions(_ context.Context, parent *core.Artifacts, args struct{ Dimensions []string }) (*core.Artifacts, error) {
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

func (*artifactsSchema) dimensionDefinitions(_ context.Context, parent *core.Artifacts, _ struct{}) ([]*core.ArtifactDimension, error) {
	var err error
	parent, err = parent.SchemaSelection()
	if err != nil {
		return nil, err
	}
	return parent.DimensionDefinitions(), nil
}

func (*artifactsSchema) pathDefinitions(_ context.Context, parent *core.Artifacts, args struct {
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
	return parent.PathDefinitions(core.ArtifactURIOpts{Absolute: args.Absolute, TypeAssertion: args.TypeAssertion})
}
func (*artifactsSchema) dimensions(ctx context.Context, parent *core.Artifacts, _ struct{}) ([]string, error) {
	expanded, err := expandArtifacts(ctx, parent)
	if err != nil {
		return nil, err
	}
	return expanded.Dimensions(), nil
}
func (*artifactsSchema) dimensionKeys(ctx context.Context, parent *core.Artifacts, args struct{ Dimension string }) ([]string, error) {
	return parent.ExpandDimensionKeys(ctx, args.Dimension)
}

func (*artifactsSchema) dimensionItems(ctx context.Context, parent *core.Artifacts, args struct{ Dimension string }) ([]*core.Artifact, error) {
	return parent.ExpandDimensionItems(ctx, args.Dimension)
}
func (*artifactsSchema) items(ctx context.Context, parent *core.Artifacts, _ struct{}) ([]*core.Artifact, error) {
	expanded, err := expandArtifacts(ctx, parent)
	if err != nil {
		return nil, err
	}
	parent = expanded
	items := make([]*core.Artifact, 0, len(parent.Entries))
	for _, artifact := range parent.Entries {
		items = append(items, artifact.Clone())
	}
	return items, nil
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
func (*artifactsSchema) artifactURI(_ context.Context, parent *core.Artifact, args struct {
	Absolute      bool `default:"false"`
	DimensionKeys bool `default:"true"`
	TypeAssertion bool `default:"false"`
}) (string, error) {
	return parent.URI(core.ArtifactURIOpts{
		Absolute:      args.Absolute,
		DimensionKeys: args.DimensionKeys,
		TypeAssertion: args.TypeAssertion,
	})
}
func (*artifactsSchema) value(ctx context.Context, parent dagql.AnyResult, args map[string]dagql.Input) (dagql.AnyResult, error) {
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
			return nil, fmt.Errorf("artifacts of type Expertise receive their LLM through LLM.compose; artifact arguments are not supported")
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
func (*artifactsSchema) remoteCheck(ctx context.Context, artifact *core.Artifact, args struct{ Arguments core.JSON }) (*core.Check, error) {
	address, err := artifact.URI(core.ArtifactURIOpts{DimensionKeys: true})
	if err != nil {
		return nil, err
	}
	return &core.Check{RemoteArtifact: artifact.Clone(), RemoteArguments: args.Arguments, Address: address}, nil
}

func (*artifactsSchema) failedCheck(ctx context.Context, _ *core.Artifact, args struct{ Message string }) (*core.Check, error) {
	return &core.Check{Failure: args.Message, Address: core.CheckNameFromContext(ctx)}, nil
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

func (s *workspaceSchema) artifacts(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args struct {
	Include dagql.Optional[dagql.ArrayInput[dagql.String]]
}) (*core.Artifacts, error) {
	return s.collectArtifacts(ctx, parent, workspaceIncludePatterns(args.Include))
}

// collectArtifacts discovers the workspace's static artifacts. Include
// patterns narrow module loading and select each path and its children.
func (s *workspaceSchema) collectArtifacts(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], include []string) (*core.Artifacts, error) {
	result := &core.Artifacts{Entries: []*core.Artifact{}}
	if include != nil {
		result.Selector.Paths = make([]string, 0, len(include))
		for _, pattern := range include {
			result.Selector.Paths = append(result.Selector.Paths, core.IncludePatterns(pattern)...)
		}
	}
	for i := range include {
		include[i] = strings.ReplaceAll(include[i], "/", ":")
	}
	ctx, err := s.withWorkspaceClientContext(ctx, parent.Self())
	if err != nil {
		return nil, err
	}
	cfg, err := workspaceEffectiveConfig(ctx, parent.Self())
	if err != nil {
		return nil, err
	}
	mods, failures, err := s.workspaceTargetModules(ctx, parent, include, core.ModuleLoadRepairing)
	if err != nil {
		return nil, err
	}
	entrypoints, err := workspaceEntrypointNames(ctx, parent.Self())
	if err != nil {
		return nil, err
	}
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
		reparentWorkspaceTreeRoot(root, mod.Self().Name(), entrypoints[mod.Self().Name()])
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
		sdkNodes, err := s.sdkArtifactNodes(ctx, parent, cfg, entrypoints)
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
		match, err := matchWorkspaceInclude(ctx, node, include)
		if err != nil {
			return nil, err
		}
		if match {
			result.Entries = append(result.Entries, entry)
		}
	}
	seenFailures := map[string]bool{}
	for _, failure := range failures {
		if seenFailures[failure.Name] {
			continue
		}
		seenFailures[failure.Name] = true
		entry := &core.Artifact{
			ModuleName: failure.Name, Path: []string{failure.Name, "load"}, DimensionKeys: []*core.ArtifactDimensionKey{},
			Directives: []string{"check"}, TypeName: "Check", LoadFailure: &failure, Workspace: parent,
		}
		loaded = append(loaded, entry)
		result.Entries = append(result.Entries, entry)
	}
	result.AllDimensions = (&core.Artifacts{Entries: loaded}).DimensionDefinitions()
	result.NameEntries()
	slices.SortFunc(result.Entries, func(a, b *core.Artifact) int { return slices.Compare(a.Path, b.Path) })
	if err := validateArtifactPaths(result.Entries); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *workspaceSchema) sdkArtifactNodes(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], cfg *workspace.Config, entrypoints map[string]bool) ([]*core.ModTreeNode, error) {
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
		reparentWorkspaceTreeRoot(root, provider, entrypoints[provider])
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

// Keep planned artifacts in the query graph so value evaluation, remote
// execution, and saved result IDs all refer to the same batch selection.
func (*artifactsSchema) evaluationItems(ctx context.Context, parent *core.Artifacts, _ struct{}) ([]*core.Artifact, error) {
	selection, err := expandArtifacts(ctx, parent)
	if err != nil {
		return nil, err
	}
	return selection.Batch(ctx)
}

func (s *artifactsSchema) values(ctx context.Context, parent dagql.ObjectResult[*core.Artifacts], args struct {
	FailFast       bool      `default:"false"`
	Arguments      core.JSON `default:"{}"`
	MaxConcurrency int       `default:"0"`
}) ([]*core.ArtifactResult, error) {
	if args.MaxConcurrency < 0 {
		return nil, fmt.Errorf("maxConcurrency must not be negative: %d", args.MaxConcurrency)
	}
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var selection dagql.ObjectResultArray[*core.Artifact]
	if err := srv.Select(ctx, parent, &selection, dagql.Selector{Field: "__evaluationItems"}); err != nil {
		return nil, err
	}
	results := make([]*core.ArtifactResult, len(selection))
	evaluationErrors := make([]error, len(selection))
	remoteJobs := parallel.New().WithContextualTracer(true).WithFailFast(args.FailFast).WithRollupLogs(true).WithRollupSpans(true)
	// Checks that scale out run on cloud engines, so the limit applies only to
	// the jobs this engine runs.
	localJobs := remoteJobs
	if args.MaxConcurrency > 0 {
		localJobs = localJobs.WithLimit(args.MaxConcurrency)
	}
	for i, selected := range selection {
		artifact := selected.Self()
		uri, err := artifact.URI(core.ArtifactURIOpts{DimensionKeys: true})
		if err != nil {
			return nil, err
		}
		result := &core.ArtifactResult{Artifact: artifact.Clone()}
		results[i] = result
		var attrs []attribute.KeyValue
		localCheck := artifact.TypeName == "Check" && (!md.EnableCloudScaleOut || artifact.LoadFailure != nil || artifact.Workspace.Self() == nil)
		if artifact.TypeName == "Check" {
			// Name every check span by its dag:// address, whether it runs here or
			// scales out to a cloud engine, so CI suggestions reference the address.
			attrs = append(attrs, attribute.String(telemetry.CheckNameAttr, uri))
		}
		if slices.Contains(artifact.Directives, "generate") {
			attrs = append(attrs, attribute.String(telemetry.GeneratorNameAttr, uri))
		}
		group := &localJobs
		if artifact.TypeName == "Check" && !localCheck {
			group = &remoteJobs
		}
		*group = group.WithJob(uri, func(ctx context.Context) error {
			ctx = core.WithCheckName(ctx, uri)
			srv, err := core.CurrentDagqlServer(ctx)
			if err == nil {
				// Keep the value's subtree visible, including deferred execution logs.
				err = srv.Select(dagql.WithNonInternalTelemetry(ctx), selected, &result.Value, dagql.Selector{Field: "value", Args: []dagql.NamedInput{{Name: "arguments", Value: args.Arguments}}})
			}
			if err == nil {
				if sync, ok := result.Value.ObjectType().FieldSpec("sync", srv.View); ok && !sync.Args.HasRequired(srv.View) {
					var completed dagql.AnyResult
					err = srv.Select(ctx, result.Value, &completed, dagql.Selector{Field: "sync"})
					if err == nil {
						if value, ok := completed.(dagql.AnyObjectResult); ok && value.Type().Name() == result.Value.Type().Name() {
							result.Value = value
						}
						if check, ok := completed.(dagql.ObjectResult[*core.Check]); ok {
							if check.Self().Error.Valid {
								err = check.Self().Error.Value.Self()
							}
						}
					}
				}
			}
			if localCheck {
				trace.SpanFromContext(ctx).SetAttributes(attribute.Bool(telemetry.CheckPassedAttr, err == nil))
			}
			evaluationErrors[i] = err
			return err
		}, attrs...)
	}
	// Failures are data in the result list, including cancellation by fail-fast.
	runArtifactJobs(ctx, args.FailFast, localJobs, remoteJobs)
	for i, err := range evaluationErrors {
		if err == nil {
			continue
		}
		failure, err := core.NewErrorFromErr(ctx, err)
		if err != nil {
			return nil, err
		}
		results[i].Value = nil
		results[i].Error = dagql.NonNull(failure)
	}
	return results, nil
}

// runArtifactJobs runs the groups at the same time. With fail-fast, the first
// failure in any group cancels the jobs in every group.
func runArtifactJobs(ctx context.Context, failFast bool, groups ...parallel.Jobs) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wg sync.WaitGroup
	for _, group := range groups {
		if len(group.Jobs) == 0 {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := group.Run(ctx); err != nil && failFast {
				cancel(err)
			}
		}()
	}
	wg.Wait()
}

func (*artifactsSchema) resultValue(ctx context.Context, parent dagql.AnyResult, _ map[string]dagql.Input) (dagql.AnyResult, error) {
	result, ok := dagql.UnwrapAs[*core.ArtifactResult](parent)
	if !ok {
		return nil, fmt.Errorf("expected ArtifactResult")
	}
	if result.Value == nil {
		return dagql.NewResultForCurrentCall(ctx, dagql.Null[nodeInterfaceType]())
	}
	return result.Value, nil
}

func (*artifactsSchema) loadError(_ context.Context, artifact *core.Artifact, _ struct{}) (string, error) {
	if artifact.LoadFailure != nil {
		return artifact.LoadFailure.Message, nil
	}
	return "", nil
}

func (*artifactsSchema) asExpertise(ctx context.Context, parent dagql.ObjectResult[*core.Artifacts], _ struct{}) (dagql.ObjectResultArray[*core.Expertise], error) {
	return artifactValuesAs[*core.Expertise](ctx, parent)
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
	var items dagql.ObjectResultArray[*core.Artifact]
	if err := srv.Select(ctx, parent, &items, dagql.Selector{Field: "items"}); err != nil {
		return nil, err
	}
	var value T
	typeName := value.Type().NamedType
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
