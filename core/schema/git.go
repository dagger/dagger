package schema

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/sources/netconfhttp"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

func init() {
	// allow injection of custom dns resolver for go-git
	customClient := &http.Client{
		Transport: netconfhttp.NewInjectableTransport(http.DefaultTransport),
	}
	client.InstallProtocol("http", githttp.NewClient(customClient))
	client.InstallProtocol("https", githttp.NewClient(customClient))
}

var _ SchemaResolvers = &gitSchema{}

type gitSchema struct {
	// walkRevision overrides GitRepository.WalkRevision in tests.
	walkRevision func(ctx context.Context, repo *core.GitRepository, base *gitutil.Ref, rev gitutil.Revision) (string, error)
}

func (s *gitSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.NodeFunc("git", s.git).
			WithInput(gitPerClientInput).
			View(AllVersion).
			Doc(`Queries a Git repository.`).
			Args(
				dagql.Arg("url").Doc(
					`URL of the git repository.`,
					"Can be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`.",
					`Suffix ".git" is optional.`),
				dagql.Arg("keepGitDir").
					View(AllVersion).
					Default(dagql.Opt(dagql.Boolean(true))).
					Doc(`Set to true to keep .git directory.`).Deprecated(),
				dagql.Arg("keepGitDir").
					View(BeforeVersion("v0.13.4")).
					Doc(`Set to true to keep .git directory.`).Deprecated(),
				dagql.Arg("sshKnownHosts").Doc(`Set SSH known hosts`),
				dagql.Arg("sshAuthSocket").Doc(`Set SSH auth socket`),
				dagql.Arg("httpAuthUsername").Doc(`Username used to populate the password during basic HTTP Authorization`),
				dagql.Arg("httpAuthToken").Doc(`Secret used to populate the password during basic HTTP Authorization`),
				dagql.Arg("httpAuthHeader").Doc(`Secret used to populate the Authorization HTTP header`),
				dagql.Arg("experimentalServiceHost").Doc(`A service which must be started before the repo is fetched.`),
			),
		// Query.git is scoped per client because it discovers implicit inputs
		// (credential helper tokens, the SSH agent socket, remote visibility)
		// from the calling client. Once those are explicit arguments the
		// repository is fully described by them, so git delegates to this field
		// and every client with the same inputs shares one result and one
		// downstream cache lineage.
		dagql.NodeFunc("__gitRepository", s.gitRepository).
			// Still honor a deliberate per-client cache bust (a workspace lock
			// refresh or override): those callers need fresh remote metadata,
			// which this result caches for its lifetime.
			WithInput(dagql.CacheScopeInput).
			View(AllVersion).
			Doc(`(Internal-only) Construct a remote Git repository from fully explicit inputs.`),
	}.Install(srv)

	dagql.Fields[*core.GitRepository]{
		dagql.NodeFunc("__resolvedRef", s.resolvedRef).
			View(AllVersion).
			IsPersistable().
			Doc(`(Internal-only) Returns a Git ref identified by its resolved name and commit.`),
		// Named ref lookups consult the calling client's workspace lock (which
		// pin applies, and whether one should be written), so their results
		// are scoped per client even though the repository itself is shared.
		// noLock asks for a live resolution, which no earlier call may answer:
		// it gets a fresh key per call (see gitLiveInput).
		dagql.NodeFunc("head", s.head).
			WithInput(gitLiveInput(dagql.PerClientInput)).
			Doc(`Returns details for HEAD.`).
			Args(
				dagql.Arg("noLock").
					View(AfterVersion("v1.0.0-beta.15")).
					Doc(`Ignore the workspace lockfile for this lookup.`),
			),
		dagql.NodeFunc("ref", s.revision).
			WithInput(gitLockScopedInput("name")).
			Doc(`Returns details of a ref.`).
			Args(
				dagql.Arg("name").Doc(
					`Ref's name (can be a commit identifier, a tag name, a branch name, or a fully-qualified ref).`,
					`Commit identifiers may be abbreviated: an unambiguous hex prefix (4-40 characters) of a commit SHA resolves like git rev-parse, with named refs taking precedence. Abbreviated SHAs resolve against locally available objects, so remote repositories (resolved via ls-remote) can only expand prefixes of already-fetched commits; use the full SHA or a named ref otherwise.`,
					"The name may be followed by git revision suffixes, applied left to right: `~N` follows first parents N times and `^N` selects the Nth parent (`~` and `^` mean 1, `^0` is the commit itself), e.g. `HEAD~3`, `main^2` or `abc1234~2`. The result is a detached ref of the resulting commit; remote repositories fetch the history the walk needs. Other git revision syntax (`^{...}`, `@{...}`, `:path`, ranges) is not supported.",
					`A repository derived from a remote one (e.g. a workspace's history after a snapshot or commit) resolves names and commits it does not contain itself through that remote, with its authentication. Its branches and tags listings include the remote's.`),
				dagql.Arg("noLock").
					View(AfterVersion("v1.0.0-beta.15")).
					Doc(`Ignore the workspace lockfile for this lookup.`),
			),
		dagql.NodeFunc("branch", s.branch).
			WithInput(gitLiveInput(dagql.PerClientInput)).
			View(AllVersion).
			Doc(`Returns details of a branch.`).
			Args(
				dagql.Arg("name").Doc(`Branch's name (e.g., "main").`),
				dagql.Arg("noLock").
					View(AfterVersion("v1.0.0-beta.15")).
					Doc(`Ignore the workspace lockfile for this lookup.`),
			),
		dagql.NodeFunc("tag", s.tag).
			WithInput(gitLiveInput(dagql.PerClientInput)).
			View(AllVersion).
			Doc(`Returns details of a tag.`).
			Args(
				dagql.Arg("name").Doc(`Tag's name (e.g., "v0.3.9").`),
				dagql.Arg("noLock").
					View(AfterVersion("v1.0.0-beta.15")).
					Doc(`Ignore the workspace lockfile for this lookup.`),
			),
		dagql.NodeFunc("commit", s.commit).
			View(AfterVersion("v1.0.0-0")).
			Doc(`Returns details of a commit.`).
			Args(
				// TODO: id is normally a reserved word; we should probably rename this
				dagql.Arg("id").Doc(
					`Identifier of the commit (e.g., "b6315d8f2810962c601af73f86831f6866ea798b").`,
					`May be abbreviated to an unambiguous hex prefix (4-40 characters), which is expanded against locally available objects. Remote repositories (resolved via ls-remote) can only expand prefixes of already-fetched commits; use the full SHA otherwise.`),
			),
		dagql.NodeFunc("commit", s.commitRef).
			WithInput(gitLockScopedInput("id")).
			View(BeforeVersion("v1.0.0-0")).
			Doc(`Returns details of a commit.`).
			Args(
				// TODO: id is normally a reserved word; we should probably rename this
				dagql.Arg("id").Doc(`Identifier of the commit (e.g., "b6315d8f2810962c601af73f86831f6866ea798b").`),
			),
		dagql.NodeFunc("latest", s.latest).
			WithInput(gitLiveInput(dagql.PerClientInput)).
			View(AfterVersion("v1.0.0-0")).
			Doc(
				`Return the latest stable release tag, falling back to HEAD when no release exists.`,
				`Release selection accepts an optional "v" prefix, incomplete versions, and zero-padded numeric components. This operation is pinned unless noLock is enabled.`,
			).
			Args(
				dagql.Arg("version").
					Doc(`Version query used to select the greatest matching release ref.`).
					View(AfterVersion(workspace.VersionQueriesVersion)),
				dagql.Arg("tagPrefix").
					Doc(`Restrict release tags to a monorepo subpath.`).
					Internal(),
				dagql.Arg("noLock").
					View(AfterVersion("v1.0.0-beta.15")).
					Doc(`Ignore the workspace lockfile for this lookup.`),
			),

		// The repository result is shared across sessions, but tags and
		// branches move: answer each session from its own listing.
		dagql.Func("tags", s.tags).
			WithInput(dagql.PerSessionInput).
			Doc(`tags that match any of the given glob patterns.`).
			Args(
				dagql.Arg("patterns").Doc(`Glob patterns (e.g., "refs/tags/v*").`),
			),
		dagql.Func("branches", s.branches).
			WithInput(dagql.PerSessionInput).
			Doc(`branches that match any of the given glob patterns.`).
			Args(
				dagql.Arg("patterns").Doc(`Glob patterns (e.g., "refs/tags/v*").`),
			),

		// bundle resolves its ref names against this session's remote listing
		// and hands the pinned commits to __bundleFile, which does the shared,
		// persisted work. It must not itself be answered from another session.
		dagql.NodeFunc("bundle", s.bundle).
			WithInput(dagql.PerSessionInput).
			View(AfterVersion("v1.0.0-beta.10")).
			IsPersistable().
			Doc(`Pack the given refs and the objects needed to reconstruct them into a Git bundle.`).
			Args(
				dagql.Arg("refs").Doc(`Refs to advertise in the bundle. At least one named ref is required.`),
				dagql.Arg("base").Doc(`A Git ref whose reachable objects are omitted and recorded as a prerequisite.`),
			),
		dagql.NodeFunc("withBundle", s.withBundle).
			View(AfterVersion("v1.0.0-beta.10")).
			IsPersistable().
			Doc(`Import a Git bundle after fetching and verifying all of its prerequisites.`).
			Args(
				dagql.Arg("bundle").Doc(`The Git bundle to import.`),
				dagql.Arg("prerequisiteRef").Doc(`An optional remote ref hint for fetching a prerequisite when the remote does not allow fetches by object ID.`),
			),
		dagql.NodeFunc("__bundleFile", s.bundleFile).
			View(AfterVersion("v1.0.0-beta.10")).
			IsPersistable().
			Doc(`(Internal-only) Materialize a Git bundle as a File.`).
			Args(
				dagql.Arg("refs"),
				dagql.Arg("base"),
			),
		dagql.NodeFunc("__withBundleDirectory", s.withBundleDirectory).
			View(AfterVersion("v1.0.0-beta.10")).
			IsPersistable().
			Doc(`(Internal-only) Materialize the canonical repository produced by importing a Git bundle.`).
			Args(
				dagql.Arg("bundle"),
				dagql.Arg("prerequisiteRef"),
			),
		dagql.Func("withRemote", s.withRemote).
			View(AfterVersion("v1.0.0-0")).
			IsPersistable().
			Doc("Register a named remote on this repository, replacing any registered remote of the same name.",
				"Registered remotes are recorded in checkouts materialized from this repository (GitRef.tree, Workspace.git.directory), so remote-aware tooling like gh can resolve and fetch from them. The origin remote also routes push when no explicit destination is passed: its push URL, or its URL, becomes the default destination.",
				"Routing metadata only, never a credential grant: pushes still authenticate with the caller's own credentials and require approval as usual.").
			Args(
				dagql.Arg("name").Doc(`The remote's name, e.g. "origin" or "upstream".`),
				dagql.Arg("url").Doc(`The remote's fetch URL.`),
				dagql.Arg("pushUrl").Doc(`Push destination, when pushes go somewhere other than url. Empty uses url.`),
			),
		dagql.NodeFunc("remotes", s.remotes).
			View(AfterVersion("v1.0.0-0")).IsPersistable().
			Doc("List this repository's named remotes, with registered remotes overriding configured ones. Does not contact remote servers."),
		dagql.Func("__withRemoteSelection", s.withRemoteSelection).
			View(AfterVersion("v1.0.0-0")).IsPersistable().
			Doc("(Internal-only) Record captured remote configuration and upstream selection."),
		dagql.NodeFunc("remote", s.remote).
			View(AfterVersion("v1.0.0-0")).IsPersistable().
			Doc("Look up a remote by name. Fails when the remote does not exist.").
			Args(dagql.Arg("name").Doc("The remote's name.")),
		dagql.NodeFunc("defaultRemote", s.defaultRemote).
			View(AfterVersion("v1.0.0-0")).IsPersistable().
			Doc("Return the sole remote, otherwise origin, otherwise the selected branch's upstream remote, otherwise null.",
				"Frozen workspaces retain their captured upstream selection. Does not contact remote servers."),
		dagql.NodeFunc("__cleaned", s.cleaned).
			IsPersistable().
			Doc(`(Internal-only) Cleans the git repository by removing untracked files and resetting modifications.`),
		dagql.NodeFunc("withContents", s.withContents).
			View(AfterVersion("v1.0.0-0")).
			IsPersistable().
			Doc("Replace this repository's storage with the supplied self-contained Git repository, retaining its logical URL and push destinations.",
				"Accepts a whole checkout (including .git and pending file edits), .git contents, or a bare repository. Does not initialize a repository, merge histories, or modify either input.",
				"The receiver's logical routing wins over the supplied Git configuration; that configuration is not rewritten. Use Directory.asGit to open the supplied repository without retaining the receiver's routing.",
				"When the receiver is a remote repository (or was derived from one), that remote is retained with its authentication: refs the supplied storage does not contain resolve through it.").
			Args(dagql.Arg("directory").Doc("Existing Git storage to open. Git metadata and object dependencies must be contained in this directory.")),
		dagql.NodeFunc("uncommitted", s.uncommitted).
			Doc("Returns the changeset of uncommitted changes in the git repository."),
		dagql.NodeFunc("asWorkspace", s.asWorkspace).
			View(AfterVersion("v1.0.0-0")).
			Doc("Creates a synthetic workspace from this repository's HEAD and uncommitted file changes.",
				"Pending changes are applied at the repository root. The staging split is not preserved. The source repository is not modified.").
			Args(
				dagql.Arg("cwd").Doc("Current working directory inside the workspace root. Defaults to the workspace root."),
			),

		dagql.Func("withAuthToken", s.withAuthToken).
			Doc(`Token to authenticate the remote with.`).
			View(BeforeVersion("v0.19.0")).
			Deprecated(`Use "httpAuthToken" in the constructor instead.`).
			Args(
				dagql.Arg("token").Doc(`Secret used to populate the password during basic HTTP Authorization`),
			),
		dagql.Func("withAuthHeader", s.withAuthHeader).
			Doc(`Header to authenticate the remote with.`).
			View(BeforeVersion("v0.19.0")).
			Deprecated(`Use "httpAuthHeader" in the constructor instead.`).
			Args(
				dagql.Arg("header").Doc(`Secret used to populate the Authorization HTTP header`),
			),
	}.Install(srv)

	dagql.Fields[*core.GitRef]{
		dagql.Func("__workspaceExportBaseReady", func(ctx context.Context, ref *core.GitRef, _ struct{}) (dagql.Boolean, error) {
			ready, err := ref.WorkspaceExportBaseReady(ctx)
			return dagql.Boolean(ready), err
		}).View(AfterVersion("v1.0.0-0")).Doc("(Internal-only) Check immutable local history for workspace export base reuse."),
		dagql.NodeFunc("push", s.push).
			View(AfterVersion("v1.0.0-0")).
			DoNotCache("Pushes to an external Git repository on each invocation.").
			NotReplayable("Requires explicit Git push authorization from the calling client").
			Doc("Push this ref's commit and history to a remote repository using the destination's credentials.",
				"The source can come from a remote repository or an engine-side Git repository. To publish a workspace's commits, use Workspace.git.head.push. Pushing does not modify the calling client's checkout, and checkout hooks do not run.",
				"A missing remote ref is created. Without a lease, Git's normal non-force rules apply. Each invocation performs a push; loading the returned receipt does not push again.").
			Args(dagql.Arg("to").Doc("Destination remote repository. Defaults to the origin remote's push routing, or the source's repository URL when none is registered. Required when the source has no remote URL."),
				dagql.Arg("remote").Doc("Name of a registered remote to push to (see GitRepository.withRemote). Defaults to origin. The remote's push URLs, or its URL, become the destination; more than one push URL requires an explicit to instead."),
				dagql.Arg("branch").Doc("Destination branch; a refs/ prefix is used verbatim. Defaults to this ref's branch name. Required for detached and non-branch refs."),
				dagql.Arg("expectedRemoteSHA").Doc("Optional lease: a full lowercase object ID allows replacement only if the remote ref still has that value. Checked even for up-to-date pushes. Empty or omitted uses normal non-force rules, creating the ref if it does not exist.")),
		dagql.NodeFunc("targetCommit", s.targetCommit).
			View(AfterVersion("v1.0.0-0")).
			Doc(`The commit this ref resolves to.`),
		dagql.NodeFunc("commitSHA", s.fetchCommit).
			IsPersistable().
			View(AfterVersion("v1.0.0-0")).
			Doc(`The resolved commit SHA at this ref.`),
		dagql.NodeFunc("__fullCheckout", s.fullCheckout).
			IsPersistable().
			Doc(`(Internal-only) Materialize full history and Git metadata regardless of keepGitDir.`),
		dagql.NodeFuncWithDynamicInputs("tree", s.tree, s.treeCacheKey).
			IsPersistable().
			View(AllVersion).
			Doc(`The filesystem tree at this ref.`).
			Args(
				dagql.Arg("discardGitDir").
					Doc(`Set to true to discard .git directory.`),
				dagql.Arg("depth").
					Doc(`The depth of the tree to fetch.`),
				dagql.Arg("includeTags").
					Doc(`Set to true to populate tag refs in the local checkout .git.`),
				dagql.Arg("sshKnownHosts").
					View(BeforeVersion("v0.12.0")).
					Doc("This option should be passed to `git` instead.").Deprecated(),
				dagql.Arg("sshAuthSocket").
					View(BeforeVersion("v0.12.0")).
					Doc("This option should be passed to `git` instead.").Deprecated(),
			),
		dagql.NodeFunc("commit", s.fetchCommit).
			IsPersistable().
			View(BeforeVersion("v1.0.0-0")).
			Doc(`The resolved commit id at this ref.`),
		dagql.NodeFunc("commit", s.fetchCommit).
			IsPersistable().
			View(AfterVersion("v1.0.0-0")).
			Doc(`The resolved commit id at this ref.`).
			Deprecated(`Use "commitSHA" instead.`),
		dagql.NodeFunc("name", s.fetchRef).
			IsPersistable().
			View(AfterVersion("v1.0.0-0")).
			Doc(`The resolved name of this ref.`),
		dagql.NodeFunc("ref", s.fetchRef).
			IsPersistable().
			View(BeforeVersion("v1.0.0-0")).
			Doc(`The resolved ref name at this ref.`),
		dagql.NodeFunc("ref", s.fetchRef).
			IsPersistable().
			View(AfterVersion("v1.0.0-0")).
			Doc(`The resolved ref name at this ref.`).
			Deprecated(`Use "name" instead.`),
		dagql.NodeFunc("contains", s.contains).
			View(AfterVersion("v1.0.0-0")).
			Doc("Return true when the other ref's commit equals this commit or is an ancestor of it.",
				"Compares commit history across branches, tags and detached refs. Incomplete or unavailable history is an error.").
			Args(dagql.Arg("other").Doc("The ref whose commit to look for in this ref's history.")),
		dagql.NodeFunc("commonAncestor", s.commonAncestor).
			Doc(`Find the best common ancestor between this ref and another ref.`).
			Args(
				dagql.Arg("other").Doc(`The other ref to compare against.`),
			),
		dagql.NodeFunc("log", s.log).
			View(AfterVersion("v1.0.0-0")).
			Doc(`Commits reachable from this ref, newest first, starting with the commit this ref resolves to.`).
			Args(
				dagql.Arg("limit").Doc(`Maximum number of commits to return.`),
				dagql.Arg("paths").Doc(`Only include commits touching these paths, relative to the root of the repository.`),
				dagql.Arg("base").Doc(`Exclude commits reachable from this ref, i.e. only list commits added on top of it.`),
			),
		dagql.NodeFunc("withCommit", s.gitRefWithCommit).
			View(AfterVersion("v1.0.0-0")).
			IsPersistable().
			Doc("Create a single-parent commit on this ref by applying a changeset's edits.",
				"Three-way merges the changeset against this ref's tree, using its before snapshot as the base. Preserves compatible parent edits and fails on conflicts. Does not modify the input repository or host checkout.",
				"Identity and dates are explicit; neither client Git configuration nor the current clock is consulted.").
			Args(
				dagql.Arg("changes").Doc("Changes to apply. Use Changeset.filter to select paths before committing."),
				dagql.Arg("message").Doc("Commit message."),
				dagql.Arg("date").Doc("RFC3339 author date; also the default committer date."),
				dagql.Arg("authorName").Doc("Author name."),
				dagql.Arg("authorEmail").Doc("Author email."),
				dagql.Arg("committerName").Doc("Committer name. Defaults to authorName."),
				dagql.Arg("committerEmail").Doc("Committer email. Defaults to authorEmail."),
				dagql.Arg("committerDate").Doc("RFC3339 committer date. Defaults to date."),
				dagql.Arg("allowEmpty").Doc("Allow a commit whose tree matches its parent, including when the supplied edits are already present. Defaults to false."),
				dagql.Arg("signoff").Doc("Add a Signed-off-by trailer using the commit author's name and email."),
			),
		dagql.NodeFuncWithDynamicInputs("__hydrateRepository", s.gitRefHydrateRepository, s.gitRefHydrateRepositoryKey).
			View(AfterVersion("v1.0.0-0")).
			IsPersistable().
			Doc("(Internal-only) Hydrate an owned shallow repository on history demand."),
		dagql.NodeFuncWithDynamicInputs("__nativeCommitBase", s.gitRefNativeCommitBase, s.gitRefNativeCommitBaseKey).
			View(AfterVersion("v1.0.0-0")).
			IsPersistable().
			Doc("(Internal-only) Own the selected remote commit's object closure."),
		dagql.NodeFunc("__withCommitRepository", s.gitRefWithCommitRepository).
			View(AfterVersion("v1.0.0-0")).
			IsPersistable().
			Doc("(Internal-only) Preserve commit provenance for incremental source checkouts."),
		dagql.NodeFunc("__withCommitDirectory", s.gitRefWithCommitDirectory).
			View(AfterVersion("v1.0.0-0")).
			IsPersistable().
			Doc("(Internal-only) Materialize the repository containing a new commit."),
		dagql.NodeFunc("asRepository", s.gitRefAsRepository).
			View(AfterVersion("v1.0.0-0")).
			IsPersistable().
			Doc("Return this ref's repository with HEAD pinned to the selected commit.",
				"Preserves the original repository backend, connection information, and other refs. Does not modify a branch or checkout, or prune history."),
		dagql.NodeFunc("asWorkspace", s.gitRefAsWorkspace).
			View(AfterVersion("v1.0.0-0")).
			Doc("Creates a synthetic workspace from this git ref.").
			Args(
				dagql.Arg("cwd").Doc("Current working directory inside the workspace root. Defaults to the workspace root."),
			),
	}.Install(srv)

	srv.InstallObject(dagql.NewClass[*core.GitRemoteHandle](srv).View(AfterVersion("v1.0.0-0")))
	dagql.Fields[*core.GitRemoteHandle]{
		dagql.NodeFunc("repository", s.remoteRepository).
			WithInput(dagql.PerClientInput).
			View(AfterVersion("v1.0.0-0")).IsPersistable().
			Doc("Access this remote's repository using its fetch URL and the caller's credentials, or the source's existing capability for this exact destination.",
				"HEAD is the remote's HEAD, independent of the workspace's selected commit. Remote registration alone does not grant credentials."),
	}.Install(srv)
	srv.InstallObject(dagql.NewClass[*core.GitPushResult](srv).View(AfterVersion("v1.0.0-0")))
	core.GitPushDispositions.Install(srv, AfterVersion("v1.0.0-0"))
	dagql.Fields[*core.GitPushResult]{}.Install(srv)
	dagql.Fields[*core.Query]{
		dagql.Func("__gitPushResult", s.pushResult).View(AfterVersion("v1.0.0-0")).
			Doc("(Internal-only) Reconstruct a completed push receipt without contacting the remote."),
	}.Install(srv)

	srv.InstallObject(dagql.NewClass[*core.GitBundle](srv).View(AfterVersion("v1.0.0-beta.10")))
	srv.InstallObject(dagql.NewClass[*core.GitBundleRef](srv).View(AfterVersion("v1.0.0-beta.10")))
	dagql.Fields[*core.GitBundle]{
		dagql.NodeFunc("validate", s.validateBundle).
			IsPersistable().
			Doc(`Perform full structural verification of the bundle and error if it is malformed.`),
		dagql.NodeFunc("asFile", s.bundleAsFile).
			IsPersistable().
			Doc(`Return the bundle bytes as a File.`),
	}.Install(srv)
	dagql.Fields[*core.GitBundleRef]{}.Install(srv)

	srv.InstallObject(dagql.NewClass[*core.GitCommit](srv).View(AfterVersion("v1.0.0-0")))

	dagql.Fields[*core.GitCommit]{
		// A commit is immutable, but its tags aren't: these two fields are the
		// only ones that read tag state, so scope them per-session rather than
		// mixing tags into the commit's identity, which would invalidate the
		// commit's metadata and tree every time anything in the repo is tagged.
		// Per-session matches the freshness of the remote snapshot they answer
		// from, the same guarantee GitRepository.tags and latest give.
		// (selectGitReleaseTag re-resolves that snapshot for the same reason.)
		dagql.NodeFunc("releaseTag", s.releaseTag).
			WithInput(dagql.PerSessionInput).
			Doc(`The latest semver release tag that points directly at this commit.`).
			Args(
				dagql.Arg("includePreRelease").Doc(`Include pre-release tags when choosing the latest tag.`),
			),
		dagql.NodeFunc("ancestorReleaseTag", s.ancestorReleaseTag).
			WithInput(dagql.PerSessionInput).
			Doc(`The latest semver release tag reachable from this commit.`).
			Args(
				dagql.Arg("includePreRelease").Doc(`Include pre-release tags when choosing the latest tag.`),
			),
		dagql.NodeFuncWithDynamicInputs("tree", s.commitTree, s.commitTreeCacheKey).
			IsPersistable().
			Doc(`The filesystem tree at this commit.`).
			Args(
				dagql.Arg("discardGitDir").
					Doc(`Set to true to discard .git directory.`),
				dagql.Arg("depth").
					Doc(`The depth of the tree to fetch.`),
				dagql.Arg("includeTags").
					Doc(`Set to true to populate tag refs in the local checkout .git.`),
			),
		dagql.NodeFunc("changes", s.commitChanges).
			IsPersistable().
			Doc("Returns the changes from the first parent to this commit, excluding Git metadata.",
				"Root commits are compared with an empty tree. Merge commits are compared with their first parent, not a merge base.").
			Args(dagql.Arg("against").Doc("Use this commit as the comparison base instead of the first parent. The comparison commit may belong to an unrelated history or repository.")),
		dagql.NodeFunc("sha", s.commitSHA).
			IsPersistable().
			Doc(`The full commit SHA.`),
		dagql.NodeFunc("shortSha", s.commitShortSHA).
			IsPersistable().
			Doc(`The abbreviated commit SHA.`),
		dagql.NodeFunc("authoredDate", s.commitAuthoredDate).
			IsPersistable().
			Doc(`Git author date, in RFC3339 format.`),
		dagql.NodeFunc("committedDate", s.commitCommittedDate).
			IsPersistable().
			Doc(`Git committer date, in RFC3339 format.`),
		dagql.NodeFunc("authorName", s.commitAuthorName).
			IsPersistable().
			Doc(`Git author name.`),
		dagql.NodeFunc("authorEmail", s.commitAuthorEmail).
			IsPersistable().
			Doc(`Git author email.`),
		dagql.NodeFunc("committerName", s.commitCommitterName).
			IsPersistable().
			Doc(`Git committer name.`),
		dagql.NodeFunc("committerEmail", s.commitCommitterEmail).
			IsPersistable().
			Doc(`Git committer email.`),
		dagql.NodeFunc("message", s.commitMessage).
			IsPersistable().
			Doc(`Full commit message.`),
		dagql.NodeFunc("messageHeadline", s.commitMessageHeadline).
			IsPersistable().
			Doc(`First line of the commit message.`),
		dagql.NodeFunc("messageBody", s.commitMessageBody).
			IsPersistable().
			Doc(`Commit message body, excluding the headline.`),
		dagql.NodeFunc("parentShas", s.commitParentSHAs).
			IsPersistable().
			Doc(`Parent commit SHAs.`),
	}.Install(srv)
}

type gitArgs struct {
	URL string

	KeepGitDir              dagql.Optional[dagql.Boolean] `default:"false"`
	ExperimentalServiceHost dagql.Optional[core.ServiceID]

	SSHKnownHosts string                        `name:"sshKnownHosts" default:""`
	SSHAuthSocket dagql.Optional[core.SocketID] `name:"sshAuthSocket"`

	HTTPAuthUsername string                        `name:"httpAuthUsername" default:""`
	HTTPAuthToken    dagql.Optional[core.SecretID] `name:"httpAuthToken"`
	HTTPAuthHeader   dagql.Optional[core.SecretID] `name:"httpAuthHeader"`

	// internal args that can override the HEAD ref+commit
	Commit string `default:"" internal:"true"`
	Ref    string `default:"" internal:"true"`

	// SSHAuthSocketScoped indicates whether the SSHAuthSocket argument has been set
	// and is set to a Host._sshAuthSocket value (which is scoped by SSH key fingerprints
	// rather than client-specific paths). For instance, if the user provides an explicit
	// SSHAuthSocket arg but using Host.unixSocket, this will be false and indicate we
	// need to scope the cache key of the socket using Host._sshAuthSocket.
	SSHAuthSocketScoped bool `name:"sshAuthSocketScoped" default:"false" internal:"true"`
}

//nolint:gocyclo
func (s *gitSchema) git(ctx context.Context, parent dagql.ObjectResult[*core.Query], args gitArgs) (inst dagql.ObjectResult[*core.GitRepository], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}
	curCall := dagql.CurrentCall(ctx)
	if curCall == nil {
		return inst, fmt.Errorf("current call is nil")
	}

	var publicMetadata *gitutil.Remote
	var experimentalServiceHostID *call.ID
	if args.ExperimentalServiceHost.Valid {
		experimentalServiceHostID, err = args.ExperimentalServiceHost.Value.ID()
		if err != nil {
			return inst, fmt.Errorf("experimental service host ID: %w", err)
		}
	}
	var sshAuthSocketID *call.ID
	if args.SSHAuthSocket.Valid {
		sshAuthSocketID, err = args.SSHAuthSocket.Value.ID()
		if err != nil {
			return inst, fmt.Errorf("ssh auth socket ID: %w", err)
		}
	}
	var httpAuthTokenID *call.ID
	if args.HTTPAuthToken.Valid {
		httpAuthTokenID, err = args.HTTPAuthToken.Value.ID()
		if err != nil {
			return inst, fmt.Errorf("http auth token ID: %w", err)
		}
	}
	var httpAuthHeaderID *call.ID
	if args.HTTPAuthHeader.Valid {
		httpAuthHeaderID, err = args.HTTPAuthHeader.Value.ID()
		if err != nil {
			return inst, fmt.Errorf("http auth header ID: %w", err)
		}
	}

	remote, err := gitutil.ParseURL(args.URL)
	if errors.Is(err, gitutil.ErrUnknownProtocol) {
		candidates, candErr := gitutil.ParseCloneURL(args.URL)
		if candErr != nil {
			return inst, fmt.Errorf("failed to parse Git URL: %w", candErr)
		}
		try := make([][]dagql.NamedInput, 0, len(candidates))
		for _, candidate := range candidates {
			try = append(try, []dagql.NamedInput{
				{Name: "url", Value: dagql.NewString(candidate.String())},
			})
		}
		if args.Commit != "" {
			for i := range try {
				try[i] = append(try[i], dagql.NamedInput{
					Name:  "commit",
					Value: dagql.NewString(args.Commit),
				})
			}
		}
		if args.Ref != "" {
			for i := range try {
				try[i] = append(try[i], dagql.NamedInput{
					Name:  "ref",
					Value: dagql.NewString(args.Ref),
				})
			}
		}
		if args.KeepGitDir.Valid {
			for i := range try {
				try[i] = append(try[i], dagql.NamedInput{
					Name:  "keepGitDir",
					Value: dagql.Opt(args.KeepGitDir.Value),
				})
			}
		}
		if args.ExperimentalServiceHost.Valid {
			for i := range try {
				try[i] = append(try[i], dagql.NamedInput{
					Name:  "experimentalServiceHost",
					Value: dagql.Opt(dagql.NewID[*core.Service](experimentalServiceHostID)),
				})
			}
		}
		if args.SSHKnownHosts != "" {
			for i := range try {
				try[i] = append(try[i], dagql.NamedInput{
					Name:  "sshKnownHosts",
					Value: dagql.NewString(args.SSHKnownHosts),
				})
			}
		}
		if args.SSHAuthSocket.Valid {
			for i := range try {
				try[i] = append(try[i], dagql.NamedInput{
					Name:  "sshAuthSocket",
					Value: dagql.Opt(dagql.NewID[*core.Socket](sshAuthSocketID)),
				})
			}
		}
		if args.HTTPAuthUsername != "" {
			for i := range try {
				try[i] = append(try[i], dagql.NamedInput{
					Name:  "httpAuthUsername",
					Value: dagql.NewString(args.HTTPAuthUsername),
				})
			}
		}
		if args.HTTPAuthToken.Valid {
			for i := range try {
				try[i] = append(try[i], dagql.NamedInput{
					Name:  "httpAuthToken",
					Value: dagql.Opt(dagql.NewID[*core.Secret](httpAuthTokenID)),
				})
			}
		}
		if args.HTTPAuthHeader.Valid {
			for i := range try {
				try[i] = append(try[i], dagql.NamedInput{
					Name:  "httpAuthHeader",
					Value: dagql.Opt(dagql.NewID[*core.Secret](httpAuthHeaderID)),
				})
			}
		}
		if args.SSHAuthSocketScoped {
			for i := range try {
				try[i] = append(try[i], dagql.NamedInput{
					Name:  "sshAuthSocketScoped",
					Value: dagql.NewBoolean(true),
				})
			}
		}

		var accessErrors []error
		for _, selectArgs := range try {
			var repo dagql.ObjectResult[*core.GitRepository]
			err := srv.Select(ctx, parent, &repo, dagql.Selector{
				Field: "git",
				Args:  selectArgs,
				View:  curCall.View,
			})
			if err != nil {
				if errors.Is(err, gitutil.ErrGitAuthFailed) {
					accessErrors = append(accessErrors, err)
					continue
				}
				return inst, err
			}
			if _, err := repo.Self().LoadRemote(ctx); err != nil {
				if errors.Is(err, gitutil.ErrGitAuthFailed) {
					accessErrors = append(accessErrors, err)
					continue
				}
				return inst, err
			}
			return repo, nil
		}

		return inst, fmt.Errorf("cannot access Git repository: %w", errors.Join(accessErrors...))
	}
	if err != nil {
		return inst, fmt.Errorf("failed to parse Git URL: %w", err)
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata from context: %w", err)
	}

	var gitServices core.ServiceBindings
	if args.ExperimentalServiceHost.Valid {
		svc, err := args.ExperimentalServiceHost.Value.Load(ctx, srv)
		if err != nil {
			return inst, err
		}
		svcDig, err := svc.ContentPreferredDigest(ctx)
		if err != nil {
			return inst, fmt.Errorf("experimental service host digest: %w", err)
		}
		host, err := svc.Self().Hostname(ctx, svcDig)
		if err != nil {
			return inst, err
		}
		gitServices = append(gitServices, core.ServiceBinding{
			Service:  svc,
			Hostname: host,
		})
	}

	switch remote.Scheme {
	case gitutil.SSHProtocol:
		if remote.User == nil {
			// default to git user for SSH, otherwise weird incorrect defaults
			// like "root" can get applied in various places. This matches the
			// git module source implementation.
			remote.User = url.User("git")
		}

		if args.SSHAuthSocket.Valid {
			// A scoped socket is already an explicit, client-independent
			// input; it is loaded by __gitRepository below.
			if !args.SSHAuthSocketScoped {
				var scopedSock dagql.ObjectResult[*core.Socket]
				if err := srv.Select(ctx, srv.Root(), &scopedSock,
					dagql.Selector{
						Field: "host",
					},
					dagql.Selector{
						Field: "_sshAuthSocket",
						Args: []dagql.NamedInput{
							{
								Name:  "source",
								Value: dagql.Opt(dagql.NewID[*core.Socket](sshAuthSocketID)),
							},
						},
					},
				); err != nil {
					return inst, fmt.Errorf("failed to scope SSH auth socket: %w", err)
				}
				scopedSockID, err := scopedSock.ID()
				if err != nil {
					return inst, fmt.Errorf("scoped ssh auth socket ID: %w", err)
				}

				// reinvoke this API with the scoped socket as an explicit arg so it shows up in the DAG
				selectArgs := []dagql.NamedInput{
					{
						Name:  "url",
						Value: dagql.NewString(remote.String()),
					},
					{
						Name:  "sshAuthSocket",
						Value: dagql.Opt(dagql.NewID[*core.Socket](scopedSockID)),
					},
					{
						Name:  "sshAuthSocketScoped",
						Value: dagql.NewBoolean(true),
					},
				}
				if args.Commit != "" {
					selectArgs = append(selectArgs, dagql.NamedInput{
						Name:  "commit",
						Value: dagql.NewString(args.Commit),
					})
				}
				if args.Ref != "" {
					selectArgs = append(selectArgs, dagql.NamedInput{
						Name:  "ref",
						Value: dagql.NewString(args.Ref),
					})
				}
				if args.KeepGitDir.Valid {
					selectArgs = append(selectArgs, dagql.NamedInput{
						Name:  "keepGitDir",
						Value: dagql.Opt(args.KeepGitDir.Value),
					})
				}
				if args.ExperimentalServiceHost.Valid {
					selectArgs = append(selectArgs, dagql.NamedInput{
						Name:  "experimentalServiceHost",
						Value: dagql.Opt(dagql.NewID[*core.Service](experimentalServiceHostID)),
					})
				}
				if args.SSHKnownHosts != "" {
					selectArgs = append(selectArgs, dagql.NamedInput{
						Name:  "sshKnownHosts",
						Value: dagql.NewString(args.SSHKnownHosts),
					})
				}
				err = srv.Select(ctx, parent, &inst, dagql.Selector{
					Field: "git",
					Args:  selectArgs,
					View:  curCall.View,
				})
				return inst, err
			}
		} else {
			// No explicit socket: scope a default SSH auth socket from a client that
			// has one. Normally that's the current client; for trusted module
			// dependency/SDK resolution running under a nested client without a
			// socket (e.g. a codegen exec during `dagger generate`), fall back to the
			// session's originating client.
			sshSocketCtx := ctx
			sshAuthSocketPath := clientMetadata.SSHAuthSocketPath
			if sshAuthSocketPath == "" && core.IsModuleDependencyResolution(ctx) {
				mainClientMetadata, err := parent.Self().MainClientCallerMetadata(ctx)
				if err != nil {
					return inst, err
				}
				if mainClientMetadata.SSHAuthSocketPath != "" {
					sshSocketCtx = engine.ContextWithClientMetadata(ctx, mainClientMetadata)
					sshAuthSocketPath = mainClientMetadata.SSHAuthSocketPath
				}
			}
			if sshAuthSocketPath == "" {
				// A credential-free destination can be used by push, which asks
				// before borrowing the owner's agent. Reads still fail in setup.
				break
			}

			// Scope that client's default SSH auth socket and reinvoke so it appears in the DAG.
			var scopedSock dagql.ObjectResult[*core.Socket]
			if err := srv.Select(sshSocketCtx, srv.Root(), &scopedSock,
				dagql.Selector{
					Field: "host",
				},
				dagql.Selector{
					Field: "_sshAuthSocket",
				},
			); err != nil {
				return inst, fmt.Errorf("failed to select SSH auth socket: %w", err)
			}
			scopedSockID, err := scopedSock.ID()
			if err != nil {
				return inst, fmt.Errorf("scoped ssh auth socket ID: %w", err)
			}

			// reinvoke this API with the socket as an explicit arg so it shows up in the DAG
			selectArgs := []dagql.NamedInput{
				{
					Name:  "url",
					Value: dagql.NewString(remote.String()),
				},
				{
					Name:  "sshAuthSocket",
					Value: dagql.Opt(dagql.NewID[*core.Socket](scopedSockID)),
				},
				{
					Name:  "sshAuthSocketScoped",
					Value: dagql.NewBoolean(true),
				},
			}
			if args.Commit != "" {
				selectArgs = append(selectArgs, dagql.NamedInput{
					Name:  "commit",
					Value: dagql.NewString(args.Commit),
				})
			}
			if args.Ref != "" {
				selectArgs = append(selectArgs, dagql.NamedInput{
					Name:  "ref",
					Value: dagql.NewString(args.Ref),
				})
			}
			if args.KeepGitDir.Valid {
				selectArgs = append(selectArgs, dagql.NamedInput{
					Name:  "keepGitDir",
					Value: dagql.Opt(args.KeepGitDir.Value),
				})
			}
			if args.ExperimentalServiceHost.Valid {
				selectArgs = append(selectArgs, dagql.NamedInput{
					Name:  "experimentalServiceHost",
					Value: dagql.Opt(dagql.NewID[*core.Service](experimentalServiceHostID)),
				})
			}
			if args.SSHKnownHosts != "" {
				selectArgs = append(selectArgs, dagql.NamedInput{
					Name:  "sshKnownHosts",
					Value: dagql.NewString(args.SSHKnownHosts),
				})
			}
			err = srv.Select(ctx, parent, &inst, dagql.Selector{
				Field: "git",
				Args:  selectArgs,
				View:  curCall.View,
			})
			return inst, err
		}
	case gitutil.HTTPProtocol, gitutil.HTTPSProtocol:
		if !args.HTTPAuthToken.Valid && !args.HTTPAuthHeader.Valid {
			// For HTTP refs, try to load client credentials from the git helper.
			parentClientMetadata, err := parent.Self().NonModuleParentClientMetadata(ctx)
			if err != nil {
				return inst, err
			}

			// Determine which client(s) may supply implicit credentials. Arbitrary
			// git access from nested module runtime code must not implicitly use the
			// host's credentials, so by default we only do so when we ARE the
			// non-module caller. For trusted module dependency/SDK resolution we
			// additionally fall back to the session's originating client, since
			// codegen can run under a nested client (e.g. a git-less codegen exec
			// during `dagger generate`) that doesn't itself hold the user's
			// credentials.
			isTrustedDepResolution := core.IsModuleDependencyResolution(ctx)
			directCaller := clientMetadata.ClientID == parentClientMetadata.ClientID
			// A remote that an agent's model supplied as a tool argument may
			// also use the agent owner's credentials, approved by the owner when
			// a module drives the agent (see Server.AuthorizeGitRead). URLs with
			// their own userinfo keep it.
			agentAddress := !directCaller && !isTrustedDepResolution &&
				remote.User == nil && core.IsAgentAddressResolution(ctx)
			if !directCaller && !isTrustedDepResolution && !agentAddress {
				break
			}
			credClientMetadatas := []*engine.ClientMetadata{parentClientMetadata}
			if isTrustedDepResolution {
				mainClientMetadata, err := parent.Self().MainClientCallerMetadata(ctx)
				if err != nil {
					return inst, err
				}
				if mainClientMetadata.ClientID != parentClientMetadata.ClientID {
					credClientMetadatas = append(credClientMetadatas, mainClientMetadata)
				}
			}

			// start services if needed, before checking for auth
			var dnsConfig *oci.DNSConfig
			if len(gitServices) > 0 {
				svcs, err := parent.Self().Services(ctx)
				if err != nil {
					return inst, fmt.Errorf("failed to get services: %w", err)
				}
				detach, _, err := svcs.StartBindings(ctx, gitServices)
				if err != nil {
					return inst, err
				}
				defer detach()

				dnsConfig, err = core.DNSConfig(ctx)
				if err != nil {
					return inst, err
				}
			}

			metadata, err := cachedPublicRemote(netconfhttp.WithDNSConfig(ctx, dnsConfig), remote, len(gitServices) > 0)
			if err != nil {
				// A workspace pin may let child fields resolve without contacting
				// this repository. Don't fail the parent visibility probe when a
				// pin for this remote exists; skip implicit credentials and let any
				// operation that truly needs the remote surface its own error.
				if gitRemoteHasWorkspacePin(ctx, remote.Remote()) {
					break
				}
				return inst, err
			}
			if metadata != nil {
				publicMetadata = metadata
				break
			}
			if agentAddress {
				// Ask only now: public remotes need no credentials, so no approval.
				owner, err := parent.Self().AuthorizeGitRead(ctx, remote.Remote())
				if err != nil {
					return inst, err
				}
				credClientMetadatas = []*engine.ClientMetadata{owner}
			}

			// Retrieve credentials, trying each candidate client until one succeeds.
			for _, credClientMetadata := range credClientMetadatas {
				authCtx := engine.ContextWithClientMetadata(ctx, credClientMetadata)
				bk, err := parent.Self().Engine(authCtx)
				if err != nil {
					return inst, fmt.Errorf("failed to get engine client: %w", err)
				}
				credentials, err := bk.GetCredential(authCtx, remote.Scheme, remote.Host, remote.Path)
				if err != nil {
					// it's possible to provide auth tokens via chained API calls, so warn now but
					// don't fail. Auth will be checked again before relevant operations later.
					slog.Warn("Failed to retrieve git credentials", "error", err, "clientID", credClientMetadata.ClientID)
					continue
				}

				hash := sha256.Sum256([]byte(credentials.Password))
				secretName := hex.EncodeToString(hash[:])
				var authToken dagql.ObjectResult[*core.Secret]
				if err := srv.Select(authCtx, srv.Root(), &authToken,
					dagql.Selector{
						Field: "setSecret",
						Args: []dagql.NamedInput{
							{
								Name:  "name",
								Value: dagql.NewString(secretName),
							},
							{
								Name:  "plaintext",
								Value: dagql.NewString(credentials.Password),
							},
						},
					},
				); err != nil {
					return inst, fmt.Errorf("failed to create a new secret with the git auth token: %w", err)
				}
				authTokenID, err := authToken.ID()
				if err != nil {
					return inst, fmt.Errorf("git auth token ID: %w", err)
				}

				// reinvoke this API with the token as an explicit arg so it shows up in the DAG
				selectArgs := []dagql.NamedInput{
					{
						Name:  "url",
						Value: dagql.NewString(remote.String()),
					},
					{
						Name:  "httpAuthToken",
						Value: dagql.Opt(dagql.NewID[*core.Secret](authTokenID)),
					},
				}
				// Omit blank username; adding it would change the selector hash and kill cache hits.
				if credentials.Username != "" {
					selectArgs = append(selectArgs, dagql.NamedInput{
						Name:  "httpAuthUsername",
						Value: dagql.NewString(credentials.Username),
					})
				}
				if args.KeepGitDir.Valid {
					selectArgs = append(selectArgs, dagql.NamedInput{
						Name:  "keepGitDir",
						Value: dagql.Opt(args.KeepGitDir.Value),
					})
				}
				if args.Commit != "" {
					selectArgs = append(selectArgs, dagql.NamedInput{
						Name:  "commit",
						Value: dagql.NewString(args.Commit),
					})
				}
				if args.Ref != "" {
					selectArgs = append(selectArgs, dagql.NamedInput{
						Name:  "ref",
						Value: dagql.NewString(args.Ref),
					})
				}
				if args.ExperimentalServiceHost.Valid {
					selectArgs = append(selectArgs, dagql.NamedInput{
						Name:  "experimentalServiceHost",
						Value: dagql.Opt(dagql.NewID[*core.Service](experimentalServiceHostID)),
					})
				}
				err = srv.Select(ctx, parent, &inst, dagql.Selector{
					Field: "git",
					Args:  selectArgs,
					View:  curCall.View,
				})
				return inst, err
			}
			// no candidate client provided credentials; proceed unauthenticated
			break
		}
	}

	// Every implicit input is now explicit, so the repository no longer depends
	// on which client asked. Delegate to the client-independent constructor.
	err = srv.Select(ctx, parent, &inst, dagql.Selector{
		Field: "__gitRepository",
		Args:  gitRepositoryNamedInputs(remote, args, experimentalServiceHostID, sshAuthSocketID, httpAuthTokenID, httpAuthHeaderID),
		View:  curCall.View,
	})
	if err == nil && publicMetadata != nil {
		if backend, ok := inst.Self().Backend.(*core.RemoteGitRepository); ok {
			err = backend.PrimePublicRemote(ctx, publicMetadata)
		}
	}
	return inst, err
}

// gitPerClientInput is dagql.PerClientInput, except that resolving a remote an
// agent's model supplied gets a namespace of its own. Such a lookup may carry
// the agent owner's credentials (see core.WithAgentAddressResolution), so it
// must neither reuse the caller's own lookups of the same URL, which may have
// none, nor hand the owner's to them. It keeps PerClientInput's name, so
// every other lookup keeps its existing call digest.
var gitPerClientInput = agentAddressScopedInput(dagql.PerClientInput)

// agentAddressScopedInput wraps input, a cache input resolving to a string
// key, to give lookups of a model-supplied address (see
// core.WithAgentAddressResolution) a namespace of their own. It keeps input's
// name and, for every other lookup, its key, so their call digests don't
// change.
func agentAddressScopedInput(input dagql.ImplicitInput) dagql.ImplicitInput {
	return dagql.ImplicitInput{
		Name: input.Name,
		Resolver: func(ctx context.Context, args map[string]dagql.Input) (dagql.Input, error) {
			resolved, err := input.Resolver(ctx, args)
			if err != nil || !core.IsAgentAddressResolution(ctx) {
				return resolved, err
			}
			key, ok := resolved.(dagql.String)
			if !ok {
				return nil, fmt.Errorf("unexpected %s cache key %T", input.Name, resolved)
			}
			return dagql.NewString(key.String() + ":agent-address"), nil
		},
	}
}

// gitLockScopedInput scopes a ref lookup per client when its resolution can
// consult the calling client's workspace lock. The repository result is shared
// across clients, but which pin applies to a named ref (and whether a missing
// pin should be written) is per workspace, so clients must not share those
// lookups. A full commit SHA never consults the lock, so SHA lookups stay
// shared: that is what workspace snapshots pin their refs by. The same holds
// for revision suffixes applied to a full SHA (e.g. <sha>~2): only the base
// of a revision can consult the lock. noLock asks for a live resolution and
// gets a fresh key per call, except for a full SHA: it is immutable.
func gitLockScopedInput(argName string) dagql.ImplicitInput {
	perClient := gitLiveInput(dagql.PerClientInput)
	return dagql.ImplicitInput{
		Name: "cachePerClientLock:" + argName,
		Resolver: func(ctx context.Context, args map[string]dagql.Input) (dagql.Input, error) {
			if name, ok := args[argName].(dagql.String); ok {
				base := name.String()
				if rev, err := gitutil.ParseRevision(base); err == nil {
					base = rev.Base
				}
				if gitutil.IsCommitSHA(base) {
					return dagql.NewString(""), nil
				}
			}
			return perClient.Resolver(ctx, args)
		},
	}
}

// gitLiveInput wraps a lookup's cache input so that noLock: true, a request
// to resolve the ref live, gets a fresh key per call rather than the result of
// an earlier lookup. Without noLock the input's name and value are unchanged,
// so existing call digests are too. The resolver then lists the remote again
// (see core.ContextWithLiveGitRemote).
func gitLiveInput(input dagql.ImplicitInput) dagql.ImplicitInput {
	return dagql.PerCallWhen("noLock", input)
}

// gitRepositoryNamedInputs spells out the arguments for __gitRepository. Only
// set arguments are included so that equivalent requests produce the same call.
func gitRepositoryNamedInputs(remote *gitutil.GitURL, args gitArgs, serviceID, sshAuthSocketID, httpAuthTokenID, httpAuthHeaderID *call.ID) []dagql.NamedInput {
	inputs := []dagql.NamedInput{
		{Name: "url", Value: dagql.NewString(remote.String())},
	}
	if args.KeepGitDir.Valid {
		inputs = append(inputs, dagql.NamedInput{Name: "keepGitDir", Value: dagql.Opt(args.KeepGitDir.Value)})
	}
	if serviceID != nil {
		inputs = append(inputs, dagql.NamedInput{Name: "experimentalServiceHost", Value: dagql.Opt(dagql.NewID[*core.Service](serviceID))})
	}
	if args.SSHKnownHosts != "" {
		inputs = append(inputs, dagql.NamedInput{Name: "sshKnownHosts", Value: dagql.NewString(args.SSHKnownHosts)})
	}
	if sshAuthSocketID != nil {
		inputs = append(inputs, dagql.NamedInput{Name: "sshAuthSocket", Value: dagql.Opt(dagql.NewID[*core.Socket](sshAuthSocketID))})
	}
	if args.HTTPAuthUsername != "" {
		inputs = append(inputs, dagql.NamedInput{Name: "httpAuthUsername", Value: dagql.NewString(args.HTTPAuthUsername)})
	}
	if httpAuthTokenID != nil {
		inputs = append(inputs, dagql.NamedInput{Name: "httpAuthToken", Value: dagql.Opt(dagql.NewID[*core.Secret](httpAuthTokenID))})
	}
	if httpAuthHeaderID != nil {
		inputs = append(inputs, dagql.NamedInput{Name: "httpAuthHeader", Value: dagql.Opt(dagql.NewID[*core.Secret](httpAuthHeaderID))})
	}
	if args.Commit != "" {
		inputs = append(inputs, dagql.NamedInput{Name: "commit", Value: dagql.NewString(args.Commit)})
	}
	if args.Ref != "" {
		inputs = append(inputs, dagql.NamedInput{Name: "ref", Value: dagql.NewString(args.Ref)})
	}
	return inputs
}

// gitRepository constructs a remote repository from explicit inputs only. It
// performs no credential discovery: git resolves those per client and passes
// the results here, so this call is shared by every client with the same
// arguments.
func (s *gitSchema) gitRepository(ctx context.Context, parent dagql.ObjectResult[*core.Query], args gitArgs) (inst dagql.ObjectResult[*core.GitRepository], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	remote, err := gitutil.ParseURL(args.URL)
	if err != nil {
		return inst, fmt.Errorf("failed to parse Git URL: %w", err)
	}
	if remote.Scheme == gitutil.SSHProtocol && remote.User == nil {
		remote.User = url.User("git")
	}

	var gitServices core.ServiceBindings
	if args.ExperimentalServiceHost.Valid {
		svc, err := args.ExperimentalServiceHost.Value.Load(ctx, srv)
		if err != nil {
			return inst, err
		}
		svcDig, err := svc.ContentPreferredDigest(ctx)
		if err != nil {
			return inst, fmt.Errorf("experimental service host digest: %w", err)
		}
		host, err := svc.Self().Hostname(ctx, svcDig)
		if err != nil {
			return inst, err
		}
		gitServices = append(gitServices, core.ServiceBinding{
			Service:  svc,
			Hostname: host,
		})
	}

	var (
		sshAuthSock    dagql.ObjectResult[*core.Socket]
		httpAuthToken  dagql.ObjectResult[*core.Secret]
		httpAuthHeader dagql.ObjectResult[*core.Secret]
	)
	if args.SSHAuthSocket.Valid {
		sshAuthSock, err = args.SSHAuthSocket.Value.Load(ctx, srv)
		if err != nil {
			return inst, err
		}
	}
	if args.HTTPAuthToken.Valid {
		httpAuthToken, err = args.HTTPAuthToken.Value.Load(ctx, srv)
		if err != nil {
			return inst, err
		}
	}
	if args.HTTPAuthHeader.Valid {
		httpAuthHeader, err = args.HTTPAuthHeader.Value.Load(ctx, srv)
		if err != nil {
			return inst, err
		}
	}

	discardGitDir := false
	if args.KeepGitDir.Valid {
		discardGitDir = !args.KeepGitDir.Value.Bool()
	}

	var head *gitutil.Ref
	if args.Ref != "" || args.Commit != "" {
		head = &gitutil.Ref{
			Name: args.Ref,
			SHA:  args.Commit,
		}
	}

	var mirror dagql.ObjectResult[*core.RemoteGitMirror]
	if err := srv.Select(ctx, parent, &mirror, dagql.Selector{
		Field: "_remoteGitMirror",
		Args: []dagql.NamedInput{
			{Name: "remoteURL", Value: dagql.String(remote.Remote())},
		},
	}); err != nil {
		return inst, fmt.Errorf("failed to select remote git mirror: %w", err)
	}

	repo, err := core.NewGitRepository(ctx, &core.RemoteGitRepository{
		URL:           remote,
		SSHKnownHosts: args.SSHKnownHosts,
		SSHAuthSocket: sshAuthSock,
		AuthUsername:  args.HTTPAuthUsername,
		AuthToken:     httpAuthToken,
		AuthHeader:    httpAuthHeader,
		Services:      gitServices,
		Platform:      parent.Self().Platform(),
		Mirror:        mirror,
	})
	if err != nil {
		return inst, err
	}
	repo.Remote.Head = head
	repo.DiscardGitDir = discardGitDir

	return dagql.NewObjectResultForCurrentCall(ctx, srv, repo)
}

func calcGitContentDigest(gitRef *core.GitRef, args treeArgs) (digest.Digest, error) {
	if gitRef.Ref == nil {
		return "", fmt.Errorf("cannot content-address remote git tree: missing ref")
	}
	if gitRef.Ref.SHA == "" {
		return "", fmt.Errorf("cannot content-address remote git tree: ref %q has no resolved SHA", gitRef.Ref.Name)
	}

	repo := gitRef.Repo.Self()
	remoteRepo, ok := repo.Backend.(*core.RemoteGitRepository)
	if !ok {
		return "", fmt.Errorf("cannot content-address non-remote git tree")
	}

	keepsGitDir := !repo.DiscardGitDir && !args.DiscardGitDir

	dgstInputs := []string{
		// The remaining inputs (url + SHA + bool) also feed the GitRef and
		// GitCommit content digests; without a discriminator the three can
		// collide and the cache would serve one type where another is expected.
		"gitTree",

		// A commit SHA only identifies an object inside a Git object database.
		// The remote URL is part of the checkout source.
		remoteRepo.URL.Remote(),

		// The resolved commit selects the files to check out.
		gitRef.Ref.SHA,

		// The returned Directory may include or exclude .git based on both the
		// repository keepGitDir option and tree(discardGitDir: ...).
		strconv.FormatBool(keepsGitDir),
	}

	if keepsGitDir {
		dgstInputs = append(dgstInputs,
			// Depth changes retained git history. For example, `git log` sees one
			// commit at the default shallow depth but more with tree(depth: 5).
			strconv.Itoa(args.Depth),

			// includeTags changes which tag refs are populated under .git.
			strconv.FormatBool(args.IncludeTags),

			// ref.Name affects named-ref vs detached-SHA checkout metadata.
			gitRef.Ref.Name,
		)
		// Remote configuration is written to .git/config. Hash the same
		// merged configuration as the checkout so differing routing cannot
		// share a Directory, while equivalent registration orders still can.
		remotes := core.MergeGitRemotes(
			[]core.GitRemote{{Name: "origin", URL: remoteRepo.URL.Remote(), Implicit: true}},
			repo.Remotes,
		)
		if repo.UpstreamRemote != nil {
			remotes = core.MergeGitRemotes(nil, repo.Remotes)
			dgstInputs = append(dgstInputs, "upstreamRemote", *repo.UpstreamRemote)
		}
		dgstInputs = append(dgstInputs, "remotes", hashutil.HashStrings(gitRemoteDigestInputs(remotes)...).String())
	}

	return hashutil.HashStrings(dgstInputs...), nil
}

// cachedPublicRemote shares one probe and its advertisement per session. Git
// is per-client input, so a remote reached from both the CLI and a module's dependency resolution
// would otherwise be probed once per client. The probe sends no credentials,
// so the URL alone identifies the answer — except behind a service binding,
// where visibility depends on the service.
//
// A repository that turns private mid-session keeps its cached answer until the
// command ends, and credentials stay unattached until then. RemoteGitRepository
// .Remote already caches advertisements for one session; reusing this probe
// does not extend that freshness window.
func cachedPublicRemote(
	ctx context.Context,
	remote *gitutil.GitURL,
	serviceBound bool,
) (_ *gitutil.Remote, rerr error) {
	ctx, span := core.Tracer(ctx).Start(ctx, "git remote visibility", telemetry.Internal())
	defer telemetry.EndWithCause(span, &rerr)

	if serviceBound {
		return probePublicRemote(ctx, remote)
	}

	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return probePublicRemote(ctx, remote)
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("git remote visibility session metadata: %w", err)
	}

	cacheKey := hashutil.HashStrings("gitRemoteVisibility", clientMetadata.SessionID, remote.Remote()).String()
	cacheRes, err := cache.GetOrInitArbitrary(ctx, clientMetadata.SessionID, cacheKey, func(ctx context.Context) (any, error) {
		return probePublicRemote(ctx, remote)
	})
	if err != nil {
		return nil, err
	}
	metadata, ok := cacheRes.Value().(*gitutil.Remote)
	if !ok {
		return nil, fmt.Errorf("unexpected git remote visibility cache value type %T", cacheRes.Value())
	}
	return metadata, nil
}

// IsRemotePublic checks anonymous access without attaching caller credentials.
func IsRemotePublic(ctx context.Context, remote *gitutil.GitURL) (bool, error) {
	metadata, err := probePublicRemote(ctx, remote)
	return metadata != nil, err
}

// A nil advertisement means the repository requires authentication.
// Published advertisements are immutable in the session cache.
func probePublicRemote(ctx context.Context, remote *gitutil.GitURL) (*gitutil.Remote, error) {
	metadata, err := publicRemoteAdvertisement(ctx, remote)
	if err != nil {
		// Some Git hosts return a 200 HTML login page for unauthenticated refs: go-git reports ErrInvalidPktLen
		// treat as auth-required/private
		if errors.Is(err, pktline.ErrInvalidPktLen) {
			return nil, nil
		}
		// Azure Repos may also redirect unauthenticated private repository
		// probes to a sign-in endpoint instead of returning a Git transport
		// auth error.
		if strings.Contains(err.Error(), "http redirect:") && strings.Contains(err.Error(), "does not end with /info/refs") {
			return nil, nil
		}
		if errors.Is(err, transport.ErrAuthenticationRequired) {
			return nil, nil
		}
		// AzureDevops handling
		if strings.Contains(err.Error(), `target "/_signin" does not end`) {
			return nil, nil
		}

		return nil, err
	}
	return metadata, nil
}

type refArgs struct {
	Name          string
	NoLock        bool   `name:"noLock" default:"false"`
	Commit        string `default:"" internal:"true"`
	LockOperation string `default:"" internal:"true"`
	LockName      string `default:"" internal:"true"`
}

func gitLockInputs(repo *core.GitRepository, name string) ([]any, error) {
	remoteRepo, ok := repo.Backend.(*core.RemoteGitRepository)
	if !ok {
		return nil, fmt.Errorf("git locking only supports remote repositories")
	}
	return []any{remoteRepo.URL.Remote(), name}, nil
}

func gitRemoteHasWorkspacePin(ctx context.Context, remote string) bool {
	remote = workspace.NormalizeGitRemote(remote)
	if remote == "" {
		return false
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return false
	}
	lookupLock, err := lookupLockForAPI(ctx, query, workspace.LockOperationGitLatest)
	if err != nil || lookupLock == nil {
		return false
	}
	entries := lookupLock.lock.Entries()
	for _, entry := range entries {
		if entry.Namespace != workspace.CoreLockNamespace ||
			!strings.HasPrefix(entry.Operation, "git-") ||
			len(entry.Inputs) == 0 {
			continue
		}
		entryRemote, ok := entry.Inputs[0].(string)
		if ok && workspace.NormalizeGitRemote(entryRemote) == remote {
			return true
		}
	}
	return false
}

type gitBundleArgs struct {
	Refs []string
	Base dagql.Optional[core.GitRefID]
}

// gitBundleFileArgs adds the commits the named refs resolved to when the
// bundle was requested. The repository result is shared across sessions and
// the bundle is persisted, so without the pins a later request for a ref that
// has since moved would be answered with the earlier bundle.
type gitBundleFileArgs struct {
	Refs []string
	Base dagql.Optional[core.GitRefID]
	Pins []string `default:"[]"`
}

func gitBundleNamedInputs(args gitBundleArgs, pins []string) []dagql.NamedInput {
	refs := make(dagql.ArrayInput[dagql.String], len(args.Refs))
	for i, ref := range args.Refs {
		refs[i] = dagql.NewString(ref)
	}
	inputs := []dagql.NamedInput{
		{Name: "refs", Value: refs},
		{Name: "pins", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(pins...))},
	}
	if args.Base.Valid {
		inputs = append(inputs, dagql.NamedInput{Name: "base", Value: args.Base})
	}
	return inputs
}

func (s *gitSchema) bundle(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRepository],
	args gitBundleArgs,
) (inst dagql.ObjectResult[*core.GitBundle], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	pins, err := core.GitBundleRefPins(ctx, parent.Self(), args.Refs)
	if err != nil {
		return inst, err
	}
	var file dagql.ObjectResult[*core.File]
	if err := srv.Select(ctx, parent, &file, dagql.Selector{
		Field: "__bundleFile",
		Args:  gitBundleNamedInputs(args, pins),
	}); err != nil {
		return inst, err
	}
	bundle, err := core.ParseGitBundle(ctx, file)
	if err != nil {
		return inst, fmt.Errorf("parse created git bundle: %w", err)
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, bundle)
}

func (s *gitSchema) bundleFile(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRepository],
	args gitBundleFileArgs,
) (inst dagql.ObjectResult[*core.File], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	var base *core.GitRef
	if args.Base.Valid {
		loaded, err := args.Base.Value.Load(ctx, srv)
		if err != nil {
			return inst, fmt.Errorf("load git bundle base: %w", err)
		}
		parentDigest, err := parent.RecipeDigest(ctx)
		if err != nil {
			return inst, fmt.Errorf("read git bundle repository identity: %w", err)
		}
		baseDigest, err := loaded.Self().Repo.RecipeDigest(ctx)
		if err != nil {
			return inst, fmt.Errorf("read git bundle base repository identity: %w", err)
		}
		if parentDigest != baseDigest {
			return inst, fmt.Errorf("git bundle base must belong to the bundled repository")
		}
		base = loaded.Self()
	}
	file, err := core.CreateGitBundleFile(ctx, parent.Self(), args.Refs, base)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, file)
}

type gitWithBundleArgs struct {
	Bundle          core.GitBundleID
	PrerequisiteRef string `default:""`
}

func gitWithBundleNamedInputs(args gitWithBundleArgs) []dagql.NamedInput {
	return []dagql.NamedInput{
		{Name: "bundle", Value: args.Bundle},
		{Name: "prerequisiteRef", Value: dagql.NewString(args.PrerequisiteRef)},
	}
}

func (s *gitSchema) withBundle(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRepository],
	args gitWithBundleArgs,
) (inst dagql.ObjectResult[*core.GitRepository], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	var dir dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, parent, &dir, dagql.Selector{
		Field: "__withBundleDirectory",
		Args:  gitWithBundleNamedInputs(args),
	}); err != nil {
		return inst, err
	}
	return gitRepositoryWithContents(ctx, srv, parent, dir)
}

func (s *gitSchema) withBundleDirectory(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRepository],
	args gitWithBundleArgs,
) (inst dagql.ObjectResult[*core.Directory], rerr error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	bundle, err := args.Bundle.Load(ctx, srv)
	if err != nil {
		return inst, fmt.Errorf("load git bundle: %w", err)
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	dir := &core.Directory{
		Platform: query.Platform(),
		Dir:      new(core.LazyAccessor[string, *core.Directory]),
		Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory]),
		Lazy:     &core.DirectoryGitBundleImportLazy{LazyState: core.NewLazyState(), Repo: parent, Bundle: bundle, PrerequisiteRef: args.PrerequisiteRef},
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}

func (s *gitSchema) validateBundle(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitBundle],
	_ struct{},
) (inst dagql.ObjectResult[*core.GitBundle], _ error) {
	if err := core.ValidateGitBundle(ctx, parent.Self()); err != nil {
		return inst, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, parent.Self().Clone())
}

func (s *gitSchema) bundleAsFile(
	_ context.Context,
	parent dagql.ObjectResult[*core.GitBundle],
	_ struct{},
) (dagql.ObjectResult[*core.File], error) {
	if parent.Self().File.Self() == nil {
		return dagql.ObjectResult[*core.File]{}, fmt.Errorf("git bundle file is missing")
	}
	return parent.Self().File, nil
}

// revision resolves GitRepository.ref: a ref name, optionally followed by
// revision suffixes such as ~3 or ^2.
//
// The base name resolves exactly like a plain ref, workspace lock included:
// HEAD~3 pins (or writes) the same lock entry as HEAD. The full expression
// never gets a lock entry of its own: the walk from a pinned base is
// deterministic, so pinning the base pins the result. The result is a
// detached ref named by its SHA, like an expanded abbreviated SHA.
func (s *gitSchema) revision(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args refArgs) (inst dagql.Result[*core.GitRef], _ error) {
	if args.LockOperation != "" {
		// internal lookup with explicit lock handling: a plain ref name
		return s.ref(ctx, parent, args)
	}
	rev, err := gitutil.ParseRevision(args.Name)
	if err != nil {
		return inst, err
	}
	if !rev.HasSuffix() {
		return s.ref(ctx, parent, args)
	}
	if args.Commit != "" {
		// The caller already knows the commit the expression resolved to
		// (e.g. a pinned module version): honor it, like a pinned ref name.
		if !gitutil.IsCommitSHA(args.Commit) {
			return inst, fmt.Errorf("invalid commit SHA: %q", args.Commit)
		}
		return s.commitSHARef(ctx, parent, args.Commit)
	}

	base, err := s.ref(ctx, parent, refArgs{Name: rev.Base, NoLock: args.NoLock})
	if err != nil {
		return inst, fmt.Errorf("resolve %q: %w", rev.Expr, err)
	}
	if baseRepo := base.Self().Repo; resolvedThroughUpstream(parent.Self(), baseRepo.Self()) {
		// The base resolved through the upstream, so it is not in this
		// storage: walk the whole expression there, which fetches the history
		// the walk needs.
		return s.upstreamRef(ctx, baseRepo, refArgs{Name: args.Name, NoLock: args.NoLock})
	}
	walk := s.walkRevision
	if walk == nil {
		walk = func(ctx context.Context, repo *core.GitRepository, base *gitutil.Ref, rev gitutil.Revision) (string, error) {
			return repo.WalkRevision(ctx, base, rev)
		}
	}
	sha, err := walk(ctx, parent.Self(), base.Self().Ref, rev)
	if err != nil {
		return inst, err
	}
	return s.gitRefResult(ctx, parent, &gitutil.Ref{Name: sha, SHA: sha})
}

func (s *gitSchema) ref(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args refArgs) (inst dagql.Result[*core.GitRef], _ error) {
	if args.NoLock {
		ctx = withoutWorkspaceLookupLock(ctx)
	}
	repo := parent.Self()
	if args.Commit != "" && !gitutil.IsCommitSHA(args.Commit) {
		return inst, fmt.Errorf("invalid commit SHA: %q", args.Commit)
	}
	// Names this repository lacks may still resolve elsewhere (see
	// unresolvedRef), which applies its own lock handling: keep the caller's
	// request intact.
	upstreamArgs := args
	if args.LockOperation == "" && args.Commit == "" && !gitutil.IsCommitSHA(args.Name) {
		args.LockOperation = workspace.LockOperationGitSHA
		args.LockName = args.Name
	}
	if args.LockOperation != "" {
		if _, ok := repo.Backend.(*core.RemoteGitRepository); !ok {
			args.LockOperation = ""
		}
	}

	var (
		lockResolution lookupLockResolution
		lookupLock     *workspaceLookupLock
	)
	if args.Commit == "" && args.LockOperation != "" {
		query, err := core.CurrentQuery(ctx)
		if err != nil {
			return inst, err
		}
		lookupLock, err = lookupLockForAPI(ctx, query, args.LockOperation)
		if err != nil {
			return inst, err
		}
		lockInputs, err := gitLockInputs(repo, args.LockName)
		if err != nil {
			return inst, fmt.Errorf("%s lock inputs: %w", args.LockOperation, err)
		}
		lockResolution = resolveLookupFromLoadedLock(
			lookupLock,
			args.LockOperation,
			lockInputs,
		)
		if lockResolution.Pin != "" {
			lockedSHA := lockResolution.Pin
			if !gitutil.IsCommitSHA(lockedSHA) {
				return inst, fmt.Errorf("invalid locked commit SHA: %q", lockedSHA)
			}
			ref := &gitutil.Ref{
				Name: args.LockName,
				SHA:  lockedSHA,
			}
			return s.selectResolvedRef(ctx, parent, ref)
		}
	}

	if args.Commit == "" && gitutil.IsCommitSHA(args.Name) {
		return s.commitSHARef(ctx, parent, args.Name)
	}

	remote, err := repo.LoadRemote(ctx)
	if err != nil {
		return inst, err
	}
	ref, err := remote.Lookup(args.Name)
	if err != nil {
		return s.unresolvedRef(ctx, parent, upstreamArgs, err)
	}
	if args.Commit != "" && args.Commit != ref.SHA {
		ref.SHA = args.Commit
	}

	if args.Commit == "" && args.LockOperation != "" && lockResolution.ShouldWrite && lookupLock != nil {
		lockInputs, err := gitLockInputs(repo, args.LockName)
		if err != nil {
			return inst, fmt.Errorf("%s lock inputs: %w", args.LockOperation, err)
		}
		if err := lookupLock.SetLookup(
			workspace.CoreLockNamespace,
			args.LockOperation,
			lockInputs,
			ref.SHA,
		); err != nil {
			return inst, fmt.Errorf("set lock entry for %s: %w", args.LockOperation, err)
		}
	}

	return s.selectResolvedRef(ctx, parent, ref)
}

// unresolvedRef handles a name that matched no ref in the repository's own
// listing. args is the caller's original request.
func (s *gitSchema) unresolvedRef(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args refArgs, err error) (dagql.Result[*core.GitRef], error) {
	repo := parent.Self()
	// A name that looks like a hex prefix may be an abbreviated commit SHA
	// (tools routinely print short hashes): expand it against the locally
	// available object database, like `git rev-parse` would. Named refs always
	// win over prefixes, matching git's own precedence.
	if gitutil.IsCommitSHAPrefix(args.Name) {
		sha, shaErr := repo.ResolveShortSHA(ctx, args.Name)
		if shaErr == nil {
			return s.gitRefResult(ctx, parent, &gitutil.Ref{
				Name: sha,
				SHA:  sha,
			})
		}
		err = errors.Join(err, shaErr)
		if !errors.Is(shaErr, gitutil.ErrShortSHANotFound) {
			// An ambiguous prefix (or a failure to read the local objects)
			// is final, as in git: the upstream cannot disambiguate it.
			return dagql.Result[*core.GitRef]{}, err
		}
	}
	// Owned storage resolves names it lacks through the remote it was derived
	// from. Only a missing name falls through: a failure to list the local
	// refs is reported as-is.
	var notFound *gitutil.RefNotFoundError
	if upstream := ownedUpstream(repo); upstream.Self() != nil && errors.As(err, &notFound) {
		inst, uerr := s.upstreamRef(ctx, upstream, args)
		if uerr != nil {
			return inst, fmt.Errorf("ref %q not found locally; %w", args.Name, uerr)
		}
		return inst, nil
	}
	return dagql.Result[*core.GitRef]{}, err
}

// ownedUpstream returns the remote that owned storage resolves missing names
// and commits through, if any.
func ownedUpstream(repo *core.GitRepository) dagql.ObjectResult[*core.GitRepository] {
	if local, ok := repo.Backend.(*core.LocalGitRepository); ok {
		return local.Upstream
	}
	return dagql.ObjectResult[*core.GitRepository]{}
}

// resolvedThroughUpstream reports whether a ref of owned storage repo was
// resolved through its upstream, i.e. belongs to the remote instead.
func resolvedThroughUpstream(repo, refRepo *core.GitRepository) bool {
	if ownedUpstream(repo).Self() == nil || refRepo == nil {
		return false
	}
	_, remote := refRepo.Backend.(*core.RemoteGitRepository)
	return remote
}

// upstreamDescription names an upstream in errors, without credentials.
func upstreamDescription(upstream dagql.ObjectResult[*core.GitRepository]) string {
	if remote, ok := upstream.Self().Backend.(*core.RemoteGitRepository); ok && remote.URL != nil {
		return "upstream " + remote.URL.RedactedRemote()
	}
	return "upstream"
}

// missingCommitUpstream returns the upstream to resolve a full commit SHA
// through: set only for owned storage that does not contain the commit, so a
// SHA read from an upstream-resolved ref can be passed back in.
func missingCommitUpstream(ctx context.Context, repo *core.GitRepository, sha string) (dagql.ObjectResult[*core.GitRepository], error) {
	upstream := ownedUpstream(repo)
	if upstream.Self() == nil {
		return upstream, nil
	}
	has, err := repo.Backend.(*core.LocalGitRepository).HasCommit(ctx, sha)
	if err != nil || has {
		return dagql.ObjectResult[*core.GitRepository]{}, err
	}
	return upstream, nil
}

// commitSHARef resolves a full commit SHA: in this repository when it has the
// commit (or cannot resolve elsewhere), otherwise through its upstream.
func (s *gitSchema) commitSHARef(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], sha string) (dagql.Result[*core.GitRef], error) {
	upstream, err := missingCommitUpstream(ctx, parent.Self(), sha)
	if err != nil {
		return dagql.Result[*core.GitRef]{}, err
	}
	if upstream.Self() != nil {
		inst, err := s.upstreamRef(ctx, upstream, refArgs{Name: sha})
		if err != nil {
			return inst, fmt.Errorf("commit %s not found locally; %w", sha, err)
		}
		return inst, nil
	}
	return s.gitRefResult(ctx, parent, &gitutil.Ref{Name: sha, SHA: sha})
}

// upstreamRef resolves a name through the remote an owned repository was
// derived from. The result is the remote's own ref, so reading it fetches with
// the remote's authentication and service bindings; nothing is written into
// the owned storage.
func (s *gitSchema) upstreamRef(ctx context.Context, upstream dagql.ObjectResult[*core.GitRepository], args refArgs) (dagql.Result[*core.GitRef], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.Result[*core.GitRef]{}, err
	}
	selectArgs := []dagql.NamedInput{{Name: "name", Value: dagql.String(args.Name)}}
	if args.Commit != "" {
		selectArgs = append(selectArgs, dagql.NamedInput{Name: "commit", Value: dagql.String(args.Commit)})
	}
	if args.NoLock {
		selectArgs = append(selectArgs, dagql.NamedInput{Name: "noLock", Value: dagql.Boolean(true)})
	}
	if args.LockOperation != "" {
		selectArgs = append(selectArgs,
			dagql.NamedInput{Name: "lockOperation", Value: dagql.String(args.LockOperation)},
			dagql.NamedInput{Name: "lockName", Value: dagql.String(args.LockName)},
		)
	}
	var result dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, upstream, &result, dagql.Selector{Field: "ref", Args: selectArgs}); err != nil {
		return dagql.Result[*core.GitRef]{}, fmt.Errorf("%s: %w", upstreamDescription(upstream), err)
	}
	return result.Result, nil
}

type resolvedRefArgs struct {
	Name   string
	Commit string
}

func (s *gitSchema) resolvedRef(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args resolvedRefArgs) (dagql.Result[*core.GitRef], error) {
	if !gitutil.IsCommitSHA(args.Commit) {
		return dagql.Result[*core.GitRef]{}, fmt.Errorf("invalid commit SHA: %q", args.Commit)
	}
	return s.gitRefResult(ctx, parent, &gitutil.Ref{Name: args.Name, SHA: args.Commit})
}

func (s *gitSchema) selectResolvedRef(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], ref *gitutil.Ref) (dagql.Result[*core.GitRef], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.Result[*core.GitRef]{}, err
	}
	var result dagql.ObjectResult[*core.GitRef]
	err = srv.Select(ctx, parent, &result, dagql.Selector{Field: "__resolvedRef", Args: []dagql.NamedInput{
		{Name: "name", Value: dagql.String(ref.Name)},
		{Name: "commit", Value: dagql.String(ref.SHA)},
	}})
	return result.Result, err
}

func (s *gitSchema) gitRefResult(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], ref *gitutil.Ref) (inst dagql.Result[*core.GitRef], _ error) {
	repo := parent.Self()
	refBackend, err := repo.Backend.Get(ctx, ref)
	if err != nil {
		return inst, err
	}

	result := &core.GitRef{
		Repo:    parent,
		Ref:     ref,
		Backend: refBackend,
	}
	inst, err = dagql.NewResultForCurrentCall(ctx, result)
	if err != nil {
		return inst, err
	}

	// all the same as in git, but instead of the *remote* details, just use
	// the *ref* details
	// if the upstream remote changes in a ref we don't care about, it
	// shouldn't be mixed into the cache
	dgstInputs := []string{
		// Discriminate from the GitCommit and tree Directory digests, which
		// hash the same url + SHA + bool inputs for a different result type.
		"gitRef",
		repo.URL.Value.String(),
		string(ref.Digest()),
		strconv.FormatBool(repo.DiscardGitDir),
	}
	if len(repo.Remotes) > 0 {
		// The same commit with different remote routing is a different GitRef:
		// merging these results could send a push to the wrong destination.
		dgstInputs = append(dgstInputs, "remotes", hashutil.HashStrings(gitRemoteDigestInputs(repo.Remotes)...).String())
	}
	if repo.UpstreamRemote != nil {
		dgstInputs = append(dgstInputs, "upstreamRemote", *repo.UpstreamRemote)
	}
	if localRepo, ok := repo.Backend.(*core.LocalGitRepository); ok {
		// URL is empty for local repos, and a SHA alone doesn't identify the
		// repository state it was resolved in: two checkouts at the same
		// commit can differ in tags and remotes
		dirDgst, err := localRepo.Directory.ContentPreferredDigest(ctx)
		if err != nil {
			return inst, err
		}
		dgstInputs = append(dgstInputs, "localRepo", dirDgst.String())
		if localRepo.Upstream.Self() != nil {
			// The same storage can carry different authority to complete its
			// history or reopen its remote. Keep the exact source recipe.
			upstreamDigest, err := localRepo.Upstream.RecipeDigest(ctx)
			if err != nil {
				return inst, err
			}
			dgstInputs = append(dgstInputs, "upstreamCapability", upstreamDigest.String())
		}
	}
	if remoteRepo, ok := repo.Backend.(*core.RemoteGitRepository); ok {
		dgstInputs = append(dgstInputs, "authUsername", remoteRepo.AuthUsername)
		if remoteRepo.SSHAuthSocket.Self() != nil {
			dgstInputs = append(dgstInputs, "sshAuthSock", string(remoteRepo.SSHAuthSocket.Self().Handle))
		}
		if remoteRepo.AuthToken.Self() != nil {
			dgstInputs = append(dgstInputs, "authToken", string(remoteRepo.AuthToken.Self().Handle))
		}
		if remoteRepo.AuthHeader.Self() != nil {
			dgstInputs = append(dgstInputs, "authHeader", string(remoteRepo.AuthHeader.Self().Handle))
		}
	}
	inst, err = inst.WithContentDigest(ctx, hashutil.HashStrings(dgstInputs...))
	if err != nil {
		return inst, err
	}
	return inst, nil
}

type headArgs struct {
	NoLock bool `name:"noLock" default:"false"`
}

func (s *gitSchema) head(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args headArgs) (inst dagql.Result[*core.GitRef], _ error) {
	return s.ref(ctx, parent, refArgs{
		Name:          "HEAD",
		NoLock:        args.NoLock,
		LockOperation: workspace.LockOperationGitSHA,
		LockName:      "HEAD",
	})
}

type commitArgs struct {
	ID string
}

func supportsStrictRefs(ctx context.Context) bool {
	return core.Supports(ctx, "v0.19.0")
}

func (s *gitSchema) commit(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args commitArgs) (inst dagql.Result[*core.GitCommit], _ error) {
	if supportsStrictRefs(ctx) && !gitutil.IsCommitSHA(args.ID) {
		if !gitutil.IsCommitSHAPrefix(args.ID) {
			return inst, fmt.Errorf("invalid commit SHA: %q", args.ID)
		}
		// An abbreviated SHA: expand it to the full SHA of the single
		// matching commit, like `git rev-parse` would. Only locally available
		// objects can answer this; see GitRepository.ResolveShortSHA.
		sha, err := parent.Self().ResolveShortSHA(ctx, args.ID)
		if err != nil {
			upstream := ownedUpstream(parent.Self())
			if upstream.Self() == nil || !errors.Is(err, gitutil.ErrShortSHANotFound) {
				return inst, err
			}
			// Not a local commit: expand it against the upstream's fetched
			// objects instead, as GitRepository.ref does.
			sha, err = upstream.Self().ResolveShortSHA(ctx, args.ID)
			if err != nil {
				return inst, fmt.Errorf("commit %q not found locally; %s: %w", args.ID, upstreamDescription(upstream), err)
			}
			return s.gitCommitResult(ctx, upstream, &gitutil.Ref{Name: sha, SHA: sha})
		}
		args.ID = sha
	}
	ref, err := parent.Self().Remote.Lookup(args.ID)
	if err != nil {
		return inst, err
	}
	if gitutil.IsCommitSHA(ref.SHA) {
		upstream, err := missingCommitUpstream(ctx, parent.Self(), ref.SHA)
		if err != nil {
			return inst, err
		}
		if upstream.Self() != nil {
			// A commit only the upstream has (e.g. a SHA read from a ref
			// resolved through it) belongs to the upstream.
			return s.gitCommitResult(ctx, upstream, ref)
		}
	}
	return s.gitCommitResult(ctx, parent, ref)
}

func (s *gitSchema) commitRef(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args commitArgs) (inst dagql.Result[*core.GitRef], _ error) {
	if supportsStrictRefs(ctx) && !gitutil.IsCommitSHA(args.ID) && !gitutil.IsCommitSHAPrefix(args.ID) {
		return inst, fmt.Errorf("invalid commit SHA: %q", args.ID)
	}
	return s.ref(ctx, parent, refArgs{Name: args.ID})
}

type branchArgs refArgs

func (s *gitSchema) branch(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args branchArgs) (dagql.Result[*core.GitRef], error) {
	lockName := "refs/heads/" + strings.TrimPrefix(args.Name, "refs/heads/")
	if supportsStrictRefs(ctx) {
		args.Name = lockName
	}
	return s.ref(ctx, parent, refArgs{
		Name:          args.Name,
		NoLock:        args.NoLock,
		Commit:        args.Commit,
		LockOperation: workspace.LockOperationGitSHA,
		LockName:      lockName,
	})
}

type tagArgs refArgs

func (s *gitSchema) tag(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args tagArgs) (dagql.Result[*core.GitRef], error) {
	lockName := "refs/tags/" + strings.TrimPrefix(args.Name, "refs/tags/")
	if supportsStrictRefs(ctx) {
		args.Name = lockName
	}
	return s.ref(ctx, parent, refArgs{
		Name:          args.Name,
		NoLock:        args.NoLock,
		Commit:        args.Commit,
		LockOperation: workspace.LockOperationGitSHA,
		LockName:      lockName,
	})
}

type tagsArgs struct {
	Patterns dagql.Optional[dagql.ArrayInput[dagql.String]] `name:"patterns"`
}

func (s *gitSchema) tags(ctx context.Context, parent *core.GitRepository, args tagsArgs) (dagql.Array[dagql.String], error) {
	var patterns []string
	if args.Patterns.Valid {
		for _, pattern := range args.Patterns.Value {
			patterns = append(patterns, pattern.String())
		}
	}
	remote, err := parent.LoadRemote(ctx)
	if err != nil {
		return nil, err
	}
	return withUpstreamNames(ctx, parent, "tags", args.Patterns, remote.Filter(patterns).Tags().ShortNames())
}

type branchesArgs struct {
	Patterns dagql.Optional[dagql.ArrayInput[dagql.String]] `name:"patterns"`
}

func (s *gitSchema) branches(ctx context.Context, parent *core.GitRepository, args branchesArgs) (dagql.Array[dagql.String], error) {
	var patterns []string
	if args.Patterns.Valid {
		for _, pattern := range args.Patterns.Value {
			patterns = append(patterns, pattern.String())
		}
	}
	remote, err := parent.LoadRemote(ctx)
	if err != nil {
		return nil, err
	}
	return withUpstreamNames(ctx, parent, "branches", args.Patterns, remote.Filter(patterns).Branches().ShortNames())
}

// withUpstreamNames adds the names owned storage's upstream lists (via field,
// with the same patterns) to its own, sorted and without duplicates: ref
// resolves both, so a listing of local names alone would hide names ref
// accepts.
func withUpstreamNames(ctx context.Context, repo *core.GitRepository, field string, patterns dagql.Optional[dagql.ArrayInput[dagql.String]], local []string) (dagql.Array[dagql.String], error) {
	upstream := ownedUpstream(repo)
	if upstream.Self() == nil {
		return dagql.NewStringArray(local...), nil
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var selectArgs []dagql.NamedInput
	if patterns.Valid {
		selectArgs = append(selectArgs, dagql.NamedInput{Name: "patterns", Value: patterns})
	}
	var upstreamNames dagql.Array[dagql.String]
	if err := srv.Select(ctx, upstream, &upstreamNames, dagql.Selector{Field: field, Args: selectArgs}); err != nil {
		return nil, fmt.Errorf("list %s of %s: %w", field, upstreamDescription(upstream), err)
	}
	names := slices.Clone(local)
	for _, name := range upstreamNames {
		if !slices.Contains(local, name.String()) {
			names = append(names, name.String())
		}
	}
	slices.Sort(names)
	return dagql.NewStringArray(names...), nil
}

func (s *gitSchema) cleaned(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args struct{}) (inst dagql.ObjectResult[*core.Directory], rerr error) {
	local, ok := parent.Self().Backend.(*core.LocalGitRepository)
	if !ok {
		return parent.Self().Backend.Cleaned(ctx)
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	dir := &core.Directory{
		Platform: query.Platform(),
		Dir:      new(core.LazyAccessor[string, *core.Directory]),
		Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory]),
		Lazy:     &core.DirectoryGitCleanedLazy{LazyState: core.NewLazyState(), Repo: parent},
	}
	handedOff := false
	defer func() {
		if !handedOff {
			rerr = errors.Join(rerr, dir.OnRelease(context.WithoutCancel(ctx)))
		}
	}()
	unchanged, err := dir.Lazy.(*core.DirectoryGitCleanedLazy).EvaluateForCall(ctx, dir)
	if err != nil {
		return inst, err
	}
	if unchanged {
		return local.Directory, nil
	}
	inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
	if err != nil {
		return inst, err
	}
	handedOff = true
	return inst, nil
}

func (s *gitSchema) uncommitted(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args struct{}) (inst dagql.ObjectResult[*core.Changeset], _ error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	var cleaned dagql.ObjectResult[*core.Directory]
	var dirty dagql.ObjectResult[*core.Directory]

	dirty, err = parent.Self().Backend.Dirty(ctx)
	if err != nil {
		return inst, err
	}
	if dirty.Self() == nil {
		// clean repo, so just get head, there'll be no diff later
		if err := dag.Select(ctx, parent, &dirty,
			dagql.Selector{
				Field: "head",
			},
			dagql.Selector{
				Field: "tree",
			},
		); err != nil {
			return inst, fmt.Errorf("failed to select head tree for clean repo: %w", err)
		}
		cleaned = dirty
	} else {
		// wrapped in an internal field to get good caching behavior
		if err := dag.Select(ctx, parent, &cleaned, dagql.Selector{
			Field: "__cleaned",
		}); err != nil {
			return inst, fmt.Errorf("failed to select cleaned: %w", err)
		}
	}
	cleanedID, err := cleaned.ID()
	if err != nil {
		return inst, fmt.Errorf("cleaned directory ID: %w", err)
	}

	if err := dag.Select(ctx, dirty, &inst,
		dagql.Selector{
			Field: "changes",
			Args: []dagql.NamedInput{
				{
					Name:  "from",
					Value: dagql.NewID[*core.Directory](cleanedID),
				},
			},
		},
	); err != nil {
		return inst, fmt.Errorf("failed to select cleaned digest: %w", err)
	}
	return inst, nil
}

func (s *gitSchema) asWorkspace(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args workspaceArgs) (dagql.ObjectResult[*core.Workspace], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	var ref dagql.ObjectResult[*core.GitRef]
	if err := srv.Select(ctx, parent, &ref, dagql.Selector{Field: "head"}); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	var ws dagql.ObjectResult[*core.Workspace]
	if err := srv.Select(ctx, ref, &ws, dagql.Selector{Field: "asWorkspace", Args: []dagql.NamedInput{{Name: "cwd", Value: dagql.NewString(args.Cwd)}}}); err != nil {
		return ws, err
	}
	var changes dagql.ObjectResult[*core.Changeset]
	if err := srv.Select(ctx, parent, &changes, dagql.Selector{Field: "uncommitted"}); err != nil {
		return ws, err
	}
	id, err := changes.ID()
	if err != nil {
		return ws, err
	}
	var result dagql.ObjectResult[*core.Workspace]
	err = srv.Select(ctx, ws, &result, dagql.Selector{Field: "withChanges", Args: []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](id)}}})
	return result, err
}

func (s *gitSchema) gitRefAsWorkspace(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args workspaceArgs) (dagql.ObjectResult[*core.Workspace], error) {
	return syntheticWorkspaceFromGitRef(ctx, parent, args.Cwd)
}

type withAuthTokenArgs struct {
	Token core.SecretID
}

func (s *gitSchema) gitRefAsRepository(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], _ struct{}) (dagql.ObjectResult[*core.GitRepository], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.GitRepository]{}, err
	}
	if parent.Self().Ref == nil || parent.Self().Repo.Self() == nil {
		return dagql.ObjectResult[*core.GitRepository]{}, fmt.Errorf("git ref has no resolved repository")
	}
	repo := parent.Self().Repo.Self().CloneWithBackend(parent.Self().Repo.Self().Backend)
	pinnedRef := *parent.Self().Ref
	repo.Remote.Head = &pinnedRef
	return dagql.NewObjectResultForCurrentCall(ctx, srv, repo)
}

type gitWithContentsArgs struct {
	Directory dagql.ID[*core.Directory]
}

func (s *gitSchema) withContents(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args gitWithContentsArgs) (inst dagql.ObjectResult[*core.GitRepository], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	dir, err := args.Directory.Load(ctx, srv)
	if err != nil {
		return inst, err
	}
	// Retain the receiver's remote (or the remote it was itself derived from):
	// names the supplied storage lacks still resolve through it, with the
	// receiver's own authentication. Never inferred from the supplied storage.
	backend := &core.LocalGitRepository{Directory: dir, Upstream: core.GitUpstream(parent)}
	if err := backend.ValidateSelfContained(ctx); err != nil {
		return inst, err
	}
	repo, err := core.NewGitRepository(ctx, backend)
	if err != nil {
		return inst, err
	}
	repo.URL = parent.Self().URL
	if parent.Self().UpstreamRemote == nil && repo.UpstreamRemote != nil {
		// Supplied retained storage may already record a selection that its
		// detached HEAD cannot express. Explicit registrations still win.
		repo.Remotes = core.MergeGitRemotes(repo.Remotes, parent.Self().Remotes)
	} else {
		repo.Remotes = core.CloneGitRemotes(parent.Self().Remotes)
		repo.UpstreamRemote = parent.Self().UpstreamRemote
	}
	repo.DiscardGitDir = parent.Self().DiscardGitDir
	return dagql.NewObjectResultForCurrentCall(ctx, srv, repo)
}

func gitRepositoryWithContents(ctx context.Context, srv *dagql.Server, repo dagql.ObjectResult[*core.GitRepository], dir dagql.ObjectResult[*core.Directory]) (inst dagql.ObjectResult[*core.GitRepository], err error) {
	id, err := dir.ID()
	if err != nil {
		return inst, err
	}
	err = srv.Select(ctx, repo, &inst, dagql.Selector{Field: "withContents", Args: []dagql.NamedInput{{Name: "directory", Value: dagql.NewID[*core.Directory](id)}}})
	return inst, err
}

func (s *gitSchema) withAuthToken(ctx context.Context, parent *core.GitRepository, args withAuthTokenArgs) (*core.GitRepository, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	token, err := args.Token.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	if remote, ok := parent.Backend.(*core.RemoteGitRepository); ok {
		backend := &core.RemoteGitRepository{
			URL:           remote.URL,
			SSHKnownHosts: remote.SSHKnownHosts,
			SSHAuthSocket: remote.SSHAuthSocket,
			Services:      slices.Clone(remote.Services),
			Platform:      remote.Platform,
			AuthUsername:  remote.AuthUsername,
			AuthToken:     token,
			AuthHeader:    remote.AuthHeader,
			Mirror:        remote.Mirror,
		}
		return parent.CloneWithBackend(backend), nil
	}
	return parent, nil
}

type withAuthHeaderArgs struct {
	Header core.SecretID
}

func (s *gitSchema) withAuthHeader(ctx context.Context, parent *core.GitRepository, args withAuthHeaderArgs) (*core.GitRepository, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	header, err := args.Header.Load(ctx, srv)
	if err != nil {
		return nil, err
	}
	if remote, ok := parent.Backend.(*core.RemoteGitRepository); ok {
		backend := &core.RemoteGitRepository{
			URL:           remote.URL,
			SSHKnownHosts: remote.SSHKnownHosts,
			SSHAuthSocket: remote.SSHAuthSocket,
			Services:      slices.Clone(remote.Services),
			Platform:      remote.Platform,
			AuthUsername:  remote.AuthUsername,
			AuthToken:     remote.AuthToken,
			AuthHeader:    header,
			Mirror:        remote.Mirror,
		}
		return parent.CloneWithBackend(backend), nil
	}
	return parent, nil
}

// fullCheckout materializes the canonical full retained checkout, shared by
// public trees and workspaces, including repositories with keepGitDir=false.
// Its cache key depends on the resolved GitRef, never on workspace edits.
func (s *gitSchema) fullCheckout(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], _ struct{}) (inst dagql.ObjectResult[*core.Directory], err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	dir, err := evaluatedDirectory(ctx, query, &core.DirectoryGitTreeLazy{LazyState: core.NewLazyState(), Ref: parent, KeepGitDir: true})
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
}

type treeArgs struct {
	DiscardGitDir bool `default:"false"`
	Depth         int  `default:"1"`
	IncludeTags   bool `default:"false"`

	SSHKnownHosts dagql.Optional[dagql.String]  `name:"sshKnownHosts"`
	SSHAuthSocket dagql.Optional[core.SocketID] `name:"sshAuthSocket"`
}

// treeCacheKey canonicalizes a tree call's arguments, so that the spellings
// of one tree share one call, one cached result and so one snapshot: an
// argument at its default is dropped, and without .git, depth and
// includeTags shape nothing in the tree (only the history and tag refs
// under .git) and are dropped too, as is discardGitDir when the repository
// already discards .git. Snapshots of one tree built by separate calls share
// no layers, so a diff between their descendants (a workspace's changes, a
// merge's) would have to walk both trees in full.
func (s *gitSchema) treeCacheKey(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args treeArgs, req *dagql.CallRequest) error {
	canonicalGitTreeArgs(req, parent.Self().Repo.Self().DiscardGitDir, args.DiscardGitDir, args.Depth, args.IncludeTags)
	return nil
}

func (s *gitSchema) commitTreeCacheKey(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args commitTreeArgs, req *dagql.CallRequest) error {
	canonicalGitTreeArgs(req, parent.Self().Repo.Self().DiscardGitDir, args.DiscardGitDir, args.Depth, args.IncludeTags)
	return nil
}

func canonicalGitTreeArgs(req *dagql.CallRequest, repoDiscardsGitDir, discardGitDir bool, depth int, includeTags bool) {
	if repoDiscardsGitDir || discardGitDir {
		if repoDiscardsGitDir || !discardGitDir {
			req.DeleteArg("discardGitDir")
		}
		req.DeleteArg("depth")
		req.DeleteArg("includeTags")
		return
	}
	req.DeleteArg("discardGitDir")
	if depth == 1 {
		req.DeleteArg("depth")
	}
	if !includeTags {
		req.DeleteArg("includeTags")
	}
}

func (s *gitSchema) tree(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args treeArgs) (inst dagql.ObjectResult[*core.Directory], rerr error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	if args.SSHKnownHosts.Valid {
		return inst, fmt.Errorf("sshKnownHosts is no longer supported on `tree`")
	}
	if args.SSHAuthSocket.Valid {
		return inst, fmt.Errorf("sshAuthSocket is no longer supported on `tree`")
	}

	// Trees without .git are content-addressed independently of the ref name.
	// Record a pinned recipe before publishing that equivalence: otherwise a
	// later SHA-based selection (such as Workspace.snapshot) can reuse the
	// first writer's mutable branch recipe. Keep named refs for trees with .git,
	// where the ref name is part of the checkout metadata and content identity.
	ref := parent.Self()
	if (ref.Repo.Self().DiscardGitDir || args.DiscardGitDir) && ref.Ref.Name != ref.Ref.SHA {
		return pinnedGitTree(ctx, srv, ref.Repo, ref.Ref.SHA, args)
	}

	if !ref.Repo.Self().DiscardGitDir && !args.DiscardGitDir && args.Depth == 0 && !args.IncludeTags {
		// Use one materialization for full retained trees and workspace Git
		// metadata, even when callers spell the public default args differently.
		err = srv.Select(ctx, parent, &inst, dagql.Selector{Field: "__fullCheckout"})
	} else {
		var query *core.Query
		query, err = core.CurrentQuery(ctx)
		if err != nil {
			return inst, err
		}
		dir := &core.Directory{
			Platform: query.Platform(),
			Dir:      new(core.LazyAccessor[string, *core.Directory]),
			Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory]),
			Lazy:     &core.DirectoryGitTreeLazy{LazyState: core.NewLazyState(), Ref: parent, DiscardGitDir: args.DiscardGitDir, Depth: args.Depth, IncludeTags: args.IncludeTags},
		}
		inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
	}
	if err != nil {
		return inst, err
	}

	if _, ok := parent.Self().Repo.Self().Backend.(*core.RemoteGitRepository); ok {
		dgst, err := calcGitContentDigest(parent.Self(), args)
		if err != nil {
			return inst, err
		}
		// A full checkout is shared and already built: teach its digest directly.
		if lazy, ok := inst.Self().Lazy.(*core.DirectoryGitTreeLazy); ok && !lazy.KeepGitDir {
			lazy.ContentDigest = dgst
		} else {
			inst, err = inst.WithContentDigest(ctx, dgst)
			if err != nil {
				return inst, err
			}
		}
	}

	return inst, nil
}

func pinnedGitTree(ctx context.Context, srv *dagql.Server, repo dagql.ObjectResult[*core.GitRepository], sha string, args treeArgs) (inst dagql.ObjectResult[*core.Directory], err error) {
	err = srv.Select(ctx, repo, &inst,
		dagql.Selector{Field: "ref", Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.NewString(sha)},
		}},
		dagql.Selector{Field: "tree", Args: []dagql.NamedInput{
			{Name: "discardGitDir", Value: dagql.NewBoolean(args.DiscardGitDir)},
			{Name: "depth", Value: dagql.NewInt(args.Depth)},
			{Name: "includeTags", Value: dagql.NewBoolean(args.IncludeTags)},
		}},
	)
	return inst, err
}

func (s *gitSchema) targetCommit(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args struct{}) (inst dagql.Result[*core.GitCommit], _ error) {
	return s.gitCommitResult(ctx, parent.Self().Repo, parent.Self().Ref)
}

func (s *gitSchema) gitCommitResult(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], ref *gitutil.Ref) (inst dagql.Result[*core.GitCommit], _ error) {
	repo := parent.Self()
	refBackend, err := repo.Backend.Get(ctx, ref)
	if err != nil {
		return inst, err
	}

	result := &core.GitCommit{
		Repo:     parent,
		Ref:      &gitutil.Ref{SHA: ref.SHA},
		FetchRef: ref,
		Backend:  refBackend,
	}
	inst, err = dagql.NewResultForCurrentCall(ctx, result)
	if err != nil {
		return inst, err
	}

	dgstInputs := []string{
		// Discriminate from the GitRef and tree Directory digests, which hash
		// the same url + SHA + bool inputs for a different result type.
		"gitCommit",
		repo.URL.Value.String(),
		ref.SHA,
		strconv.FormatBool(repo.DiscardGitDir),
	}
	if len(repo.Remotes) > 0 {
		// GitCommit retains its repository, including its remote routing.
		dgstInputs = append(dgstInputs, "remotes", hashutil.HashStrings(gitRemoteDigestInputs(repo.Remotes)...).String())
	}
	if repo.UpstreamRemote != nil {
		dgstInputs = append(dgstInputs, "upstreamRemote", *repo.UpstreamRemote)
	}
	if localRepo, ok := repo.Backend.(*core.LocalGitRepository); ok {
		// URL is empty for local repos, and a SHA alone doesn't identify the
		// repository state it was resolved in: two checkouts at the same
		// commit can differ in tags and remotes, which releaseTag and
		// ancestorReleaseTag depend on
		dirDgst, err := localRepo.Directory.ContentPreferredDigest(ctx)
		if err != nil {
			return inst, err
		}
		dgstInputs = append(dgstInputs, "localRepo", dirDgst.String())
		if localRepo.Upstream.Self() != nil {
			// Commits retain the same source authority as refs.
			upstreamDigest, err := localRepo.Upstream.RecipeDigest(ctx)
			if err != nil {
				return inst, err
			}
			dgstInputs = append(dgstInputs, "upstreamCapability", upstreamDigest.String())
		}
	}
	if remoteRepo, ok := repo.Backend.(*core.RemoteGitRepository); ok {
		dgstInputs = append(dgstInputs, "authUsername", remoteRepo.AuthUsername)
		if remoteRepo.SSHAuthSocket.Self() != nil {
			dgstInputs = append(dgstInputs, "sshAuthSock", string(remoteRepo.SSHAuthSocket.Self().Handle))
		}
		if remoteRepo.AuthToken.Self() != nil {
			dgstInputs = append(dgstInputs, "authToken", string(remoteRepo.AuthToken.Self().Handle))
		}
		if remoteRepo.AuthHeader.Self() != nil {
			dgstInputs = append(dgstInputs, "authHeader", string(remoteRepo.AuthHeader.Self().Handle))
		}
	}
	inst, err = inst.WithContentDigest(ctx, hashutil.HashStrings(dgstInputs...))
	if err != nil {
		return inst, err
	}
	return inst, nil
}

type commitTreeArgs struct {
	DiscardGitDir bool `default:"false"`
	Depth         int  `default:"1"`
	IncludeTags   bool `default:"false"`
}

func (s *gitSchema) commitTree(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args commitTreeArgs) (inst dagql.ObjectResult[*core.Directory], rerr error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	// Share the pinned recipe with GitRef.tree, even when this commit was
	// originally selected through a mutable ref's targetCommit.
	commit := parent.Self()
	if commit.Repo.Self().DiscardGitDir || args.DiscardGitDir {
		return pinnedGitTree(ctx, srv, commit.Repo, commit.Ref.SHA, treeArgs{
			DiscardGitDir: args.DiscardGitDir,
			Depth:         args.Depth,
			IncludeTags:   args.IncludeTags,
		})
	}

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	dir := &core.Directory{
		Platform: query.Platform(),
		Dir:      new(core.LazyAccessor[string, *core.Directory]),
		Snapshot: new(core.LazyAccessor[bkcache.ImmutableRef, *core.Directory]),
		Lazy:     &core.DirectoryGitCommitTreeLazy{LazyState: core.NewLazyState(), Commit: parent, DiscardGitDir: args.DiscardGitDir, Depth: args.Depth, IncludeTags: args.IncludeTags},
	}
	inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
	if err != nil {
		return inst, err
	}

	if _, ok := parent.Self().Repo.Self().Backend.(*core.RemoteGitRepository); ok {
		ref := &core.GitRef{
			Repo:    parent.Self().Repo,
			Backend: parent.Self().Backend,
			Ref:     parent.Self().Ref,
		}
		dgst, err := calcGitContentDigest(ref, treeArgs{
			DiscardGitDir: args.DiscardGitDir,
			Depth:         args.Depth,
			IncludeTags:   args.IncludeTags,
		})
		if err != nil {
			return inst, err
		}
		dir.Lazy.(*core.DirectoryGitCommitTreeLazy).ContentDigest = dgst
	}

	return inst, nil
}

type commitChangesArgs struct {
	Against dagql.Optional[core.GitCommitID]
}

func (s *gitSchema) commitChanges(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args commitChangesArgs) (inst dagql.ObjectResult[*core.Changeset], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	var against dagql.ObjectResult[*core.GitCommit]
	if args.Against.Valid {
		against, err = args.Against.Value.Load(ctx, srv)
		if err != nil {
			return inst, fmt.Errorf("load comparison commit: %w", err)
		}
	} else {
		meta, err := parent.Self().Metadata(ctx)
		if err != nil {
			return inst, fmt.Errorf("read commit parents: %w", err)
		}
		if len(meta.ParentSHAs) > 0 {
			if err := srv.Select(ctx, parent.Self().Repo, &against, dagql.Selector{
				Field: "commit",
				Args:  []dagql.NamedInput{{Name: "id", Value: dagql.NewString(meta.ParentSHAs[0])}},
			}); err != nil {
				return inst, fmt.Errorf("select first parent: %w", err)
			}
		}
	}

	// Both sides are immutable, metadata-free trees. Selecting the existing
	// core operations retains their pinned recipes and persistence dependencies.
	tree := dagql.Selector{Field: "tree", Args: []dagql.NamedInput{
		{Name: "discardGitDir", Value: dagql.Boolean(true)},
	}}
	var before, after dagql.ObjectResult[*core.Directory]
	if against.Self() == nil {
		err = srv.Select(ctx, srv.Root(), &before, dagql.Selector{Field: "directory"})
	} else {
		err = srv.Select(ctx, against, &before, tree)
	}
	if err != nil {
		return inst, fmt.Errorf("select comparison tree: %w", err)
	}
	if err := srv.Select(ctx, parent, &after, tree); err != nil {
		return inst, fmt.Errorf("select commit tree: %w", err)
	}
	beforeID, err := before.ID()
	if err != nil {
		return inst, err
	}
	err = srv.Select(ctx, after, &inst, dagql.Selector{
		Field: "changes",
		Args:  []dagql.NamedInput{{Name: "from", Value: dagql.NewID[*core.Directory](beforeID)}},
	})
	return inst, err
}

func gitCommitMetadata(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit]) (*core.GitCommitMetadata, error) {
	return parent.Self().Metadata(ctx)
}

func (s *gitSchema) commitSHA(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args struct{}) (dagql.String, error) {
	meta, err := gitCommitMetadata(ctx, parent)
	if err != nil {
		return "", err
	}
	return dagql.NewString(meta.SHA), nil
}

func (s *gitSchema) commitShortSHA(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args struct{}) (dagql.String, error) {
	meta, err := gitCommitMetadata(ctx, parent)
	if err != nil {
		return "", err
	}
	return dagql.NewString(meta.ShortSHA), nil
}

func (s *gitSchema) commitAuthoredDate(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args struct{}) (dagql.String, error) {
	meta, err := gitCommitMetadata(ctx, parent)
	if err != nil {
		return "", err
	}
	return dagql.NewString(meta.AuthoredDate), nil
}

func (s *gitSchema) commitCommittedDate(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args struct{}) (dagql.String, error) {
	meta, err := gitCommitMetadata(ctx, parent)
	if err != nil {
		return "", err
	}
	return dagql.NewString(meta.CommittedDate), nil
}

func (s *gitSchema) commitAuthorName(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args struct{}) (dagql.String, error) {
	meta, err := gitCommitMetadata(ctx, parent)
	if err != nil {
		return "", err
	}
	return dagql.NewString(meta.AuthorName), nil
}

func (s *gitSchema) commitAuthorEmail(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args struct{}) (dagql.String, error) {
	meta, err := gitCommitMetadata(ctx, parent)
	if err != nil {
		return "", err
	}
	return dagql.NewString(meta.AuthorEmail), nil
}

func (s *gitSchema) commitCommitterName(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args struct{}) (dagql.String, error) {
	meta, err := gitCommitMetadata(ctx, parent)
	if err != nil {
		return "", err
	}
	return dagql.NewString(meta.CommitterName), nil
}

func (s *gitSchema) commitCommitterEmail(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args struct{}) (dagql.String, error) {
	meta, err := gitCommitMetadata(ctx, parent)
	if err != nil {
		return "", err
	}
	return dagql.NewString(meta.CommitterEmail), nil
}

func (s *gitSchema) commitMessage(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args struct{}) (dagql.String, error) {
	meta, err := gitCommitMetadata(ctx, parent)
	if err != nil {
		return "", err
	}
	return dagql.NewString(meta.Message), nil
}

func (s *gitSchema) commitMessageHeadline(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args struct{}) (dagql.String, error) {
	headline, err := parent.Self().MessageHeadline(ctx)
	if err != nil {
		return "", err
	}
	return dagql.NewString(headline), nil
}

func (s *gitSchema) commitMessageBody(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args struct{}) (dagql.String, error) {
	body, err := parent.Self().MessageBody(ctx)
	if err != nil {
		return "", err
	}
	return dagql.NewString(body), nil
}

func (s *gitSchema) commitParentSHAs(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args struct{}) (dagql.Array[dagql.String], error) {
	meta, err := gitCommitMetadata(ctx, parent)
	if err != nil {
		return nil, err
	}
	return dagql.NewStringArray(meta.ParentSHAs...), nil
}

type releaseTagArgs struct {
	IncludePreRelease bool `default:"false"`
}

type gitReleaseTag struct {
	RefName string
	SHA     string
	Version string
}

func (s *gitSchema) releaseTag(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitCommit],
	args releaseTagArgs,
) (dagql.Nullable[dagql.Result[*core.GitRef]], error) {
	return s.commitReleaseTag(ctx, parent, args.IncludePreRelease, false)
}

func (s *gitSchema) ancestorReleaseTag(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitCommit],
	args releaseTagArgs,
) (dagql.Nullable[dagql.Result[*core.GitRef]], error) {
	return s.commitReleaseTag(ctx, parent, args.IncludePreRelease, true)
}

func (s *gitSchema) commitReleaseTag(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitCommit],
	includePreRelease bool,
	ancestor bool,
) (dagql.Nullable[dagql.Result[*core.GitRef]], error) {
	none := dagql.Null[dagql.Result[*core.GitRef]]()

	tag, err := selectGitReleaseTag(ctx, parent.Self(), includePreRelease, ancestor)
	if err != nil {
		return none, err
	}
	if tag == nil {
		return none, nil
	}

	ref, err := s.selectResolvedRef(ctx, parent.Self().Repo, &gitutil.Ref{
		Name: tag.RefName,
		SHA:  tag.SHA,
	})
	if err != nil {
		return none, err
	}
	return dagql.NonNull(ref), nil
}

func selectGitReleaseTag(ctx context.Context, commit *core.GitCommit, includePreRelease bool, ancestor bool) (*gitReleaseTag, error) {
	if commit == nil || commit.Ref == nil || commit.Ref.SHA == "" {
		return nil, fmt.Errorf("git commit release tag: missing commit SHA")
	}
	if commit.Repo.Self() == nil || commit.Repo.Self().Backend == nil {
		return nil, fmt.Errorf("git commit release tag: missing repository")
	}
	if commit.Backend == nil {
		return nil, fmt.Errorf("git commit release tag: missing backend")
	}

	var remoteTags map[string]string
	if remoteRepo, ok := commit.Repo.Self().Backend.(*core.RemoteGitRepository); ok {
		// Commits are content-addressed by URL and SHA, so this commit may
		// carry a repository resolved by an earlier session, whose tags have
		// since moved. Re-resolve the remote rather than reading the snapshot
		// it was created with; the lookup is cached per session.
		remote, err := remoteRepo.Remote(ctx)
		if err != nil {
			return nil, err
		}
		remoteTags = remotePeeledTagRefs(remote)
	}

	depth := 1
	if ancestor {
		depth = 0
	}

	var selected *gitReleaseTag
	err := commit.Mount(ctx, depth, false, func(git *gitutil.GitCLI) error {
		localTags := map[string]string{}
		if _, ok := commit.Repo.Self().Backend.(*core.LocalGitRepository); ok {
			var err error
			localTags, err = localPeeledTagRefs(ctx, git)
			if err != nil {
				return err
			}

			// consulting the remote is best-effort: the mounted local repo has
			// none of the auth wiring a RemoteGitRepository carries, so a
			// private origin, an SSH remote, or an offline machine would all
			// fail here - answer from local tags instead
			remoteName, err := defaultGitFetchRemote(ctx, git)
			if err != nil {
				return err
			}
			if remoteName != "" {
				remote, err := git.LsRemote(ctx, remoteName)
				switch {
				case err == nil:
					remoteTags = remotePeeledTagRefs(remote)
				case ctx.Err() != nil:
					return context.Cause(ctx)
				default:
					slog.Warn("failed to list tags from git remote; using local tags only",
						"remote", remoteName, "error", err)
				}
			}
		}

		tags := reconcileGitTagRefs(localTags, remoteTags)
		tags = semverReleaseTags(tags, includePreRelease)
		sortGitReleaseTags(tags)

		if ancestor {
			selected = latestReachableGitReleaseTag(ctx, git, commit.Ref.SHA, tags)
		} else {
			selected = latestDirectGitReleaseTag(commit.Ref.SHA, tags)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return selected, nil
}

func localPeeledTagRefs(ctx context.Context, git *gitutil.GitCLI) (map[string]string, error) {
	out, err := git.Run(ctx,
		"for-each-ref",
		"--format=%(refname)%09%(objecttype)%09%(objectname)%09%(*objecttype)%09%(*objectname)",
		"refs/tags",
	)
	if err != nil {
		return nil, fmt.Errorf("list local git tags: %w", err)
	}

	tags := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		for len(fields) < 5 {
			fields = append(fields, "")
		}

		refName := fields[0]
		objectType := fields[1]
		objectSHA := fields[2]
		peeledType := fields[3]
		peeledSHA := fields[4]

		switch {
		case objectType == "commit" && gitutil.IsCommitSHA(objectSHA):
			tags[refName] = objectSHA
		case peeledType == "commit" && gitutil.IsCommitSHA(peeledSHA):
			tags[refName] = peeledSHA
		}
	}
	return tags, nil
}

func remotePeeledTagRefs(remote *gitutil.Remote) map[string]string {
	tags := map[string]string{}
	if remote == nil {
		return tags
	}

	peeled := map[string]string{}
	for _, ref := range remote.Refs {
		if ref == nil {
			continue
		}
		if tagName, ok := strings.CutSuffix(ref.Name, "^{}"); ok {
			if strings.HasPrefix(tagName, "refs/tags/") && gitutil.IsCommitSHA(ref.SHA) {
				peeled[tagName] = ref.SHA
			}
			continue
		}
		if strings.HasPrefix(ref.Name, "refs/tags/") && gitutil.IsCommitSHA(ref.SHA) {
			tags[ref.Name] = ref.SHA
		}
	}
	for name, sha := range peeled {
		tags[name] = sha
	}
	return tags
}

func defaultGitFetchRemote(ctx context.Context, git *gitutil.GitCLI) (string, error) {
	out, err := git.New(gitutil.WithIgnoreError()).Run(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(string(out))
	if branch != "" {
		out, err := git.New(gitutil.WithIgnoreError()).Run(ctx, "config", "--get", "branch."+branch+".remote")
		if err != nil {
			return "", err
		}
		if remote := strings.TrimSpace(string(out)); remote != "" {
			return remote, nil
		}
	}

	out, err = git.New(gitutil.WithIgnoreError()).Run(ctx, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(out)) == "" {
		return "", nil
	}
	return "origin", nil
}

func reconcileGitTagRefs(localTags, remoteTags map[string]string) []gitReleaseTag {
	byName := make(map[string]gitReleaseTag, len(localTags)+len(remoteTags))
	for name, sha := range localTags {
		byName[name] = gitReleaseTag{RefName: name, SHA: sha}
	}
	// the remote wins when a tag resolves differently: a local checkout may
	// simply predate a force-pushed tag, and that staleness shouldn't turn
	// into an error the remote itself would never produce
	for name, remoteSHA := range remoteTags {
		byName[name] = gitReleaseTag{RefName: name, SHA: remoteSHA}
	}

	tags := make([]gitReleaseTag, 0, len(byName))
	for _, tag := range byName {
		tags = append(tags, tag)
	}
	return tags
}

func semverReleaseTags(tags []gitReleaseTag, includePreRelease bool) []gitReleaseTag {
	releases := make([]gitReleaseTag, 0, len(tags))
	for _, tag := range tags {
		version := strings.TrimPrefix(tag.RefName, "refs/tags/")
		if !semver.IsValid(version) {
			continue
		}
		if !includePreRelease && semver.Prerelease(version) != "" {
			continue
		}
		tag.Version = version
		releases = append(releases, tag)
	}
	return releases
}

func sortGitReleaseTags(tags []gitReleaseTag) {
	slices.SortFunc(tags, func(a, b gitReleaseTag) int {
		if c := semver.Compare(a.Version, b.Version); c != 0 {
			return -c
		}
		return cmp.Compare(a.RefName, b.RefName)
	})
}

func latestDirectGitReleaseTag(commitSHA string, tags []gitReleaseTag) *gitReleaseTag {
	for i := range tags {
		if tags[i].SHA == commitSHA {
			return &tags[i]
		}
	}
	return nil
}

func latestReachableGitReleaseTag(ctx context.Context, git *gitutil.GitCLI, commitSHA string, tags []gitReleaseTag) *gitReleaseTag {
	for i := range tags {
		if tags[i].SHA == commitSHA {
			return &tags[i]
		}
		if isGitAncestor(ctx, git, tags[i].SHA, commitSHA) {
			return &tags[i]
		}
	}
	return nil
}

func isGitAncestor(ctx context.Context, git *gitutil.GitCLI, ancestorSHA, commitSHA string) bool {
	_, err := git.Run(ctx, "merge-base", "--is-ancestor", ancestorSHA, commitSHA)
	return err == nil
}

func (s *gitSchema) fetchCommit(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	args struct{},
) (dagql.String, error) {
	return dagql.NewString(parent.Self().Ref.SHA), nil
}

func (s *gitSchema) fetchRef(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	args struct{},
) (dagql.String, error) {
	return dagql.NewString(cmp.Or(parent.Self().Ref.Name, parent.Self().Ref.SHA)), nil
}

type mergeBaseArgs struct {
	Other core.GitRefID
}

func (s *gitSchema) commonAncestor(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	args mergeBaseArgs,
) (inst dagql.ObjectResult[*core.GitRef], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}
	other, err := args.Other.Load(ctx, srv)
	if err != nil {
		return inst, err
	}

	result, err := core.MergeBase(ctx, parent.Self(), other.Self())
	if err != nil {
		return inst, err
	}
	return dagql.NewObjectResultForCurrentCall(ctx, srv, result)
}

type gitLogArgs struct {
	Limit int `default:"10"`
	Paths dagql.Optional[dagql.ArrayInput[dagql.String]]
	Base  dagql.Optional[core.GitRefID]
}

func (s *gitSchema) log(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	args gitLogArgs,
) (dagql.ObjectResultArray[*core.GitCommit], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	opts := core.GitLogOptions{Limit: args.Limit}
	if args.Paths.Valid {
		for _, path := range args.Paths.Value {
			opts.Paths = append(opts.Paths, path.String())
		}
	}
	if args.Base.Valid {
		base, err := args.Base.Value.Load(ctx, srv)
		if err != nil {
			return nil, err
		}
		opts.Base = base.Self()
	}

	metas, err := parent.Self().Log(ctx, opts)
	if err != nil {
		return nil, err
	}

	// build each element by selecting the repository's commit field, so a log
	// entry is the same object, with the same ID and cache entry, as the commit
	// looked up directly by its SHA
	commits := make(dagql.ObjectResultArray[*core.GitCommit], 0, len(metas))
	for _, meta := range metas {
		var commit dagql.ObjectResult[*core.GitCommit]
		if err := srv.Select(ctx, parent.Self().Repo, &commit, dagql.Selector{
			Field: "commit",
			Args: []dagql.NamedInput{
				{Name: "id", Value: dagql.String(meta.SHA)},
			},
		}); err != nil {
			return nil, fmt.Errorf("git log: load commit %s: %w", meta.SHA, err)
		}
		commit.Self().PrefillMetadata(meta)
		commits = append(commits, commit)
	}
	return commits, nil
}

type latestArgs struct {
	Version   string `default:""`
	TagPrefix string `name:"tagPrefix" default:""`
	NoLock    bool   `name:"noLock" default:"false"`
}

func (s *gitSchema) latest(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRepository],
	args latestArgs,
) (inst dagql.Result[*core.GitRef], _ error) {
	if args.NoLock {
		ctx = withoutWorkspaceLookupLock(ctx)
	}
	repo := parent.Self()
	remoteRepo, isRemote := repo.Backend.(*core.RemoteGitRepository)
	if !isRemote {
		remote, err := repo.LoadRemote(ctx)
		if err != nil {
			return inst, err
		}
		ref, err := core.SelectGitRefWithVersionQuery(
			remote,
			args.TagPrefix,
			args.Version,
		)
		if err != nil {
			return inst, err
		}
		return s.selectResolvedRef(ctx, parent, ref)
	}

	var lockOptions []workspace.LookupOption
	if args.TagPrefix != "" {
		lockOptions = append(lockOptions, workspace.LookupOption{
			Name:  "tagPrefix",
			Value: args.TagPrefix,
		})
	}
	if args.Version != "" {
		lockOptions = append(lockOptions, workspace.LookupOption{
			Name:  "version",
			Value: args.Version,
		})
	}
	lockInputs := workspace.LookupInputs(
		[]any{remoteRepo.URL.Remote()},
		lockOptions...,
	)

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return inst, err
	}
	lookupLock, err := lookupLockForAPI(ctx, query, workspace.LockOperationGitLatest)
	if err != nil {
		return inst, err
	}

	lockResolution := resolveLookupFromLoadedLock(
		lookupLock,
		workspace.LockOperationGitLatest,
		lockInputs,
	)
	var selectedRef string
	if lockResolution.Pin != "" {
		selectedRef = lockResolution.Pin
		if err := core.ValidateGitVersionRef(
			selectedRef,
			args.TagPrefix,
			args.Version,
		); err != nil {
			return inst, fmt.Errorf("%s lock value: %w", workspace.LockOperationGitLatest, err)
		}
	} else {
		remote, err := repo.LoadRemote(ctx)
		if err != nil {
			return inst, err
		}
		ref, err := core.SelectGitRefWithVersionQuery(
			remote,
			args.TagPrefix,
			args.Version,
		)
		if err != nil {
			return inst, err
		}
		selectedRef = ref.Name

		if lockResolution.ShouldWrite && lookupLock != nil {
			if err := lookupLock.SetLookup(
				workspace.CoreLockNamespace,
				workspace.LockOperationGitLatest,
				lockInputs,
				selectedRef,
			); err != nil {
				return inst, fmt.Errorf("set lock entry for %s: %w", workspace.LockOperationGitLatest, err)
			}
		}
	}

	return s.ref(ctx, parent, refArgs{
		Name:          selectedRef,
		NoLock:        args.NoLock,
		LockOperation: workspace.LockOperationGitSHA,
		LockName:      selectedRef,
	})
}
