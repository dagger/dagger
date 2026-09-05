package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	gitsession "github.com/dagger/dagger/engine/session/git"
)

type workspaceExportArgs struct {
	To dagql.Optional[dagql.ID[*core.Workspace]]
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
	// Resolve the actual destination on the *calling* client, not the target
	// object's owner or cached workspace read epoch. This digest keys its
	// reconstruction and is checked again by ApplyBundle as a ref-state lease.
	state, err := bk.GitCheckoutState(ctx, hostPath)
	if err != nil {
		return fmt.Errorf("read export destination: %w", err)
	}
	var targetRepo dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, srv.Root(), &targetRepo,
		dagql.Selector{Field: "host"},
		dagql.Selector{Field: "__gitDir", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(hostPath)},
			{Name: "stateDigest", Value: dagql.NewString(state)},
			{Name: "validateState", Value: dagql.NewBoolean(true)},
		}},
		dagql.Selector{Field: "asGit"},
	); err != nil {
		return fmt.Errorf("resolve export destination repository: %w", err)
	}
	remote, err := targetRepo.Self().LoadRemote(ctx)
	if err != nil {
		return err
	}
	var target dagql.ObjectResult[*core.GitRef]
	if remote.Get("HEAD") != nil || (remote.Head != nil && remote.Head.SHA != "") {
		if err := srv.Select(ctx, targetRepo, &target, dagql.Selector{Field: "head"}); err != nil {
			return fmt.Errorf("resolve export destination HEAD: %w", err)
		}
	}
	var head dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, source, &head, dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}); err != nil {
		return err
	}
	metadata := &gitsession.ApplyBundleMetadata{CheckoutPath: hostPath, TargetSha: head.Self().Ref.SHA, ExpectedStateDigest: state}
	if target.Self() != nil && metadata.TargetSha == target.Self().Ref.SHA {
		return bk.ApplyGitBundle(ctx, metadata, nil)
	}

	// Materialize the selected commit as HEAD, independently of whether the
	// original ref was a branch, tag or detached SHA and of keepGitDir. This is
	// a clean repository; neither the overlay nor mounts enter the bundle.
	var repo dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, source, &repo, dagql.Selector{Field: "__commitBase"}, dagql.Selector{Field: "asGit"}); err != nil {
		return err
	}
	// A common ancestor keeps the transfer thin even when histories diverged.
	// No common ancestor (or an unavailable optimization) falls back to a full
	// bundle, so the client can still retain the commits for manual integration.
	var base *core.GitRef
	if target.Self() != nil {
		base, _ = core.MergeBase(ctx, head.Self(), target.Self())
	}
	if base != nil && base.Ref.SHA == metadata.TargetSha {
		base = nil // Bundling an ancestor with itself excluded would be empty.
	}
	args := []dagql.NamedInput{{Name: "refs", Value: dagql.ArrayInput[dagql.String]{dagql.NewString("HEAD")}}}
	if base != nil {
		var baseRef dagql.ObjectResult[*core.GitRef]
		if err := srv.Select(ctx, repo, &baseRef, dagql.Selector{Field: "ref", Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString(base.Ref.SHA)},
		}}); err != nil {
			return err
		}
		baseID, err := baseRef.ID()
		if err != nil {
			return err
		}
		args = append(args, dagql.NamedInput{Name: "base", Value: dagql.Opt(dagql.NewID[*core.GitRef](baseID))})
	}
	var bundle dagql.ObjectResult[*core.GitBundle]
	if err := srv.Select(ctx, repo, &bundle, dagql.Selector{Field: "bundle", Args: args}); err != nil {
		return fmt.Errorf("create workspace export bundle: %w", err)
	}
	// HEAD may be advertised under its canonical branch name by GitBundle.
	if len(bundle.Self().Refs) != 1 || bundle.Self().Refs[0].SHA != metadata.TargetSha {
		return fmt.Errorf("workspace export bundle does not contain the selected HEAD")
	}
	metadata.BundleRef = bundle.Self().Refs[0].Name
	file := bundle.Self().File
	reader, err := file.Self().Open(ctx, file)
	if err != nil {
		return err
	}
	defer reader.Close()
	return bk.ApplyGitBundle(ctx, metadata, reader)
}
