package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dagger/dagger/core"
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
	dagql.Fields[*core.ArtifactDimension]{}.Install(srv)
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
		dagql.NodeFunc("asAgentMiddlewares", s.asAgentMiddlewares).Doc("Convert the selection to agent middleware without running the functions. Fail if any artifact is not an agent middleware."),
		dagql.NodeFunc("asChecks", s.asChecks).Doc("Convert the selection to Checks. Fail if any artifact is not a Check. Does not apply command filters or run the checks."),
		dagql.NodeFunc("asChangesets", s.asChangesets).Doc("Convert the selection to Changesets. Fail if any artifact is not a Changeset. Does not apply command filters."),
		dagql.NodeFunc("asServices", s.asServices).Doc("Convert the selection to Services. Fail if any artifact is not a Service. Does not apply command filters or start the services."),
		dagql.Func("pathDefinitions", s.pathDefinitions).Doc("List selected schema paths, including empty collections. Does not read runtime values or resolve dimension-key filters.").Args(dagql.Arg("absolute").Doc("Prefix each address with the workspace's Git address and commit.")),
		dagql.Func("dimensionDefinitions", s.dimensionDefinitions).Doc("List dimensions on the selected schema paths, including empty collections. Does not read runtime values."),
		// Each invocation gets a new cache key. Retain its results so SDK clients can load their IDs.
		dagql.NodeFunc("values", s.values).WithInput(dagql.PerCallInput).Doc("Evaluate the selection in parallel, retaining each result and error.").Args(dagql.Arg("failFast").Doc("Cancel remaining work after the first failure."), dagql.Arg("arguments").Doc("Field arguments applied to each artifact, as a JSON object.")),
		dagql.Func("types", s.types).Doc("List concrete type definitions represented in this selection, sorted by name with no duplicates."),
		dagql.Func("filterCheckCommand", s.filterCheckCommand).Doc("Select Check artifacts for dagger check, using each workspace's check and generator settings. Include stale checks only for Changesets marked generate.").Args(dagql.Arg("generated").Doc("Include generated-file checks. Defaults to the workspace check-generated setting, or true when unset.")),
		dagql.Func("filterGenerateCommand", s.filterGenerateCommand).Doc("Select Changeset artifacts marked generate, using each workspace's generator settings."),
		dagql.Func("filterAgentCommand", s.filterAgentCommand).Doc("Select LLM artifacts marked agent."),
		dagql.Func("filterUpCommand", s.filterUpCommand).Doc("Select Service artifacts marked up, using each workspace's service settings."),
		dagql.Func("filterDirectives", s.filterDirectives).Doc("Keep artifacts with any listed directive. Does not filter by type or workspace settings.").Args(dagql.Arg("directives"), dagql.Arg("exclude").Doc("Remove the matching artifacts instead.")),
		dagql.Func("filterParentTypes", s.filterParentTypes).Doc("Keep artifacts whose immediate parent has any listed object type. Artifacts without a typed parent do not match.").Args(dagql.Arg("types"), dagql.Arg("exclude").Doc("Remove the matching artifacts instead.")),
		dagql.Func("filterParentDirectives", s.filterParentDirectives).Doc("Keep artifacts whose immediate parent has any listed directive. Artifacts without a parent do not match.").Args(dagql.Arg("directives"), dagql.Arg("exclude").Doc("Remove the matching artifacts instead.")),
		dagql.Func("withArtifacts", s.withArtifacts).Doc("Combine two selections, keeping each workspace address once. Different addresses remain distinct even if they return the same object."),
		dagql.Func("withoutUri", s.withoutURI).Doc("Remove artifacts selected by a DAG address."),
		dagql.Func("filterTypes", s.filterTypes).Doc("Keep artifacts of any listed concrete GraphQL type.").Args(dagql.Arg("types"), dagql.Arg("exclude").Doc("Remove the matching artifacts instead.")),
		dagql.Func("filterPath", s.filterPath).Doc("Match one complete, ordered field sequence exactly."),
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
		dagql.Func("__remoteCheck", s.remoteCheck),
		dagql.Func("__failedCheck", s.failedCheck),
		dagql.Func("loadError", s.loadError).Doc("A module load failure, or an empty string if discovery succeeded."),
		dagql.Func("arguments", s.arguments).Doc("The arguments accepted by the artifact field."),
		dagql.Func("description", s.description).Doc("The description of the field that supplies this artifact."),
		dagql.Func("uri", s.artifactURI).
			Doc("The artifact's DAG address, such as dag://engine-dev/playground.").
			Args(
				dagql.Arg("absolute").Doc("Prefix the workspace's Git address and commit: dag://<workspace>@<commit>:<path>. Fails if the workspace has no Git address."),
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

func (*artifactsSchema) types(ctx context.Context, parent *core.Artifacts, _ struct{}) (dagql.ObjectResultArray[*core.TypeDef], error) {
	parent, err := expandArtifacts(ctx, parent)
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
// workspace, at its own commit, is accepted.
func checkArtifactAddressWorkspace(entries []*core.Artifact, addr *dagaddress.Address, uri string) error {
	if !addr.Absolute {
		return nil
	}
	notSupported := fmt.Errorf("address selects another workspace; filters cannot change workspace: %s", uri)
	if len(entries) == 0 {
		return notSupported
	}
	for _, artifact := range entries {
		address, commit, err := artifact.Workspace.Self().GitAddress()
		if err != nil || address != addr.Workspace || commit != addr.Version {
			return notSupported
		}
	}
	return nil
}

func (*artifactsSchema) dimensionDefinitions(_ context.Context, parent *core.Artifacts, _ struct{}) ([]*core.ArtifactDimension, error) {
	return parent.DimensionDefinitions(), nil
}

func (s *artifactsSchema) pathDefinitions(ctx context.Context, parent *core.Artifacts, args struct {
	Absolute bool `default:"false"`
}) ([]*core.ArtifactPath, error) {
	paths := map[string]*core.ArtifactPath{}
	for _, entry := range parent.Entries {
		uri, err := entry.URI(core.ArtifactURIOpts{Absolute: args.Absolute})
		if err != nil {
			return nil, err
		}
		path := paths[uri]
		if path == nil {
			description, err := s.description(ctx, entry, struct{}{})
			if err != nil {
				return nil, err
			}
			path = &core.ArtifactPath{URI: uri, Description: description, Dimensions: []string{}}
			paths[uri] = path
		}
		for _, dimension := range entry.DimensionDefinitions() {
			if !slices.Contains(path.Dimensions, dimension.Identifier) {
				path.Dimensions = append(path.Dimensions, dimension.Identifier)
			}
		}
	}
	result := make([]*core.ArtifactPath, 0, len(paths))
	for _, path := range paths {
		result = append(result, path)
	}
	slices.SortFunc(result, func(a, b *core.ArtifactPath) int { return strings.Compare(a.URI, b.URI) })
	return result, nil
}
func (*artifactsSchema) dimensions(ctx context.Context, parent *core.Artifacts, _ struct{}) ([]string, error) {
	expanded, err := expandArtifacts(ctx, parent)
	if err != nil {
		return nil, err
	}
	return expanded.Dimensions(), nil
}
func (s *artifactsSchema) dimensionKeys(ctx context.Context, parent *core.Artifacts, args struct{ Dimension string }) ([]string, error) {
	items, err := s.dimensionItems(ctx, parent, args)
	if err != nil {
		return nil, err
	}
	dimension, err := parent.ResolveDimension(args.Dimension)
	if err != nil {
		return nil, err
	}
	return (&core.Artifacts{Entries: items}).DimensionKeys(dimension), nil
}
func (*artifactsSchema) dimensionItems(ctx context.Context, parent *core.Artifacts, args struct{ Dimension string }) ([]*core.Artifact, error) {
	dimension, err := parent.ResolveDimension(args.Dimension)
	if err != nil {
		return nil, err
	}
	filtered, err := parent.FilterDimensions([]string{args.Dimension}).BindDimensions()
	if err != nil {
		return nil, err
	}
	expanded, err := expandArtifacts(ctx, filtered.ForDimensionKeys(dimension))
	if err != nil {
		return nil, err
	}
	return expanded.DimensionItems(dimension)
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
	for i, artifact := range parent.Entries {
		if len(artifact.DimensionKeys) > 0 {
			return "", fmt.Errorf("a selection with resolved collection keys has no single DAG address; use the individual artifact addresses")
		}
		id, err := artifact.Workspace.ID()
		if err != nil {
			return "", err
		}
		if i > 0 && id.EngineResultID() != workspaceID {
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

func expandArtifacts(ctx context.Context, parent *core.Artifacts) (*core.Artifacts, error) {
	if len(parent.Entries) > 0 && parent.Entries[0].Workspace.Self() != nil {
		ws := parent.Entries[0].Workspace
		var err error
		ctx, err = withWorkspaceClientContext(ctx, ws.Self())
		if err != nil {
			return nil, err
		}
		ctx = core.WorkspaceToContext(ctx, ws)
	}
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
	if artifact.TypeName == "Check" && artifact.LoadFailure == nil {
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
func (*artifactsSchema) remoteCheck(_ context.Context, artifact *core.Artifact, args struct{ Arguments core.JSON }) (*core.Check, error) {
	return &core.Check{RemoteArtifact: artifact.Clone(), RemoteArguments: args.Arguments}, nil
}

func (*artifactsSchema) failedCheck(_ context.Context, _ *core.Artifact, args struct{ Message string }) (*core.Check, error) {
	return &core.Check{Failure: args.Message}, nil
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

// evaluateArtifact selects the artifact's value in its own workspace.
func evaluateArtifact(ctx context.Context, artifact *core.Artifact, dest any, inputs ...dagql.NamedInput) error {
	ctx, err := withWorkspaceClientContext(ctx, artifact.Workspace.Self())
	if err != nil {
		return err
	}
	ctx = core.WorkspaceToContext(ctx, artifact.Workspace)
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
			result.Selector.Paths = append(result.Selector.Paths, core.IncludePattern(pattern))
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
			// Installed SDKs supply their engine-managed generator below.
			if sdkProviders[mod.Self().Name()] && artifactGeneratorNode(node) != nil {
				continue
			}
			nodes = append(nodes, node)
		}
	}
	if parent.Self().ConfigFile != "" && len(cfg.SDKs) > 0 {
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
	}
	for _, node := range nodes {
		match, err := matchWorkspaceInclude(ctx, node, include)
		if err != nil {
			return nil, err
		}
		if !match {
			continue
		}
		path := node.CommandPath().CliCase()
		if len(path) == 0 {
			path = node.Path().CliCase()
		}
		result.Entries = append(result.Entries, &core.Artifact{
			Path: path, DimensionKeys: []*core.ArtifactDimensionKey{},
			Directives: node.Directives, TypeName: node.ObjectType().Name, Node: node, Workspace: parent,
		})
	}
	seenFailures := map[string]bool{}
	for _, failure := range failures {
		if seenFailures[failure.Name] {
			continue
		}
		seenFailures[failure.Name] = true
		result.Entries = append(result.Entries, &core.Artifact{
			Path: []string{failure.Name, "load"}, DimensionKeys: []*core.ArtifactDimensionKey{},
			Directives: []string{"check"}, TypeName: "Check", LoadFailure: &failure, Workspace: parent,
		})
	}
	slices.SortFunc(result.Entries, func(a, b *core.Artifact) int { return slices.Compare(a.Path, b.Path) })
	if err := validateArtifactPaths(result.Entries); err != nil {
		return nil, err
	}
	return result, nil
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

// Generator wrappers replace a raw toolchain's generator namespace. Its skip
// settings still apply to the wrapper, which runs the same implementation.
type artifactGeneratorPolicy struct {
	source  string
	wrapper bool
	skip    []string
}

func artifactGeneratorPolicies(ctx context.Context, entries []*core.Artifact, cfg *workspace.Config) (map[string]artifactGeneratorPolicy, map[string]bool, error) {
	policies := map[string]artifactGeneratorPolicy{}
	wrappers := map[string]bool{}
	rawSkips := map[string][]string{}
	for _, artifact := range entries {
		if artifact.Node == nil || artifact.Node.Module.Self() == nil {
			continue
		}
		mod := artifact.Node.Module.Self()
		if _, found := policies[mod.Name()]; found {
			continue
		}
		source := mod.GetSource()
		if source == nil {
			continue
		}
		digest, err := source.SourceImplementationDigest(ctx)
		if err != nil {
			return nil, nil, err
		}
		policy := artifactGeneratorPolicy{source: digest.String(), skip: cfg.Modules[mod.Name()].Generate.Skip}
		if contextSource := mod.GetContextSource(); contextSource != nil {
			contextDigest, err := contextSource.SourceImplementationDigest(ctx)
			if err != nil {
				return nil, nil, err
			}
			policy.wrapper = digest != contextDigest
		}
		policies[mod.Name()] = policy
		if policy.wrapper {
			wrappers[policy.source] = true
		} else {
			rawSkips[policy.source] = append(rawSkips[policy.source], policy.skip...)
		}
	}
	for name, policy := range policies {
		if policy.wrapper {
			policy.skip = append(slices.Clone(policy.skip), rawSkips[policy.source]...)
			policies[name] = policy
		}
	}
	return policies, wrappers, nil
}

func (*artifactsSchema) filterDirectives(_ context.Context, parent *core.Artifacts, args artifactDirectiveFilterArgs) (*core.Artifacts, error) {
	return parent.FilterDirectives(args.Directives, args.Exclude), nil
}

func (*artifactsSchema) filterCheckCommand(ctx context.Context, parent *core.Artifacts, args struct {
	Generated dagql.Optional[dagql.Boolean]
}) (*core.Artifacts, error) {
	return filterArtifactCommand(ctx, parent, "check", "Check", args.Generated)
}

func (*artifactsSchema) filterGenerateCommand(ctx context.Context, parent *core.Artifacts, _ struct{}) (*core.Artifacts, error) {
	return filterArtifactCommand(ctx, parent, "generate", "Changeset", dagql.Optional[dagql.Boolean]{})
}

func (*artifactsSchema) filterUpCommand(ctx context.Context, parent *core.Artifacts, _ struct{}) (*core.Artifacts, error) {
	return filterArtifactCommand(ctx, parent, "up", "Service", dagql.Optional[dagql.Boolean]{})
}

func (*artifactsSchema) filterAgentCommand(_ context.Context, parent *core.Artifacts, _ struct{}) (*core.Artifacts, error) {
	return parent.FilterDirectives([]string{"agent"}, false).FilterTypes([]string{"LLM"}, false), nil
}

type artifactWorkspacePolicy struct {
	config   *workspace.Config
	modules  map[string]artifactGeneratorPolicy
	wrappers map[string]bool
}

func filterArtifactCommand(ctx context.Context, parent *core.Artifacts, directive, typeName string, generated dagql.Optional[dagql.Boolean]) (*core.Artifacts, error) {
	selected := parent.FilterDirectives([]string{directive}, false).FilterTypes([]string{typeName}, false)
	if len(selected.Entries) == 0 {
		return selected, nil
	}
	// Keep each workspace's settings when the selection is a union.
	byWorkspace := map[uint64][]*core.Artifact{}
	for _, artifact := range parent.Entries {
		id, err := artifact.Workspace.ID()
		if err != nil {
			return nil, err
		}
		byWorkspace[id.EngineResultID()] = append(byWorkspace[id.EngineResultID()], artifact)
	}
	policies := map[uint64]artifactWorkspacePolicy{}
	for id, entries := range byWorkspace {
		cfg, err := workspaceEffectiveConfig(ctx, entries[0].Workspace.Self())
		if err != nil {
			return nil, err
		}
		policy := artifactWorkspacePolicy{config: cfg}
		if directive == "check" || directive == "generate" {
			policy.modules, policy.wrappers, err = artifactGeneratorPolicies(ctx, entries, cfg)
			if err != nil {
				return nil, err
			}
		}
		policies[id] = policy
	}
	excluded := &core.Artifacts{}
	for _, artifact := range selected.Entries {
		id, err := artifact.Workspace.ID()
		if err != nil {
			return nil, err
		}
		enabled, err := policies[id.EngineResultID()].enabled(ctx, artifact, directive, generated)
		if err != nil {
			return nil, err
		}
		if !enabled {
			excluded.Entries = append(excluded.Entries, artifact)
		}
	}
	return selected.WithoutArtifacts(excluded)
}

func (policy artifactWorkspacePolicy) enabled(ctx context.Context, artifact *core.Artifact, directive string, generated dagql.Optional[dagql.Boolean]) (bool, error) {
	// Module-load failures remain visible to check commands.
	if artifact.Node == nil || len(artifact.Node.Path()) == 0 {
		return true, nil
	}
	if directive == "check" {
		if parent := artifact.Node.Parent.ObjectType(); parent != nil && parent.Name == "Changeset" {
			if !slices.Contains(artifact.Node.Parent.Directives, "generate") {
				return false, nil
			}
			checkGenerated := true
			if policy.config.CheckGenerated != nil {
				checkGenerated = *policy.config.CheckGenerated
			}
			if generated.Valid {
				checkGenerated = bool(generated.Value)
			}
			if !checkGenerated {
				return false, nil
			}
		}
	}
	name := artifact.Node.Path()[0]
	entry := policy.config.Modules[name]
	var skip []string
	switch directive {
	case "check":
		skip = entry.Check.Skip
	case "up":
		skip = entry.Up.Skip
	}
	nodes := []*core.ModTreeNode{artifact.Node}
	if generator := artifactGeneratorNode(artifact.Node); generator != nil {
		nodes = append(nodes, generator)
		if module, found := policy.modules[name]; found {
			if policy.wrappers[module.source] && !module.wrapper {
				return false, nil
			}
			skip = append(slices.Clone(skip), module.skip...)
		} else {
			skip = append(slices.Clone(skip), entry.Generate.Skip...)
		}
	}
	if len(skip) == 0 {
		return true, nil
	}
	match, err := matchAnyNode(ctx, nodes, skip, true)
	return !match, err
}

func (*artifactsSchema) description(_ context.Context, parent *core.Artifact, _ struct{}) (string, error) {
	if parent.LoadFailure != nil {
		return "this workspace module could not be loaded", nil
	}
	if parent.Node == nil {
		return "", nil
	}
	generator := parent.Node.Parent
	if parent.Node.Name == "stale" && generator != nil && slices.Contains(generator.Directives, "generate") {
		if obj := generator.ObjectType(); obj != nil && obj.Name == "Changeset" && obj.SourceModuleName == "" {
			description, _, _ := strings.Cut(generator.Description, "\n")
			description = strings.TrimRight(strings.TrimSpace(description), ".:;!?")
			if description == "" {
				return "staleness check", nil
			}
			first, size := utf8.DecodeRuneInString(description)
			return "staleness check: " + string(unicode.ToLower(first)) + description[size:], nil
		}
	}
	return parent.Node.Description, nil
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
	FailFast  bool      `default:"false"`
	Arguments core.JSON `default:"{}"`
}) ([]*core.ArtifactResult, error) {
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
	jobs := parallel.New().WithContextualTracer(true).WithFailFast(args.FailFast).WithRollupLogs(true).WithRollupSpans(true)
	for i, selected := range selection {
		artifact := selected.Self()
		uri, err := artifact.URI(core.ArtifactURIOpts{DimensionKeys: true})
		if err != nil {
			return nil, err
		}
		result := &core.ArtifactResult{Artifact: artifact.Clone()}
		results[i] = result
		var attrs []attribute.KeyValue
		localCheck := artifact.TypeName == "Check" && (!md.EnableCloudScaleOut || artifact.LoadFailure != nil)
		if localCheck {
			attrs = append(attrs, attribute.String(telemetry.CheckNameAttr, uri))
		}
		if slices.Contains(artifact.Directives, "generate") {
			attrs = append(attrs, attribute.String(telemetry.GeneratorNameAttr, uri))
		}
		jobs = jobs.WithJob(uri, func(ctx context.Context) error {
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
						if check, ok := completed.(dagql.ObjectResult[*core.Check]); ok {
							result.Value = check
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
	_ = jobs.Run(ctx)
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

func (*artifactsSchema) asAgentMiddlewares(ctx context.Context, parent dagql.ObjectResult[*core.Artifacts], _ struct{}) ([]*core.AgentMiddleware, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var items dagql.ObjectResultArray[*core.Artifact]
	if err := srv.Select(ctx, parent, &items, dagql.Selector{Field: "items"}); err != nil {
		return nil, err
	}
	agents := make([]*core.AgentMiddleware, len(items))
	for i, item := range items {
		agents[i], err = core.NewAgentMiddleware(item.Self())
		if err != nil {
			return nil, err
		}
	}
	return agents, nil
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
