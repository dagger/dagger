package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	gitsession "github.com/dagger/dagger/engine/session/git"
)

func (s *workspaceSchema) withExportBase(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], args struct {
	Base        dagql.ID[*core.Workspace]
	Path        string
	StateDigest string
}) (dagql.ObjectResult[*core.Workspace], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	base, err := args.Base.Load(ctx, srv)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	ws := parent.Self().Clone()
	ws.ExportBase, ws.ExportPath, ws.ExportStateDigest = base, args.Path, args.StateDigest
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ws)
}

func (s *workspaceSchema) exportDirectory(ctx context.Context, source dagql.ObjectResult[*core.Workspace], _ struct{}) (dagql.ObjectResult[*core.Directory], error) {
	var result dagql.ObjectResult[*core.Directory]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return result, err
	}
	base := source.Self().ExportBase
	if base.Self() == nil {
		return result, fmt.Errorf("workspace has no prepared checkout integration")
	}
	var repo dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, source, &repo, dagql.Selector{Field: "__commitBase"}); err != nil {
		return result, err
	}
	var head dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, base, &head, dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}); err != nil {
		return result, err
	}
	var before, after dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, base, &before, dagql.Selector{Field: "git"}, dagql.Selector{Field: "uncommitted"}); err != nil {
		return result, err
	}
	if err := srv.Select(ctx, source, &after, dagql.Selector{Field: "git"}, dagql.Selector{Field: "uncommitted"}); err != nil {
		return result, err
	}
	dir, err := core.WorkspaceExportDirectory(ctx, repo, head.Self().Ref.SHA, before.Self(), after.Self())
	if err != nil {
		return result, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}

func (s *workspaceSchema) exportWorkspaceGit(ctx context.Context, source dagql.ObjectResult[*core.Workspace], hostPath string) error {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return err
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return err
	}
	var head, base dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, source, &head, dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}); err != nil {
		return err
	}
	if err := srv.Select(ctx, source.Self().ExportBase, &base, dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}); err != nil {
		return err
	}
	var repo dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, source, &repo, dagql.Selector{Field: "__exportDirectory"}, dagql.Selector{Field: "asGit"}); err != nil {
		return err
	}
	var prerequisite dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, repo, &prerequisite, dagql.Selector{Field: "ref", Args: []dagql.NamedInput{{Name: "name", Value: dagql.NewString(base.Self().Ref.SHA)}}}); err != nil {
		return err
	}
	baseID, err := prerequisite.ID()
	if err != nil {
		return err
	}
	var bundle dagql.ObjectResult[*core.GitBundle]
	if err := srv.Select(ctx, repo, &bundle, dagql.Selector{Field: "bundle", Args: []dagql.NamedInput{
		{Name: "refs", Value: dagql.ArrayInput[dagql.String]{dagql.NewString("HEAD")}},
		{Name: "base", Value: dagql.Opt(dagql.NewID[*core.GitRef](baseID))},
	}}); err != nil {
		return err
	}
	if len(bundle.Self().Refs) != 1 {
		return fmt.Errorf("workspace export bundle must contain one transport ref")
	}
	metadata := &gitsession.ApplyBundleMetadata{
		CheckoutPath:           hostPath,
		TargetSha:              head.Self().Ref.SHA,
		ExpectedStateDigest:    source.Self().ExportStateDigest,
		BundleRef:              bundle.Self().Refs[0].Name,
		IntegrationWorktreeSha: bundle.Self().Refs[0].SHA,
	}
	file := bundle.Self().File
	reader, err := file.Self().Open(ctx, file)
	if err != nil {
		return err
	}
	defer reader.Close()
	return bk.ApplyGitBundle(ctx, metadata, reader)
}
