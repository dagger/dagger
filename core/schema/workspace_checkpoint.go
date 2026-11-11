package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
	gitsession "github.com/dagger/dagger/engine/session/git"
	"github.com/dagger/dagger/engine/session/prompt"
	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// snapshot captures a live workspace when possible and returns the resulting value.
// Capture is explicit: returning a Workspace does not opt it into Syncer.
// An existing snapshot never refreshes the original checkout.
func (s *workspaceSchema) snapshot(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], _ struct{}) (dagql.ObjectResult[*core.Workspace], error) {
	frozen := parent
	var err error
	var rootless bool
	if ws := parent.Self(); ws != nil {
		_, rootless = ws.BaseSource().(*core.WorkspaceSourceRootlessLocal)
	}
	if !rootless {
		frozen, err = s.freeze(ctx, parent)
	}
	if errors.Is(err, gitutil.ErrGitNoRepo) || errors.Is(err, engineutil.ErrGitCaptureUnsupported) {
		// Capture is an enhancement to snapshot, not a prerequisite for using a
		// workspace. Keep the original value, including any overlays and client
		// context, when there is no capturable Git baseline. Git mutations call
		// freeze directly and still require capture to succeed.
		frozen, err = parent, nil
	}
	return frozen, err
}

func (s *workspaceSchema) freeze(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
) (dagql.ObjectResult[*core.Workspace], error) {
	ws := parent.Self()
	if ws == nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace snapshot requires a workspace")
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	switch src := ws.Source().(type) {
	case *core.WorkspaceSourceClientLocal:
		return s.checkpointClientLocal(ctx, srv, parent)
	case *core.WorkspaceSourceRootlessLocal:
		return s.checkpointRootless(ctx, srv, parent)
	case *core.WorkspaceSourceDirectory:
		return parent, nil
	case *core.WorkspaceSourceGitRef:
		return s.checkpointGitRef(ctx, srv, parent, src, nil)
	case *core.WorkspaceSourceOverlay:
		switch base := src.Base.(type) {
		case *core.WorkspaceSourceDirectory:
			return parent, nil
		case *core.WorkspaceSourceGitRef:
			return s.checkpointGitRef(ctx, srv, parent, base, src)
		case *core.WorkspaceSourceClientLocal:
			frozen, err := s.checkpointClientLocal(ctx, srv, parent)
			if err != nil {
				return frozen, err
			}
			return checkpointOverlay(ctx, srv, frozen, src.Changes)
		case *core.WorkspaceSourceRootlessLocal:
			// Rootless workspaces deliberately ignore their host path and
			// accumulate edits against an in-engine empty tree.
			return s.checkpointRootless(ctx, srv, parent)
		default:
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace snapshot cannot normalize overlay base %T", src.Base)
		}
	case nil:
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace snapshot has no reconstructible source")
	default:
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace snapshot does not support source %T", src)
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
		return inst, fmt.Errorf("resolve rootless workspace snapshot tree: %w", err)
	}
	if err := srv.Select(ctx, root, &inst, dagql.Selector{
		Field: "asWorkspace",
		Args:  []dagql.NamedInput{{Name: "cwd", Value: dagql.NewString(ws.Cwd)}},
	}); err != nil {
		return inst, fmt.Errorf("construct rootless workspace snapshot: %w", err)
	}

	workspaceEnv, _ := selectedWorkspaceEnv(ctx, ws)
	inst, err = checkpointWorkspaceMetadataComposition(ctx, srv, inst, ws, workspaceEnv)
	if err != nil {
		return inst, fmt.Errorf("compose rootless workspace snapshot metadata: %w", err)
	}
	return inst, nil
}

func (s *workspaceSchema) checkpointClientLocal(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.Workspace],
) (inst dagql.ObjectResult[*core.Workspace], _ error) {
	ws := parent.Self()
	if ws.HostPath() == "" {
		return inst, fmt.Errorf("workspace snapshot requires a local Git workspace")
	}

	caller, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("workspace snapshot caller metadata: %w", err)
	}
	if ws.ClientID == "" || caller.ClientID != ws.ClientID {
		return inst, fmt.Errorf("workspace snapshot capture is only available to the workspace's owning client")
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
		return inst, fmt.Errorf("workspace snapshot engine client: %w", err)
	}

	// Zero limits use the capture service defaults (16 MiB per untracked file,
	// 64 MiB total, 4096 files). Untracked paths are approved interactively.
	policy := &gitsession.CaptureGitPolicy{MaxTotalBytes: 256 << 20}

	var bundle []byte
	capture := func() (*gitsession.CaptureGitMetadata, error) {
		// Each attempt (an approval retry included) streams the whole bundle.
		bundle = nil
		return bk.CaptureGit(clientCtx, ws.HostPath(), policy, core.MaxCapturedGitBundleBytes, func(_ gitsession.CaptureGitChunk_Kind, data []byte) error {
			bundle = append(bundle, data...)
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
		if policy.DropUntracked {
			return inst, fmt.Errorf("client did not honor dropping untracked files; upgrade the dagger CLI")
		}
		if len(approvalErr.Candidates) == 0 {
			return inst, fmt.Errorf("workspace snapshot approval required without selected paths")
		}
		summary := checkpointApprovalSummary(approvalErr.Candidates)
		choice, promptErr := checkpointPrompt(clientCtx, bk, summary)
		if promptErr != nil {
			return inst, fmt.Errorf("workspace snapshot requires interactive approval for untracked files: %s: %w", summary, promptErr)
		}
		if err := applyCheckpointDecision(policy, approvalErr.Candidates, choice); err != nil {
			return inst, err
		}
	}
	if err != nil {
		return inst, fmt.Errorf("capture workspace snapshot: %w", err)
	}
	if policy.DropUntracked && metadata.UntrackedFiles != 0 {
		return inst, fmt.Errorf("client included untracked files despite Drop; upgrade the dagger CLI")
	}

	clientCtx, err = withCapturedCheckoutSSHAuth(clientCtx, bk, caller, metadata.RemoteUrl)
	if err != nil {
		return inst, fmt.Errorf("prepare workspace snapshot SSH authentication: %w", err)
	}

	workspaceEnv, _ := selectedWorkspaceEnv(clientCtx, ws)
	inst, err = s.checkpointCapturedGitComposition(clientCtx, srv, ws, metadata, bundle, workspaceEnv)
	if err != nil {
		return inst, fmt.Errorf("construct portable workspace snapshot: %w", err)
	}
	// A successful owning-client capture approves this checkout as a donor of
	// the captured anchor's history; later ref moves do not change its closure.
	// Retain only an optional session-local donor capability, not host objects
	// or client routes in the portable recipe. Bundle-backed (dirty/unpushed)
	// captures retain their existing local repository path; only a clean remote
	// base can donate complete history lazily, when history is demanded. The
	// donor is only an optimization: registering none never fails the snapshot.
	registerCheckpointHostHistory(clientCtx, query, ws, inst.Self(), metadata, len(bundle) != 0)

	return inst, nil
}

// withCapturedCheckoutSSHAuth prepares SSH authentication for reconstructing
// the SSH origin of a checkout the caller just captured. Capturing the owning
// client's checkout also authorizes reconstructing its SSH origin. Reuse push's
// lazy host key discovery when no agent is running; ordinary Git reads must not
// start agents or unlock the owner's keys. Composition binds the prepared agent
// as a session socket scoped to its SSH identities, and the resulting recipe
// references it for the rest of the session, just as it would an agent from
// the owner's SSH_AUTH_SOCK. Non-SSH origins, and callers that already have an
// agent, keep ctx as is.
func withCapturedCheckoutSSHAuth(ctx context.Context, bk *engineutil.Client, caller *engine.ClientMetadata, remoteURL string) (context.Context, error) {
	if caller.SSHAuthSocketPath != "" {
		return ctx, nil
	}
	if remote, err := gitutil.ParseURL(remoteURL); err != nil || remote.Scheme != gitutil.SSHProtocol {
		return ctx, nil //nolint:nilerr // not an SSH origin, so no agent to prepare
	}
	socketPath, err := bk.PrepareGitSSHAuth(ctx, remoteURL)
	if err != nil {
		return ctx, err
	}
	prepared := *caller
	prepared.SSHAuthSocketPath = socketPath
	return engine.ContextWithClientMetadata(ctx, &prepared), nil
}

// registerCheckpointHostHistory reports whether the capture registered an
// approved host history donor. It never fails; why it registered none is
// recorded on the current span.
func registerCheckpointHostHistory(ctx context.Context, query *core.Query, captured, frozen *core.Workspace, metadata *gitsession.CaptureGitMetadata, hasBundle bool) bool {
	if hasBundle {
		return core.SkipHostHistoryDonor(ctx, "bundle-backed capture", nil)
	}
	if metadata.RemoteUrl == "" {
		return core.SkipHostHistoryDonor(ctx, "capture has no remote URL", nil)
	}
	source, ok := frozen.BaseSource().(*core.WorkspaceSourceGitRef)
	if !ok || source.Ref.Self() == nil {
		return core.SkipHostHistoryDonor(ctx, "snapshot base is not a Git ref", nil)
	}
	return query.RegisterCapturedHostHistory(ctx, source.Ref.Self().Repo, captured.ClientID, captured.HostPath(), metadata.BaseSha, metadata.RemoteUrl)
}

// checkpointGitRef freezes a Git workspace: its base becomes a detached
// commit, and its overlay is kept by reference.
//
// The commit is already fixed: Git refs record their resolved commit
// (__resolvedRef) when selected, so even a workspace on a branch never moves
// with it. But a named ref still selects that name, and committing onto it
// advances the name in the committed repository's own storage, where it would
// then shadow the upstream's (see LocalGitRepository.Upstream). So a named ref
// is re-selected by its SHA, detaching it. A ref already selected by SHA, from
// a repository whose remote selection is fixed, is kept as it is.
//
// A local checkout whose remote selection was never recorded reads its branch
// tracking from its current branch, which detaching HEAD loses. Its selection
// is recorded first, so the frozen repository, and checkouts derived from it,
// still name their upstream remote.
//
// Rebuilding the base reapplies the overlay onto it by reference, never as a
// rendered patch: tool edits are already patch blobs, and MCP deliberately
// keeps binary and oversized outputs out of the recipe as raw changesets (see
// MCP.applyChangeset). Edits made through the API stay the recipes that made
// them.
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
		return inst, fmt.Errorf("workspace snapshot Git source has no resolved commit")
	}

	repo := ref.Repo
	_, local := repo.Self().Backend.(*core.LocalGitRepository)
	recordSelection := local && repo.Self().UpstreamRemote == nil
	// The SHA resolvers keep the resolved SHA as the name: still detached.
	detached := ref.Ref.Name == "" || ref.Ref.Name == ref.Ref.SHA
	if detached && !recordSelection {
		return parent, nil
	}
	if recordSelection {
		remotes, upstream, err := repo.Self().ConfiguredRemotes(ctx)
		if err != nil {
			return inst, fmt.Errorf("read workspace Git remotes: %w", err)
		}
		data, err := json.Marshal(remotes)
		if err != nil {
			return inst, err
		}
		if err := srv.Select(ctx, repo, &repo, dagql.Selector{
			Field: "__withRemoteSelection",
			Args: []dagql.NamedInput{
				{Name: "remotes", Value: dagql.String(data)},
				{Name: "upstreamRemote", Value: dagql.String(upstream)},
			},
		}); err != nil {
			return inst, fmt.Errorf("record workspace Git remotes: %w", err)
		}
	}
	var pinned dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, repo, &pinned, dagql.Selector{
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
		changesID, err := overlay.Changes.ID()
		if err != nil {
			return inst, err
		}
		if err := srv.Select(ctx, inst, &inst, dagql.Selector{
			Field: "withChanges",
			Args:  []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)}},
		}); err != nil {
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
	return s.checkpointCapturedGitCompositionWithBase(ctx, srv, captured, metadata, bundleBytes, workspaceEnv, dagql.ObjectResult[*core.GitRepository]{})
}

// Only export supplies an already-owned base. Snapshot and all other capture
// callers retain their original reconstruction and client-routing behavior.
func (s *workspaceSchema) checkpointCapturedGitCompositionWithBase(
	ctx context.Context,
	srv *dagql.Server,
	captured *core.Workspace,
	metadata *gitsession.CaptureGitMetadata,
	bundleBytes []byte,
	workspaceEnv string,
	repo dagql.ObjectResult[*core.GitRepository],
) (inst dagql.ObjectResult[*core.Workspace], _ error) {
	if metadata == nil {
		return inst, fmt.Errorf("workspace snapshot capture metadata is missing")
	}

	// Sequential sibling phases partition composition, including lazy dagql
	// evaluation triggered by each selection. Keep arguments and errors out of
	// these diagnostic spans: captured paths and contents can be private.
	compositionCtx := ctx
	phaseName := "checkpoint reconstruct repository"
	if repo.Self() != nil {
		phaseName = "checkpoint reuse owned repository"
	}
	ctx, phase := core.Tracer(ctx).Start(ctx, phaseName, telemetry.Internal())
	nextPhase := func(name string) {
		phase.End()
		ctx, phase = core.Tracer(compositionCtx).Start(compositionCtx, name, telemetry.Internal())
	}
	defer func() { phase.End() }()

	prerequisiteRef := metadata.RemoteRef
	// A local filesystem remote is available only to the capturing client,
	// just like a repository with no remote at all.
	remote, remoteErr := gitutil.ParseURL(metadata.RemoteUrl)
	if repo.Self() != nil {
		// The caller proved this immutable local repository owns the captured
		// HEAD. Import still isolates and validates the exact prerequisites;
		// source refs (including a remote-ref hint) are not destination refs.
		prerequisiteRef = ""
	} else if metadata.RemoteUrl == "" || remoteErr != nil {
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
	} else {
		args := []dagql.NamedInput{{Name: "url", Value: dagql.NewString(metadata.RemoteUrl)}}
		if remote.Scheme == gitutil.SSHProtocol {
			sshArgs, err := checkpointSSHAuthArgs(ctx, srv)
			if err != nil {
				return inst, err
			}
			args = append(args, sshArgs...)
		}
		if err := srv.Select(ctx, srv.Root(), &repo, dagql.Selector{Field: "git", Args: args}); err != nil {
			return inst, fmt.Errorf("load workspace snapshot remote: %w", err)
		}
	}

	if len(bundleBytes) > 0 {
		nextPhase("checkpoint import captured bundle")
		var file dagql.ObjectResult[*core.File]
		if err := srv.Select(ctx, srv.Root(), &file, dagql.Selector{
			Field: "blob",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.NewString("workspace-checkpoint.bundle")},
				{Name: "contents", Value: dagql.Bytes(bundleBytes)},
				{Name: "permissions", Value: dagql.NewInt(0o600)},
			},
		}); err != nil {
			return inst, fmt.Errorf("embed workspace snapshot bundle: %w", err)
		}
		var bundle dagql.ObjectResult[*core.GitBundle]
		if err := srv.Select(ctx, file, &bundle, dagql.Selector{Field: "asGitBundle"}); err != nil {
			return inst, fmt.Errorf("parse workspace snapshot bundle: %w", err)
		}
		bundleID, err := bundle.ID()
		if err != nil {
			return inst, fmt.Errorf("workspace snapshot bundle identity: %w", err)
		}
		var imported dagql.ObjectResult[*core.GitRepository]
		if err := srv.Select(ctx, repo, &imported, dagql.Selector{
			Field: "withBundle",
			Args: []dagql.NamedInput{
				{Name: "bundle", Value: dagql.NewID[*core.GitBundle](bundleID)},
				{Name: "prerequisiteRef", Value: dagql.NewString(prerequisiteRef)},
			},
		}); err != nil {
			return inst, fmt.Errorf("import workspace snapshot bundle: %w", err)
		}
		repo = imported
	}

	nextPhase("checkpoint construct HEAD workspace")
	var err error
	repo, err = s.checkpointCapturedGitRemotes(ctx, srv, repo, metadata)
	if err != nil {
		return inst, err
	}

	var head dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, repo, &head, dagql.Selector{
		Field: "ref",
		Args:  []dagql.NamedInput{{Name: "name", Value: dagql.NewString(metadata.HeadSha)}},
	}); err != nil {
		return inst, fmt.Errorf("select workspace snapshot HEAD %s: %w", metadata.HeadSha, err)
	}
	if err := srv.Select(ctx, head, &inst, dagql.Selector{
		Field: "asWorkspace",
		Args:  []dagql.NamedInput{{Name: "cwd", Value: dagql.NewString(captured.Cwd)}},
	}); err != nil {
		return inst, fmt.Errorf("construct workspace snapshot from HEAD: %w", err)
	}

	if metadata.WorktreeSha != "" {
		// These are content-only views: history is discarded before comparing
		// trees. Keep the backing repository (and hence Workspace Git history)
		// complete, but do not fetch that history into two throwaway checkouts.
		treeArgs := []dagql.NamedInput{
			{Name: "discardGitDir", Value: dagql.NewBoolean(true)},
			{Name: "depth", Value: dagql.NewInt(1)},
			{Name: "includeTags", Value: dagql.NewBoolean(false)},
		}
		var headTree dagql.ObjectResult[*core.Directory]
		nextPhase("checkpoint reconstruct HEAD tree")
		if err := srv.Select(ctx, head, &headTree, dagql.Selector{Field: "tree", Args: treeArgs}); err != nil {
			return inst, fmt.Errorf("workspace snapshot HEAD tree: %w", err)
		}
		nextPhase("checkpoint reconstruct worktree tree")
		var worktree dagql.ObjectResult[*core.GitRef]
		if err := srv.Select(ctx, repo, &worktree, dagql.Selector{
			Field: "ref",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.NewString(metadata.WorktreeSha)}},
		}); err != nil {
			return inst, fmt.Errorf("select workspace snapshot worktree %s: %w", metadata.WorktreeSha, err)
		}
		var worktreeTree dagql.ObjectResult[*core.Directory]
		if err := srv.Select(ctx, worktree, &worktreeTree, dagql.Selector{Field: "tree", Args: treeArgs}); err != nil {
			return inst, fmt.Errorf("workspace snapshot worktree tree: %w", err)
		}
		nextPhase("checkpoint compose worktree changes")
		headTreeID, err := headTree.ID()
		if err != nil {
			return inst, fmt.Errorf("workspace snapshot HEAD tree identity: %w", err)
		}
		var changes dagql.ObjectResult[*core.Changeset]
		if err := srv.Select(ctx, worktreeTree, &changes, dagql.Selector{
			Field: "changes",
			Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](headTreeID)}},
		}); err != nil {
			return inst, fmt.Errorf("workspace snapshot worktree changes: %w", err)
		}
		changesID, err := changes.ID()
		if err != nil {
			return inst, fmt.Errorf("workspace snapshot changes identity: %w", err)
		}
		var withChanges dagql.ObjectResult[*core.Workspace]
		if err := srv.Select(ctx, inst, &withChanges, dagql.Selector{
			Field: "withChanges",
			Args:  []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)}},
		}); err != nil {
			return inst, fmt.Errorf("apply workspace snapshot worktree: %w", err)
		}
		inst = withChanges
	}

	nextPhase("checkpoint compose metadata")
	return checkpointWorkspaceMetadataComposition(ctx, srv, inst, captured, workspaceEnv)
}

// checkpointCapturedGitRemotes records captured routing, including the legacy
// origin push destination when an older client omits named-remote metadata.
func (s *workspaceSchema) checkpointCapturedGitRemotes(
	ctx context.Context,
	srv *dagql.Server,
	repo dagql.ObjectResult[*core.GitRepository],
	metadata *gitsession.CaptureGitMetadata,
) (inst dagql.ObjectResult[*core.GitRepository], _ error) {
	if metadata.HasRemoteMetadata {
		remotes := make([]core.GitRemote, 0, len(metadata.Remotes))
		for _, remote := range metadata.Remotes {
			entry := core.GitRemote{Name: remote.Name, URL: remote.Url, PushURL: remote.PushUrl}
			if remote.Name == metadata.RemoteName && metadata.RemoteUrl != "" {
				entry.URL, entry.PushURL = metadata.RemoteUrl, metadata.RemotePushUrl
			}
			remotes = append(remotes, entry)
		}
		data, err := json.Marshal(remotes)
		if err != nil {
			return inst, err
		}
		if err := srv.Select(ctx, repo, &repo, dagql.Selector{
			Field: "__withRemoteSelection",
			Args: []dagql.NamedInput{
				{Name: "remotes", Value: dagql.String(data)},
				{Name: "upstreamRemote", Value: dagql.String(metadata.UpstreamRemote)},
			},
		}); err != nil {
			return inst, fmt.Errorf("record captured remotes: %w", err)
		}
		return repo, nil
	}
	if metadata.RemotePushUrl != "" {
		if err := srv.Select(ctx, repo, &repo, dagql.Selector{
			Field: "withRemote",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.NewString("origin")},
				{Name: "url", Value: dagql.NewString(metadata.RemoteUrl)},
				{Name: "pushUrl", Value: dagql.NewString(metadata.RemotePushUrl)},
			},
		}); err != nil {
			return inst, fmt.Errorf("record workspace push destinations: %w", err)
		}
	}

	return repo, nil
}

func checkpointSSHAuthArgs(ctx context.Context, srv *dagql.Server) ([]dagql.NamedInput, error) {
	caller, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if caller.SSHAuthSocketPath == "" {
		return nil, nil
	}

	// Pass a scoped socket explicitly so a previously cached, unauthenticated
	// Query.git cannot hide the prepared agent, and this capture does not
	// authenticate unrelated reads through that same per-client cache entry.
	var socket dagql.ObjectResult[*core.Socket]
	if err := srv.Select(ctx, srv.Root(), &socket,
		dagql.Selector{Field: "host"},
		dagql.Selector{Field: "_sshAuthSocket"},
	); err != nil {
		return nil, fmt.Errorf("scope workspace snapshot SSH authentication: %w", err)
	}
	socketID, err := socket.ID()
	if err != nil {
		return nil, err
	}
	return []dagql.NamedInput{
		{Name: "sshAuthSocket", Value: dagql.Opt(dagql.NewID[*core.Socket](socketID))},
		{Name: "sshAuthSocketScoped", Value: dagql.NewBoolean(true)},
	}, nil
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
	// The user-level overlay rides in the recipe, so the frozen workspace
	// keeps the configuration it was captured with, even when rebuilt in
	// another session; Workspace.withUserConfig refreshes it explicitly.
	if overlay := metadata.UserConfigOverlay(); overlay != nil {
		withOverlay, err := workspaceWithUserConfigOverlay(ctx, srv, inst, overlay)
		if err != nil {
			return inst, err
		}
		inst = withOverlay
	}
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
		if candidate.GetClassification() == gitsession.CaptureClassificationNestedRepository {
			// A nested repository's contents never travel with a checkpoint,
			// so there is no byte count or reviewed state to show.
			fmt.Fprintf(&summary, "\n- %s (untracked nested repository; never captured — Include and Drop both omit it)", strconv.Quote(candidate.Path))
			continue
		}
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

// checkpointOverlay records a host-backed workspace's engine edits on top of
// its captured checkout, so the frozen recipe no longer reads the client's
// filesystem. The overlay itself cannot be kept: it is diffed against a
// sparse live host read (see overlayEditWithHost), and MCP applies tool edits
// to a host-backed workspace raw. So it is rendered against the captured tree
// and recorded as that patch (core.ApplyPatchOnto), as MCP records tool edits
// on an in-engine workspace.
//
// A patch core.EmbedPatch refuses, too large or carrying a binary file, is not
// recorded: binaries and large content stay out of recipes and traces. The
// overlay is applied raw instead, as MCP.applyChangeset does, and that is the
// one case where the frozen recipe still depends on the host: replaying it
// re-reads the touched host paths, through the owning client.
func checkpointOverlay(
	ctx context.Context,
	srv *dagql.Server,
	frozen dagql.ObjectResult[*core.Workspace],
	changes dagql.ObjectResult[*core.Changeset],
) (out dagql.ObjectResult[*core.Workspace], err error) {
	var before dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, frozen, &before, dagql.Selector{
		Field: "directory", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString("/")}},
	}); err != nil {
		return out, err
	}
	// Rendered against the frozen tree itself, and bounded: git stops at the
	// first binary hunk or past the budget, rather than writing out a build
	// output's base85 only for it to be refused.
	rendered, err := changes.Self().RenderPatchOnto(ctx, before, ".", core.EmbeddedPatchMaxBytes)
	if err == nil {
		if rendered.IsEmpty() {
			return frozen, nil
		}
		// Rendered against this very tree, so it fits by construction.
		out, err = core.ApplyPatchOnto(ctx, srv, frozen, rendered, "workspace-overlay.patch", core.PatchConflictFail)
	}
	if core.PatchNotEmbeddable(err) {
		changesID, err := changes.ID()
		if err != nil {
			return out, err
		}
		err = srv.Select(ctx, frozen, &out, dagql.Selector{
			Field: "withChanges", Args: []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)}},
		})
		return out, err
	}
	if err != nil {
		return out, fmt.Errorf("apply workspace overlay to checkpoint: %w", err)
	}
	return out, nil
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

const (
	checkpointInclude = "include"
	checkpointDrop    = "drop"
	checkpointCancel  = "cancel"
)

func applyCheckpointDecision(policy *gitsession.CaptureGitPolicy, candidates []*gitsession.CaptureGitCandidate, choice string) error {
	switch choice {
	case checkpointDrop:
		policy.DropUntracked = true
		policy.Include = nil
		policy.ApprovalTokens = nil
	case checkpointInclude:
		for _, candidate := range candidates {
			if candidate.GetClassification() == gitsession.CaptureClassificationNestedRepository {
				// Approving a nested repository would ask capture to carry its
				// contents as plain files, which it never does. Include keeps
				// the other selections and omits the boundary, as the prompt
				// summary states.
				policy.Exclude = append(policy.Exclude, candidate.GetPath())
				continue
			}
			if candidate.ApprovalToken == "" {
				return fmt.Errorf("workspace snapshot approval candidate %s has no state token", strconv.Quote(candidate.Path))
			}
			policy.ApprovalTokens = append(policy.ApprovalTokens, candidate.ApprovalToken)
		}
	case checkpointCancel:
		return fmt.Errorf("workspace snapshot rejected selected dirty paths")
	default:
		return fmt.Errorf("invalid workspace snapshot choice")
	}
	return nil
}

func checkpointPrompt(ctx context.Context, bk *engineutil.Client, summary string) (string, error) {
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return "", err
	}
	caller, err := bk.GetHostServiceCaller(ctx, md.ClientID)
	if err != nil {
		return "", err
	}
	return checkpointPromptClient(ctx, prompt.NewPromptClient(caller.Conn()), caller.Supports("/dagger.prompt.Prompt/PromptSelect"), summary)
}

func checkpointPromptClient(ctx context.Context, client prompt.PromptClient, supportsSelect bool, summary string) (string, error) {
	if supportsSelect {
		response, err := client.PromptSelect(ctx, &prompt.SelectRequest{
			Title:  "Include workspace changes?",
			Prompt: summary + "\n\nDrop omits all untracked files from the checkpoint, keeps tracked changes, and leaves local files untouched.",
			Choices: []*prompt.SelectChoice{
				{Id: checkpointInclude, Label: "Include"},
				{Id: checkpointDrop, Label: "Drop"},
				{Id: checkpointCancel, Label: "Cancel"},
			},
			DefaultChoice: checkpointCancel,
		})
		if err == nil {
			switch response.GetChoice() {
			case checkpointInclude, checkpointDrop, checkpointCancel:
				return response.Choice, nil
			default:
				return "", fmt.Errorf("invalid workspace snapshot choice")
			}
		}
		// A new proxy can advertise the RPC while its upstream client is old.
		if status.Code(err) != codes.Unimplemented {
			return "", err
		}
	}
	response, err := client.PromptBool(ctx, &prompt.BoolRequest{
		Title: "Include workspace changes?", Prompt: summary,
	})
	if err != nil {
		return "", err
	}
	if response.Response {
		return checkpointInclude, nil
	}
	return checkpointCancel, nil
}
