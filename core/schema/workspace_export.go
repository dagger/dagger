package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	gitsession "github.com/dagger/dagger/engine/session/git"
)

type workspaceExportArgs struct {
	Path string `default:""`
	From dagql.Optional[dagql.ID[*core.Workspace]]
}

type workspaceSaveArgs struct {
	Destination    dagql.ID[*core.Workspace]
	From           dagql.Optional[dagql.ID[*core.Workspace]]
	CommitterName  string
	CommitterEmail string
}

func (s *workspaceSchema) saveWorkspace(ctx context.Context, source dagql.ObjectResult[*core.Workspace], args workspaceExportArgs) error {
	if !source.Self().IsValueWorkspace() {
		return fmt.Errorf("export with path requires a frozen source workspace; call snapshot first")
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return err
	}
	if args.From.Valid {
		from, err := args.From.Value.Load(ctx, srv)
		if err != nil {
			return err
		}
		if !from.Self().IsValueWorkspace() {
			return fmt.Errorf("export from requires a frozen workspace")
		}
		sourceID, err := source.ID()
		if err != nil {
			return err
		}
		fromID, err := from.ID()
		if err != nil {
			return err
		}
		same := sourceID.IsHandle() && fromID.IsHandle() && sourceID.EngineResultID() == fromID.EngineResultID()
		if !sourceID.IsHandle() && !fromID.IsHandle() {
			same = sourceID.Digest() == fromID.Digest()
		}
		if same {
			return nil
		}
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return err
	}
	bk, err := query.Engine(ctx)
	if err != nil {
		return err
	}
	// Capture only Git state, not currentWorkspace/module configuration. Never
	// route through the source's client. Unrelated untracked files stay private
	// on the host; the writer checks them for obstructions before changing files.
	var bundle []byte
	metadata, err := bk.CaptureGit(ctx, args.Path, &gitsession.CaptureGitPolicy{DropUntracked: true, MaxTotalBytes: 256 << 20}, func(kind gitsession.CaptureGitChunk_Kind, data []byte) error {
		if kind == gitsession.CAPTURE_CHUNK_BUNDLE {
			bundle = append(bundle, data...)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("capture export destination: %w", err)
	}
	if int64(len(bundle)) != metadata.BundleBytes {
		return fmt.Errorf("export destination bundle size mismatch")
	}
	captured := &core.Workspace{Cwd: "."}
	captured.SetHostPath(args.Path)
	captured.SetSource(core.NewWorkspaceSourceClientLocal(args.Path))
	destination, err := s.checkpointCapturedGitComposition(ctx, srv, captured, metadata, bundle, "")
	if err != nil {
		return err
	}
	id, err := destination.ID()
	if err != nil {
		return err
	}
	entries, err := bk.GetGitConfig(ctx, args.Path)
	if err != nil {
		return fmt.Errorf("read export destination Git config: %w", err)
	}
	name, email := "Dagger", "dagger@localhost"
	for _, entry := range entries {
		if entry.GetValue() == "" {
			continue
		}
		switch strings.ToLower(entry.GetKey()) {
		case "user.name":
			name = entry.GetValue()
		case "user.email":
			email = entry.GetValue()
		}
	}
	if err := validateWorkspaceGitAuthor(name, email); err != nil {
		return err
	}
	var repo dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, source, &repo, dagql.Selector{Field: "__saveDirectory", Args: []dagql.NamedInput{
		{Name: "destination", Value: dagql.NewID[*core.Workspace](id)},
		{Name: "from", Value: args.From},
		{Name: "committerName", Value: dagql.NewString(name)},
		{Name: "committerEmail", Value: dagql.NewString(email)},
	}}, dagql.Selector{Field: "asGit"}); err != nil {
		return err
	}
	var transport dagql.ObjectResult[*core.GitCommit]
	if err := srv.Select(ctx, repo, &transport, dagql.Selector{Field: "head"}, dagql.Selector{Field: "targetCommit"}); err != nil {
		return err
	}
	commit, err := transport.Self().Metadata(ctx)
	if err != nil {
		return err
	}
	if len(commit.ParentSHAs) != 2 {
		return fmt.Errorf("invalid workspace export transport")
	}
	return applyWorkspaceBundle(ctx, repo, metadata.HeadSha, commit.ParentSHAs[0], args.Path, metadata.CheckoutStateDigest)
}

func (s *workspaceSchema) saveDirectory(ctx context.Context, source dagql.ObjectResult[*core.Workspace], args workspaceSaveArgs) (dagql.ObjectResult[*core.Directory], error) {
	var result dagql.ObjectResult[*core.Directory]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return result, err
	}
	destination, err := args.Destination.Load(ctx, srv)
	if err != nil {
		return result, err
	}
	repo, err := workspaceGitCheckout(ctx, srv, destination)
	if err != nil {
		return result, err
	}
	inputs := func(ws dagql.ObjectResult[*core.Workspace]) (*core.GitRef, *core.Changeset, error) {
		if !ws.Self().IsValueWorkspace() {
			return nil, nil, fmt.Errorf("export requires frozen workspaces")
		}
		var head dagql.ObjectResult[*core.GitRef]
		if err := srv.Select(ctx, ws, &head, dagql.Selector{Field: "git"}, dagql.Selector{Field: "head"}); err != nil {
			return nil, nil, err
		}
		var dirty dagql.ObjectResult[*core.Changeset]
		if err := srv.Select(ctx, ws, &dirty, dagql.Selector{Field: "git"}, dagql.Selector{Field: "uncommitted"}); err != nil {
			return nil, nil, err
		}
		return head.Self(), dirty.Self(), nil
	}
	_, dirty, err := inputs(destination)
	if err != nil {
		return result, err
	}
	head, pending, err := inputs(source)
	if err != nil {
		return result, err
	}
	var from *core.GitRef
	var fromDirty *core.Changeset
	if args.From.Valid {
		ws, err := args.From.Value.Load(ctx, srv)
		if err != nil {
			return result, err
		}
		from, fromDirty, err = inputs(ws)
		if err != nil {
			return result, err
		}
	}
	dir, err := core.WorkspaceSaveDirectory(ctx, repo, dirty, head, pending, from, fromDirty, core.WorkspacePullOpts{MaxCommits: core.MaxWorkspacePullCommits, CommitterName: args.CommitterName, CommitterEmail: args.CommitterEmail})
	if err != nil {
		return result, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}

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
	repo, err := workspaceGitCheckout(ctx, srv, source)
	if err != nil {
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
	return applyWorkspaceBundle(ctx, repo, base.Self().Ref.SHA, head.Self().Ref.SHA, hostPath, source.Self().ExportStateDigest)
}

func applyWorkspaceBundle(ctx context.Context, repo dagql.ObjectResult[*core.GitRepository], baseSHA, targetSHA, hostPath, stateDigest string) error {
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
	var prerequisite dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, repo, &prerequisite, dagql.Selector{Field: "ref", Args: []dagql.NamedInput{{Name: "name", Value: dagql.NewString(baseSHA)}}}); err != nil {
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
		TargetSha:              targetSHA,
		ExpectedStateDigest:    stateDigest,
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
