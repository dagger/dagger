package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// workspaceGitPushArgs shapes WorkspaceGit.push.
type workspaceGitPushArgs struct {
	Remote string `default:"origin"`
	Branch string `default:""`
	Force  bool   `default:"false"`
}

// workspaceGitPush pushes the workspace's git HEAD — the newest staged commit
// when any are staged, else the checkout's own HEAD — to a remote.
//
// Mechanism: the push runs through the *client's own git*, the mirror image
// of how staged commits are saved (exportPendingCommits). The checkout is
// where the user's remotes, credential helpers, ssh agent and hooks live, so
// only a push from there behaves exactly like `git push` run by the user.
// Commits that exist only engine-side travel as a git bundle — the same
// packaging a save uses — but unlike a save they are fetched into the
// checkout's object database without creating or moving any local ref, and
// pushed by hash. The local checkout's branches and work tree are never
// modified, so staged work can land on a remote branch (say, to open a pull
// request) without ever being saved locally.
func (s *workspaceSchema) workspaceGitPush(
	ctx context.Context,
	parent dagql.ObjectResult[*core.WorkspaceGit],
	args workspaceGitPushArgs,
) (dagql.String, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return "", err
	}
	ws := parent.Self().Workspace.Self()

	if args.Remote == "" {
		return "", fmt.Errorf("push: remote is required")
	}
	if _, ok := ws.SourceGitRef(); ok && !ws.IsPortableCheckpoint() {
		return "", fmt.Errorf("cannot push from a remote git workspace: there is no local checkout whose remotes and credentials could run the push")
	}

	// Preconditions first, exactly like a save: nothing is transferred until
	// the workspace is known to be backed by a local git checkout.
	clientCtx, hostPath, err := workspaceExportContext(ctx, ws)
	if err != nil {
		return "", fmt.Errorf("cannot push: %w", err)
	}
	if err := s.ensureWorkspaceGitDirectory(ctx, ws); err != nil {
		return "", fmt.Errorf("cannot push: %w", err)
	}

	// The destination ref is resolved on the host when no branch is named,
	// so the default — the checkout's current branch — reflects the checkout
	// at push time.
	var destRef string
	switch {
	case args.Branch == "":
	case strings.HasPrefix(args.Branch, "refs/"):
		destRef = args.Branch
	default:
		destRef = "refs/heads/" + args.Branch
	}

	// What gets pushed is Workspace.git.head: the newest staged commit when
	// the stack is non-empty, else the checkout's HEAD as the workspace sees
	// it. Staged commits do not exist in the checkout yet, so they ride
	// along as a bundle of exactly the staged range.
	var (
		targetSHA string
		bundleRef string
		bundle    []byte
	)
	if latest, ok := ws.LatestPendingCommit(); ok && latest.Repo.Self() != nil {
		targetSHA = latest.SHA
		bundle, err = core.WorkspaceStagedCommitsBundle(ctx, latest.Repo, latest.SHA, ws.BaseHeadSHA)
		if err != nil {
			return "", fmt.Errorf("cannot push staged commits: %w", err)
		}
		bundleRef = core.WorkspaceStagedCommitsRef
	} else {
		var head dagql.String
		if err := srv.Select(ctx, parent, &head,
			dagql.Selector{Field: "head"},
			dagql.Selector{Field: "commit"},
		); err != nil {
			return "", fmt.Errorf("resolve workspace HEAD: %w", err)
		}
		targetSHA = head.String()
	}

	query, err := core.CurrentQuery(clientCtx)
	if err != nil {
		return "", err
	}
	bk, err := query.Engine(clientCtx)
	if err != nil {
		return "", fmt.Errorf("buildkit: %w", err)
	}

	pushedRef, err := bk.PushGitCommits(
		clientCtx, hostPath, args.Remote, destRef, targetSHA, args.Force, bundleRef, bundle)
	if err != nil {
		return "", fmt.Errorf("cannot push: %w", err)
	}
	return dagql.NewString(pushedRef), nil
}
