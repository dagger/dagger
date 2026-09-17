package schema

import (
	"context"
	"fmt"
	"slices"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type artifactsSchema struct{}

func (s *artifactsSchema) Install(srv *dagql.Server) {
	srv.InstallObject(dagql.NewClass[*core.ArtifactCollectionKey](srv).View(AfterVersion("v1.0.0-0")))
	artifactClass := dagql.NewClass[*core.Artifact](srv).View(AfterVersion("v1.0.0-0"))
	srv.InstallObject(artifactClass)
	srv.InstallObject(dagql.NewClass[*core.Artifacts](srv).View(AfterVersion("v1.0.0-0")))
	dagql.Fields[*core.ArtifactCollectionKey]{}.Install(srv)
	dagql.Fields[*core.Artifacts]{
		dagql.Func("types", s.types).Doc("List concrete GraphQL types represented in this selection, sorted with no duplicates."),
		dagql.Func("filterTypes", s.filterTypes).Doc("Keep artifacts of any listed concrete GraphQL type."),
		dagql.Func("filterQuery", s.filterQuery).Doc("Match one complete, ordered field sequence exactly."),
		dagql.Func("filterCollections", s.filterCollections).Doc("Keep artifacts selected through any listed collection."),
		dagql.Func("filterCollectionKeys", s.filterCollectionKeys).Doc("Keep artifacts with any listed key in this collection."),
		dagql.Func("collections", s.collections).Doc("List collection identifiers represented in this selection, sorted with no duplicates."),
		dagql.Func("collectionKeys", s.collectionKeys).Doc("List keys represented in this selection for the given collection, sorted with no duplicates."),
		dagql.Func("items", s.items).Doc("Enumerate complete artifacts without evaluating their values."),
		dagql.Func("one", s.one).Doc("Require exactly one artifact; fail if there are zero or multiple matches."),
		dagql.Func("pretty", s.pretty).Doc("Display lines for this selection, with no trailing newlines."),
	}.Install(srv)
	dagql.Fields[*core.Artifact]{
		dagql.Func("pretty", s.artifactPretty).Doc("The full address, formatted for CLI input with consistent flag order."),
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
func (*artifactsSchema) filterQuery(_ context.Context, parent *core.Artifacts, args struct{ Query []string }) (*core.Artifacts, error) {
	return parent.FilterQuery(args.Query), nil
}
func (*artifactsSchema) filterCollections(_ context.Context, parent *core.Artifacts, args struct{ Collections []string }) (*core.Artifacts, error) {
	return parent.FilterCollections(args.Collections), nil
}
func (*artifactsSchema) filterCollectionKeys(_ context.Context, parent *core.Artifacts, args struct {
	Collection string
	Keys       []string
}) (*core.Artifacts, error) {
	return parent.FilterCollectionKeys(args.Collection, args.Keys), nil
}
func (*artifactsSchema) collections(_ context.Context, parent *core.Artifacts, _ struct{}) ([]string, error) {
	return parent.Collections(), nil
}
func (*artifactsSchema) collectionKeys(_ context.Context, parent *core.Artifacts, args struct{ Collection string }) ([]string, error) {
	return parent.CollectionKeys(args.Collection), nil
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
func (*artifactsSchema) pretty(_ context.Context, parent *core.Artifacts, _ struct{}) ([]string, error) {
	return parent.Pretty(), nil
}
func (*artifactsSchema) artifactPretty(_ context.Context, parent *core.Artifact, _ struct{}) (string, error) {
	return parent.Pretty(), nil
}
func (*artifactsSchema) value(ctx context.Context, parent dagql.AnyResult, _ map[string]dagql.Input) (dagql.AnyResult, error) {
	artifact, ok := dagql.UnwrapAs[*core.Artifact](parent)
	if !ok {
		return nil, fmt.Errorf("expected Artifact, got %T", parent.Unwrap())
	}
	ctx, err := withWorkspaceClientContext(ctx, artifact.Workspace.Self())
	if err != nil {
		return nil, err
	}
	ctx = core.WorkspaceToContext(ctx, artifact.Workspace)
	var result dagql.AnyObjectResult
	if err := artifact.Node.DagqlValue(ctx, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *workspaceSchema) artifacts(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], _ struct{}) (*core.Artifacts, error) {
	nodes, err := collectWorkspaceModuleTargets(ctx, s, parent, nil, nil, "artifacts", "artifact", core.ModuleArtifactNodes, func(node *core.ModTreeNode) *core.ModTreeNode { return node })
	if err != nil {
		return nil, err
	}
	result := &core.Artifacts{Entries: make([]*core.Artifact, 0, len(nodes))}
	for _, node := range nodes {
		result.Entries = append(result.Entries, &core.Artifact{
			Query: node.CommandPath().CliCase(), CollectionKeys: []*core.ArtifactCollectionKey{},
			TypeName: node.ObjectType().Name, Node: node, Workspace: parent,
		})
	}
	slices.SortFunc(result.Entries, func(a, b *core.Artifact) int { return slices.Compare(a.Query, b.Query) })
	for i := 1; i < len(result.Entries); i++ {
		if slices.Equal(result.Entries[i-1].Query, result.Entries[i].Query) {
			return nil, fmt.Errorf("ambiguous artifact query %q", result.Entries[i].Pretty())
		}
	}
	return result, nil
}
