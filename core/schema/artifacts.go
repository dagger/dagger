package schema

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/dagger/dagger/dagql"
)

type artifactsSchema struct{}

func (s *artifactsSchema) Install(srv *dagql.Server) {
	srv.InstallObject(dagql.NewClass[*core.ArtifactDimensionKey](srv).View(AfterVersion("v1.0.0-0")))
	artifactClass := dagql.NewClass[*core.Artifact](srv).View(AfterVersion("v1.0.0-0"))
	srv.InstallObject(artifactClass)
	srv.InstallObject(dagql.NewClass[*core.Artifacts](srv).View(AfterVersion("v1.0.0-0")))
	dagql.Fields[*core.ArtifactDimensionKey]{}.Install(srv)
	dagql.Fields[*core.Artifacts]{
		dagql.Func("types", s.types).Doc("List concrete GraphQL types represented in this selection, sorted with no duplicates."),
		dagql.Func("filterTypes", s.filterTypes).Doc("Keep artifacts of any listed concrete GraphQL type."),
		dagql.Func("filterPath", s.filterPath).Doc("Match one complete, ordered field sequence exactly."),
		dagql.Func("filterDimensions", s.filterDimensions).Doc("Keep artifacts selected through any listed dimension."),
		dagql.Func("filterDimensionKeys", s.filterDimensionKeys).Doc("Keep artifacts with any listed key in this dimension."),
		dagql.Func("filterUri", s.filterURI).
			Doc("Apply a DAG address as one filter: the chain of path, type, and dimension-key filters it encodes.",
				"The scheme is optional. The path may be a pattern; an empty path selects all artifacts.").
			Args(dagql.Arg("uri").Doc("A DAG address: [dag[+<type>]://][<path>][?<dimension>=<key>&...]")),
		dagql.Func("dimensions", s.dimensions).Doc("List dimension identifiers represented in this selection, sorted with no duplicates."),
		dagql.Func("dimensionKeys", s.dimensionKeys).Doc("List keys represented in this selection for the given dimension, sorted with no duplicates."),
		dagql.Func("items", s.items).Doc("Enumerate complete artifacts without evaluating their values."),
		dagql.Func("one", s.one).Doc("Require exactly one artifact; fail if there are zero or multiple matches. Several matches are listed, one address per line."),
		dagql.Func("uri", s.uri).Doc("The DAG address that selects this whole selection: filterUri(uri) selects the same set."),
	}.Install(srv)
	dagql.Fields[*core.Artifact]{
		dagql.Func("uri", s.artifactURI).
			Doc("The artifact's DAG address, such as dag://engine-dev/playground.").
			Args(
				dagql.Arg("absolute").Doc("Prefix the workspace's Git address and commit: dag://<workspace>@<commit>:<path>. Fails if the workspace has no Git address."),
				dagql.Arg("dimensionKeys").Doc("Include the dimension keys as a query. Without them, the address is a path selector."),
				dagql.Arg("typeAssertion").Doc("Include the artifact type in the scheme: dag+container://."),
			),
	}.Install(srv)
	artifactClass.Extend(dagql.FieldSpec{
		Name: "value", Type: nodeInterfaceType{}, Args: dagql.NewInputSpecs(),
		Description: "Evaluate the target in the workspace that supplied this artifact.",
		ViewFilter:  AfterVersion("v1.0.0-0"),
	}, s.value)
}

func (*artifactsSchema) types(_ context.Context, parent *core.Artifacts, _ struct{}) ([]string, error) {
	return parent.Types(), nil
}
func (*artifactsSchema) filterTypes(_ context.Context, parent *core.Artifacts, args struct{ Types []string }) (*core.Artifacts, error) {
	return parent.FilterTypes(args.Types), nil
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
	notSupported := fmt.Errorf("absolute addresses are not supported yet: %s", uri)
	if len(entries) == 0 {
		return notSupported
	}
	address, commit, err := entries[0].Workspace.Self().GitAddress()
	if err != nil || address != addr.Workspace || commit != addr.Version {
		return notSupported
	}
	return nil
}

func (*artifactsSchema) dimensions(_ context.Context, parent *core.Artifacts, _ struct{}) ([]string, error) {
	return parent.Dimensions(), nil
}
func (*artifactsSchema) dimensionKeys(_ context.Context, parent *core.Artifacts, args struct{ Dimension string }) ([]string, error) {
	return parent.DimensionKeys(args.Dimension), nil
}
func (*artifactsSchema) items(_ context.Context, parent *core.Artifacts, _ struct{}) ([]*core.Artifact, error) {
	items := make([]*core.Artifact, 0, len(parent.Entries))
	for _, artifact := range parent.Entries {
		items = append(items, artifact.Clone())
	}
	return items, nil
}
func (*artifactsSchema) one(_ context.Context, parent *core.Artifacts, _ struct{}) (*core.Artifact, error) {
	return parent.One()
}
func (*artifactsSchema) uri(_ context.Context, parent *core.Artifacts, _ struct{}) (string, error) {
	return parent.URI(), nil
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
func (*artifactsSchema) value(ctx context.Context, parent dagql.AnyResult, _ map[string]dagql.Input) (dagql.AnyResult, error) {
	artifact, ok := dagql.UnwrapAs[*core.Artifact](parent)
	if !ok {
		return nil, fmt.Errorf("expected Artifact, got %T", parent.Unwrap())
	}
	var result dagql.AnyObjectResult
	if err := evaluateArtifact(ctx, artifact, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// evaluateArtifact selects the artifact's value in its own workspace.
func evaluateArtifact(ctx context.Context, artifact *core.Artifact, dest any) error {
	ctx, err := withWorkspaceClientContext(ctx, artifact.Workspace.Self())
	if err != nil {
		return err
	}
	ctx = core.WorkspaceToContext(ctx, artifact.Workspace)
	return artifact.Evaluate(ctx, dest)
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
	nodes, err := collectWorkspaceModuleTargets(ctx, s, parent, include, nil, "artifacts", "artifact", core.ModuleArtifactNodes, func(node *core.ModTreeNode) *core.ModTreeNode { return node })
	if err != nil {
		return nil, err
	}
	for _, node := range nodes {
		path := node.CommandPath().CliCase()
		if len(path) == 0 {
			path = node.Path().CliCase()
		}
		result.Entries = append(result.Entries, &core.Artifact{
			Path: path, DimensionKeys: []*core.ArtifactDimensionKey{},
			TypeName: node.ObjectType().Name, Node: node, Workspace: parent,
		})
	}
	slices.SortFunc(result.Entries, func(a, b *core.Artifact) int { return slices.Compare(a.Path, b.Path) })
	for i := 1; i < len(result.Entries); i++ {
		if slices.Equal(result.Entries[i-1].Path, result.Entries[i].Path) {
			return nil, fmt.Errorf("ambiguous artifact path %q", strings.Join(result.Entries[i].Path, "/"))
		}
	}
	return result, nil
}
