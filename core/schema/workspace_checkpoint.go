package schema

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	gitsession "github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/dagger/dagger/util/gitutil"
)

type workspaceCheckpointArgs struct {
	Include dagql.Optional[dagql.ArrayInput[dagql.String]]
	Exclude dagql.Optional[dagql.ArrayInput[dagql.String]]

	MaxUntrackedFileBytes  dagql.Optional[dagql.Int]
	MaxUntrackedTotalBytes dagql.Optional[dagql.Int]
	MaxUntrackedFiles      dagql.Optional[dagql.Int]
}

type capturedCheckpointChunk struct {
	kind gitsession.CaptureGitChunk_Kind
	data []byte
}

func (s *workspaceSchema) checkpoint(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceCheckpointArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	ws := parent.Self()
	if ws == nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace checkpoint requires a workspace")
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	switch src := ws.Source().(type) {
	case *core.WorkspaceSourceClientLocal:
		return s.checkpointClientLocal(ctx, srv, parent, args)
	case *core.WorkspaceSourceRootlessLocal:
		return s.checkpointRootless(ctx, srv, parent)
	case *core.WorkspaceSourceDirectory:
		return parent, nil
	case *core.WorkspaceSourceGitRef:
		return s.checkpointGitRef(ctx, srv, parent, src, nil)
	case *core.WorkspaceSourceOverlay:
		// Rootless workspaces deliberately ignore their host path and accumulate
		// edits against an in-engine empty tree.
		switch base := src.Base.(type) {
		case *core.WorkspaceSourceDirectory:
			return parent, nil
		case *core.WorkspaceSourceGitRef:
			return s.checkpointGitRef(ctx, srv, parent, base, src)
		case *core.WorkspaceSourceClientLocal:
			frozen, err := s.checkpointClientLocal(ctx, srv, parent, args)
			if err != nil {
				return frozen, err
			}
			return checkpointOverlay(ctx, srv, frozen, src.Changes)
		case *core.WorkspaceSourceRootlessLocal:
			return s.checkpointRootless(ctx, srv, parent)
		default:
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace checkpoint cannot normalize overlay base %T", src.Base)
		}
	case nil:
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace checkpoint has no reconstructible source")
	default:
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace checkpoint does not support source %T", src)
	}
}

// checkpointRootless freezes the effective source tree of a context-only local
// workspace. Rootless workspaces intentionally never read HostPath: their
// pristine tree is empty, and functional edits accumulate as a full in-engine
// overlay. Rebuilding that tree through Directory.asWorkspace drops the client
// route and host path from the value while retaining portable workspace
// metadata.
func (s *workspaceSchema) checkpointRootless(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.Workspace],
) (inst dagql.ObjectResult[*core.Workspace], _ error) {
	ws := parent.Self()

	root, err := s.workspaceOverlayRootfs(ctx, ws)
	if err != nil {
		return inst, fmt.Errorf("resolve rootless workspace checkpoint tree: %w", err)
	}
	if err := srv.Select(ctx, root, &inst, dagql.Selector{
		Field: "asWorkspace",
		Args:  []dagql.NamedInput{{Name: "cwd", Value: dagql.NewString(ws.Cwd)}},
	}); err != nil {
		return inst, fmt.Errorf("construct rootless workspace checkpoint: %w", err)
	}

	workspaceEnv, _ := selectedWorkspaceEnv(ctx, ws)
	inst, err = checkpointWorkspaceMetadataComposition(ctx, srv, inst, ws, workspaceEnv)
	if err != nil {
		return inst, fmt.Errorf("compose rootless workspace checkpoint metadata: %w", err)
	}
	return inst, nil
}

func (s *workspaceSchema) checkpointClientLocal(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceCheckpointArgs,
) (inst dagql.ObjectResult[*core.Workspace], _ error) {
	ws := parent.Self()
	if ws.HostPath() == "" {
		return inst, fmt.Errorf("workspace checkpoint requires a local Git workspace")
	}

	caller, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("workspace checkpoint caller metadata: %w", err)
	}
	if ws.ClientID == "" || caller.ClientID != ws.ClientID {
		return inst, fmt.Errorf("workspace checkpoint capture is only available to the workspace's owning client")
	}

	clientCtx, err := s.withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return inst, err
	}
	query, err := core.CurrentQuery(clientCtx)
	if err != nil {
		return inst, err
	}
	bk, err := query.Engine(clientCtx)
	if err != nil {
		return inst, fmt.Errorf("workspace checkpoint engine client: %w", err)
	}

	policy := &gitsession.CaptureGitPolicy{
		Include:                checkpointPatterns(args.Include),
		Exclude:                checkpointPatterns(args.Exclude),
		MaxUntrackedFileBytes:  checkpointOptionalInt(args.MaxUntrackedFileBytes),
		MaxUntrackedTotalBytes: checkpointOptionalInt(args.MaxUntrackedTotalBytes),
		MaxUntrackedFiles:      int32(checkpointOptionalInt(args.MaxUntrackedFiles)),
		MaxTotalBytes:          256 << 20,
	}

	var chunks []capturedCheckpointChunk
	capture := func() (*gitsession.CaptureGitMetadata, error) {
		chunks = nil
		return bk.CaptureGit(clientCtx, ws.HostPath(), policy, func(kind gitsession.CaptureGitChunk_Kind, data []byte) error {
			chunks = append(chunks, capturedCheckpointChunk{kind: kind, data: slices.Clone(data)})
			return nil
		})
	}
	var metadata *gitsession.CaptureGitMetadata
	for {
		metadata, err = capture()
		var approvalErr *engineutil.GitCaptureApprovalError
		if !errors.As(err, &approvalErr) {
			break
		}
		if len(approvalErr.Candidates) == 0 {
			return inst, fmt.Errorf("workspace checkpoint approval required without selected paths")
		}
		summary := checkpointApprovalSummary(approvalErr.Candidates)
		approved, promptErr := checkpointPrompt(clientCtx, bk, summary)
		if promptErr != nil {
			return inst, fmt.Errorf("workspace checkpoint requires approval; pass include patterns in noninteractive use: %s: %w", summary, promptErr)
		}
		if !approved {
			return inst, fmt.Errorf("workspace checkpoint rejected selected dirty paths")
		}
		for _, candidate := range approvalErr.Candidates {
			if candidate.ApprovalToken == "" {
				return inst, fmt.Errorf("workspace checkpoint approval candidate %s has no state token", strconv.Quote(candidate.Path))
			}
			policy.ApprovalTokens = append(policy.ApprovalTokens, candidate.ApprovalToken)
		}
	}
	if err != nil {
		return inst, fmt.Errorf("capture workspace checkpoint: %w", err)
	}

	bundle := slices.Concat(checkpointBundleChunks(chunks)...)
	if int64(len(bundle)) != metadata.BundleBytes {
		return inst, fmt.Errorf("workspace checkpoint bundle is %d bytes, capture reported %d", len(bundle), metadata.BundleBytes)
	}
	workspaceEnv, _ := selectedWorkspaceEnv(clientCtx, ws)
	inst, err = s.checkpointCapturedGitComposition(clientCtx, srv, ws, metadata, bundle, workspaceEnv)
	if err != nil {
		return inst, fmt.Errorf("construct portable workspace checkpoint: %w", err)
	}

	return inst, nil
}

func (s *workspaceSchema) checkpointGitRef(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.Workspace],
	source *core.WorkspaceSourceGitRef,
	overlay *core.WorkspaceSourceOverlay,
) (inst dagql.ObjectResult[*core.Workspace], _ error) {
	ws := parent.Self()
	ref := source.Ref.Self()
	if ref == nil || ref.Ref == nil || ref.Ref.SHA == "" || ref.Repo.Self() == nil {
		return inst, fmt.Errorf("workspace checkpoint Git source has no resolved commit")
	}

	var pinned dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, ref.Repo, &pinned, dagql.Selector{
		Field: "ref",
		Args:  []dagql.NamedInput{{Name: "name", Value: dagql.NewString(ref.Ref.SHA)}},
	}); err != nil {
		return inst, fmt.Errorf("pin workspace Git ref at %s: %w", ref.Ref.SHA, err)
	}
	if err := srv.Select(ctx, pinned, &inst, dagql.Selector{
		Field: "asWorkspace",
		Args:  []dagql.NamedInput{{Name: "cwd", Value: dagql.NewString(ws.Cwd)}},
	}); err != nil {
		return inst, fmt.Errorf("normalize pinned Git workspace: %w", err)
	}

	if overlay != nil && overlay.Changes.Self() != nil {
		var err error
		inst, err = checkpointOverlay(ctx, srv, inst, overlay.Changes)
		if err != nil {
			return inst, fmt.Errorf("reapply workspace Git overlay: %w", err)
		}
	}

	workspaceEnv, _ := selectedWorkspaceEnv(ctx, ws)
	return checkpointWorkspaceMetadataComposition(ctx, srv, inst, ws, workspaceEnv)
}

func (s *workspaceSchema) checkpointCapturedGitComposition(
	ctx context.Context,
	srv *dagql.Server,
	captured *core.Workspace,
	metadata *gitsession.CaptureGitMetadata,
	bundleBytes []byte,
	workspaceEnv string,
) (inst dagql.ObjectResult[*core.Workspace], _ error) {
	if metadata == nil {
		return inst, fmt.Errorf("workspace checkpoint capture metadata is missing")
	}

	var repo dagql.ObjectResult[*core.GitRepository]
	prerequisiteRef := metadata.RemoteRef
	// A local filesystem remote is available only to the capturing client,
	// just like a repository with no remote at all.
	_, remoteErr := gitutil.ParseURL(metadata.RemoteUrl)
	if metadata.RemoteUrl == "" || remoteErr != nil {
		prerequisiteRef = ""
		var gitDir dagql.ObjectResult[*core.Directory]
		if err := srv.Select(ctx, srv.Root(), &gitDir,
			dagql.Selector{Field: "host"},
			dagql.Selector{Field: "__gitDir", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(captured.HostPath())},
				{Name: "stateDigest", Value: dagql.NewString(metadata.CheckoutStateDigest)},
				{Name: "validateState", Value: dagql.NewBoolean(true)},
			}},
		); err != nil {
			return inst, fmt.Errorf("reconstruct session checkpoint: %w", err)
		}
		// __gitDir returns the .git contents. asGit accepts a bare repository.
		if err := srv.Select(ctx, gitDir, &repo, dagql.Selector{Field: "asGit"}); err != nil {
			return inst, err
		}
	} else if err := srv.Select(ctx, srv.Root(), &repo, dagql.Selector{
		Field: "git",
		Args:  []dagql.NamedInput{{Name: "url", Value: dagql.NewString(metadata.RemoteUrl)}},
	}); err != nil {
		return inst, fmt.Errorf("load workspace checkpoint remote: %w", err)
	}

	if len(bundleBytes) > 0 {
		var file dagql.ObjectResult[*core.File]
		if err := srv.Select(ctx, srv.Root(), &file, dagql.Selector{
			Field: "blob",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.NewString("workspace-checkpoint.bundle")},
				{Name: "contents", Value: dagql.Bytes(bundleBytes)},
				{Name: "permissions", Value: dagql.NewInt(0o600)},
			},
		}); err != nil {
			return inst, fmt.Errorf("embed workspace checkpoint bundle: %w", err)
		}
		var bundle dagql.ObjectResult[*core.GitBundle]
		if err := srv.Select(ctx, file, &bundle, dagql.Selector{Field: "asGitBundle"}); err != nil {
			return inst, fmt.Errorf("parse workspace checkpoint bundle: %w", err)
		}
		bundleID, err := bundle.ID()
		if err != nil {
			return inst, fmt.Errorf("workspace checkpoint bundle identity: %w", err)
		}
		var imported dagql.ObjectResult[*core.GitRepository]
		if err := srv.Select(ctx, repo, &imported, dagql.Selector{
			Field: "withBundle",
			Args: []dagql.NamedInput{
				{Name: "bundle", Value: dagql.NewID[*core.GitBundle](bundleID)},
				{Name: "prerequisiteRef", Value: dagql.NewString(prerequisiteRef)},
			},
		}); err != nil {
			return inst, fmt.Errorf("import workspace checkpoint bundle: %w", err)
		}
		repo = imported
	}

	var head dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, repo, &head, dagql.Selector{
		Field: "ref",
		Args:  []dagql.NamedInput{{Name: "name", Value: dagql.NewString(metadata.HeadSha)}},
	}); err != nil {
		return inst, fmt.Errorf("select workspace checkpoint HEAD %s: %w", metadata.HeadSha, err)
	}
	if err := srv.Select(ctx, head, &inst, dagql.Selector{
		Field: "asWorkspace",
		Args:  []dagql.NamedInput{{Name: "cwd", Value: dagql.NewString(captured.Cwd)}},
	}); err != nil {
		return inst, fmt.Errorf("construct workspace checkpoint from HEAD: %w", err)
	}

	if metadata.WorktreeSha != "" {
		treeArgs := []dagql.NamedInput{
			{Name: "discardGitDir", Value: dagql.NewBoolean(true)},
			{Name: "depth", Value: dagql.NewInt(0)},
			{Name: "includeTags", Value: dagql.NewBoolean(false)},
		}
		var headTree dagql.ObjectResult[*core.Directory]
		if err := srv.Select(ctx, head, &headTree, dagql.Selector{Field: "tree", Args: treeArgs}); err != nil {
			return inst, fmt.Errorf("workspace checkpoint HEAD tree: %w", err)
		}
		var worktree dagql.ObjectResult[*core.GitRef]
		if err := srv.Select(ctx, repo, &worktree, dagql.Selector{
			Field: "ref",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.NewString(metadata.WorktreeSha)}},
		}); err != nil {
			return inst, fmt.Errorf("select workspace checkpoint worktree %s: %w", metadata.WorktreeSha, err)
		}
		var worktreeTree dagql.ObjectResult[*core.Directory]
		if err := srv.Select(ctx, worktree, &worktreeTree, dagql.Selector{Field: "tree", Args: treeArgs}); err != nil {
			return inst, fmt.Errorf("workspace checkpoint worktree tree: %w", err)
		}
		headTreeID, err := headTree.ID()
		if err != nil {
			return inst, fmt.Errorf("workspace checkpoint HEAD tree identity: %w", err)
		}
		var changes dagql.ObjectResult[*core.Changeset]
		if err := srv.Select(ctx, worktreeTree, &changes, dagql.Selector{
			Field: "changes",
			Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](headTreeID)}},
		}); err != nil {
			return inst, fmt.Errorf("workspace checkpoint worktree changes: %w", err)
		}
		changesID, err := changes.ID()
		if err != nil {
			return inst, fmt.Errorf("workspace checkpoint changes identity: %w", err)
		}
		var withChanges dagql.ObjectResult[*core.Workspace]
		if err := srv.Select(ctx, inst, &withChanges, dagql.Selector{
			Field: "withChanges",
			Args:  []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)}},
		}); err != nil {
			return inst, fmt.Errorf("apply workspace checkpoint worktree: %w", err)
		}
		inst = withChanges
	}

	return checkpointWorkspaceMetadataComposition(ctx, srv, inst, captured, workspaceEnv)
}

func checkpointWorkspaceMetadataComposition(
	ctx context.Context,
	srv *dagql.Server,
	workspaceResult dagql.ObjectResult[*core.Workspace],
	metadata *core.Workspace,
	workspaceEnv string,
) (inst dagql.ObjectResult[*core.Workspace], _ error) {
	if err := srv.Select(ctx, workspaceResult, &inst, dagql.Selector{
		Field: "withConfigPaths",
		Args: []dagql.NamedInput{
			{Name: "configFile", Value: dagql.NewString(metadata.ConfigFile)},
			{Name: "lockFile", Value: dagql.NewString(metadata.LockFile)},
		},
	}); err != nil {
		return inst, err
	}
	var withEnv dagql.ObjectResult[*core.Workspace]
	if err := srv.Select(ctx, inst, &withEnv, dagql.Selector{
		Field: "withConfigEnvironment",
		Args:  []dagql.NamedInput{{Name: "name", Value: dagql.NewString(workspaceEnv)}},
	}); err != nil {
		return inst, err
	}
	inst = withEnv
	if mounts, ok := metadata.MountsDir(); ok {
		for _, mountPath := range metadata.MountPoints() {
			stat, err := mounts.Self().Stat(ctx, mounts, srv, mountPath, true)
			if err != nil {
				return inst, err
			}
			field := "withMountedFile"
			var source dagql.Input
			if stat.IsDir() {
				var dir dagql.ObjectResult[*core.Directory]
				if err := srv.Select(ctx, mounts, &dir, dagql.Selector{Field: "directory", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString(mountPath)}}}); err != nil {
					return inst, err
				}
				id, err := dir.ID()
				if err != nil {
					return inst, err
				}
				source = dagql.NewID[*core.Directory](id)
				field = "withMountedDirectory"
			} else {
				var file dagql.ObjectResult[*core.File]
				if err := srv.Select(ctx, mounts, &file, dagql.Selector{Field: "file", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString(mountPath)}}}); err != nil {
					return inst, err
				}
				id, err := file.ID()
				if err != nil {
					return inst, err
				}
				source = dagql.NewID[*core.File](id)
			}
			var mounted dagql.ObjectResult[*core.Workspace]
			if err := srv.Select(ctx, inst, &mounted, dagql.Selector{
				Field: field, Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.NewString("/" + mountPath)},
					{Name: "source", Value: source},
				},
			}); err != nil {
				return inst, err
			}
			inst = mounted
		}
	}
	return inst, nil
}

func checkpointApprovalSummary(candidates []*gitsession.CaptureGitCandidate) string {
	var summary strings.Builder
	summary.WriteString("Include these workspace changes in the checkpoint?\n")
	for _, candidate := range candidates {
		kind := "untracked"
		if candidate.Tracked {
			kind = "tracked"
		}
		fmt.Fprintf(&summary, "\n- %s (%s, %d bytes; state %s", strconv.Quote(candidate.Path), kind, candidate.Bytes, candidate.ApprovalToken)
		if candidate.Classification != "" {
			fmt.Fprintf(&summary, "; warning: %s", candidate.Classification)
		}
		summary.WriteString(")")
	}
	return summary.String()
}

func checkpointPatterns(arg dagql.Optional[dagql.ArrayInput[dagql.String]]) []string {
	if !arg.Valid {
		return nil
	}
	patterns := make([]string, len(arg.Value))
	for i, pattern := range arg.Value {
		patterns[i] = pattern.String()
	}
	return patterns
}

// checkpointOverlay records only the already-approved engine edits as a patch.
// Keeping the old Changeset ID would retain the live host receiver in the recipe.
func checkpointOverlay(
	ctx context.Context,
	srv *dagql.Server,
	frozen dagql.ObjectResult[*core.Workspace],
	changes dagql.ObjectResult[*core.Changeset],
) (out dagql.ObjectResult[*core.Workspace], err error) {
	var patch dagql.ObjectResult[*core.File]
	if err := srv.Select(ctx, changes, &patch, dagql.Selector{Field: "asPatch"}); err != nil {
		return out, err
	}
	data, err := patch.Self().Contents(ctx, patch, nil, nil)
	if err != nil {
		return out, err
	}
	if len(data) == 0 {
		return frozen, nil
	}
	var blob dagql.ObjectResult[*core.File]
	if err := srv.Select(ctx, srv.Root(), &blob, dagql.Selector{
		Field: "blob", Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString("workspace-overlay.patch")},
			{Name: "contents", Value: dagql.Bytes(data)},
			{Name: "permissions", Value: dagql.NewInt(0o600)},
		},
	}); err != nil {
		return out, err
	}
	blobID, err := blob.ID()
	if err != nil {
		return out, err
	}
	var before dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, frozen, &before, dagql.Selector{
		Field: "directory", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString("/")}},
	}); err != nil {
		return out, err
	}
	beforeID, err := before.ID()
	if err != nil {
		return out, err
	}
	var delta dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, before, &delta,
		dagql.Selector{Field: "withPatchFile", Args: []dagql.NamedInput{{Name: "patch", Value: dagql.NewID[*core.File](blobID)}}},
		dagql.Selector{Field: "changes", Args: []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](beforeID)}}},
	); err != nil {
		return out, fmt.Errorf("apply workspace overlay to checkpoint: %w", err)
	}
	deltaID, err := delta.ID()
	if err != nil {
		return out, err
	}
	err = srv.Select(ctx, frozen, &out, dagql.Selector{
		Field: "withChanges", Args: []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](deltaID)}},
	})
	return out, err
}

func checkpointOptionalInt(arg dagql.Optional[dagql.Int]) int64 {
	if !arg.Valid {
		return 0
	}
	return int64(arg.Value)
}

func checkpointBundleChunks(chunks []capturedCheckpointChunk) (bundle [][]byte) {
	const traceChunkBytes = 256 << 10
	for _, chunk := range chunks {
		if chunk.kind != gitsession.CAPTURE_CHUNK_BUNDLE {
			continue
		}
		data := chunk.data
		for len(data) > 0 {
			n := min(len(data), traceChunkBytes)
			bundle = append(bundle, slices.Clone(data[:n]))
			data = data[n:]
		}
	}
	return bundle
}

func (s *workspaceSchema) withConfigPaths(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args struct {
		ConfigFile string
		LockFile   string
	},
) (dagql.ObjectResult[*core.Workspace], error) {
	ws, err := workspaceWithConfigPaths(parent.Self(), args.ConfigFile, args.LockFile)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ws)
}

func workspaceWithConfigPaths(parent *core.Workspace, configFile, lockFile string) (*core.Workspace, error) {
	if err := validateWorkspaceMetadataPath("config file", configFile); err != nil {
		return nil, err
	}
	if err := validateWorkspaceMetadataPath("lock file", lockFile); err != nil {
		return nil, err
	}
	ws := parent.Clone()
	ws.ConfigFile = configFile
	ws.LockFile = lockFile
	return ws, nil
}

func validateWorkspaceMetadataPath(label, value string) error {
	if value == "" {
		return nil
	}
	clean := path.Clean(value)
	if path.IsAbs(value) || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("workspace %s path %q must be inside the workspace root", label, value)
	}
	if clean != value {
		return fmt.Errorf("workspace %s path %q must be canonical", label, value)
	}
	return nil
}

func (s *workspaceSchema) withConfigEnvironment(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args struct{ Name string },
) (dagql.ObjectResult[*core.Workspace], error) {
	ws := workspaceWithConfigEnvironment(parent.Self(), args.Name)
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, ws)
}

func workspaceWithConfigEnvironment(parent *core.Workspace, name string) *core.Workspace {
	ws := parent.Clone()
	ws.SetSelectedEnv(name)
	return ws
}

func checkpointPrompt(ctx context.Context, bk *engineutil.Client, summary string) (bool, error) {
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return false, err
	}
	caller, err := bk.GetHostServiceCaller(ctx, md.ClientID)
	if err != nil {
		return false, err
	}
	response, err := prompt.NewPromptClient(caller.Conn()).PromptBool(ctx, &prompt.BoolRequest{
		Title: "Include workspace changes?", Prompt: summary,
	})
	if err != nil {
		return false, err
	}
	return response.Response, nil
}

func (s *workspaceSchema) portable(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], _ struct{}) (dagql.Boolean, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return false, err
	}
	recipe, err := parent.RecipeID(ctx)
	if err != nil {
		return false, err
	}
	return dagql.Boolean(srv.ClassifyRecipe(recipe).NotReplayable == nil), nil
}
