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
	"github.com/dagger/dagger/engine/sources/netconfhttp"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	"github.com/opencontainers/go-digest"
	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/util/gitutil"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
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

type gitSchema struct{}

func (s *gitSchema) Install(srv *dagql.Server) {
	dagql.Fields[*core.Query]{
		dagql.NodeFunc("git", s.git).
			WithInput(dagql.PerClientInput).
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
	}.Install(srv)

	dagql.Fields[*core.GitRepository]{
		dagql.NodeFunc("head", s.head).
			Doc(`Returns details for HEAD.`),
		dagql.NodeFunc("ref", s.ref).
			Doc(`Returns details of a ref.`).
			Args(
				dagql.Arg("name").Doc(`Ref's name (can be a commit identifier, a tag name, a branch name, or a fully-qualified ref).`),
			),
		dagql.NodeFunc("branch", s.branch).
			View(AllVersion).
			Doc(`Returns details of a branch.`).
			Args(
				dagql.Arg("name").Doc(`Branch's name (e.g., "main").`),
			),
		dagql.NodeFunc("tag", s.tag).
			View(AllVersion).
			Doc(`Returns details of a tag.`).
			Args(
				dagql.Arg("name").Doc(`Tag's name (e.g., "v0.3.9").`),
			),
		dagql.NodeFunc("commit", s.commit).
			View(AfterVersion("v1.0.0-0")).
			Doc(`Returns details of a commit.`).
			Args(
				// TODO: id is normally a reserved word; we should probably rename this
				dagql.Arg("id").Doc(`Identifier of the commit (e.g., "b6315d8f2810962c601af73f86831f6866ea798b").`),
			),
		dagql.NodeFunc("commit", s.commitRef).
			View(BeforeVersion("v1.0.0-0")).
			Doc(`Returns details of a commit.`).
			Args(
				// TODO: id is normally a reserved word; we should probably rename this
				dagql.Arg("id").Doc(`Identifier of the commit (e.g., "b6315d8f2810962c601af73f86831f6866ea798b").`),
			),
		dagql.NodeFunc("latest", s.latest).
			View(AfterVersion("v1.0.0-0")).
			Doc(
				`Return the latest stable release tag, falling back to HEAD when no release exists.`,
				`Release selection accepts an optional "v" prefix, incomplete versions, and zero-padded numeric components. This operation is pinned.`,
			).
			Args(
				dagql.Arg("tagPrefix").
					Doc(`Restrict release tags to a monorepo subpath.`).
					Internal(),
			),

		dagql.Func("tags", s.tags).
			Doc(`tags that match any of the given glob patterns.`).
			Args(
				dagql.Arg("patterns").Doc(`Glob patterns (e.g., "refs/tags/v*").`),
			),
		dagql.Func("branches", s.branches).
			Doc(`branches that match any of the given glob patterns.`).
			Args(
				dagql.Arg("patterns").Doc(`Glob patterns (e.g., "refs/tags/v*").`),
			),

		dagql.NodeFunc("__cleaned", s.cleaned).
			IsPersistable().
			Doc(`(Internal-only) Cleans the git repository by removing untracked files and resetting modifications.`),
		dagql.NodeFunc("uncommitted", s.uncommitted).
			Doc("Returns the changeset of uncommitted changes in the git repository."),
		dagql.NodeFunc("asWorkspace", s.asWorkspace).
			View(AfterVersion("v1.0.0-0")).
			Doc("Creates a synthetic workspace from this git repository.").
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
		dagql.NodeFunc("targetCommit", s.targetCommit).
			View(AfterVersion("v1.0.0-0")).
			Doc(`The commit this ref resolves to.`),
		dagql.NodeFunc("commitSHA", s.fetchCommit).
			IsPersistable().
			View(AfterVersion("v1.0.0-0")).
			Doc(`The resolved commit SHA at this ref.`),
		dagql.NodeFunc("tree", s.tree).
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
		dagql.NodeFunc("asWorkspace", s.gitRefAsWorkspace).
			View(AfterVersion("v1.0.0-0")).
			Doc("Creates a synthetic workspace from this git ref.").
			Args(
				dagql.Arg("cwd").Doc("Current working directory inside the workspace root. Defaults to the workspace root."),
			),
	}.Install(srv)

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
		dagql.NodeFunc("tree", s.commitTree).
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

		for _, selectArgs := range try {
			var repo dagql.ObjectResult[*core.GitRepository]
			err := srv.Select(ctx, parent, &repo, dagql.Selector{
				Field: "git",
				Args:  selectArgs,
				View:  curCall.View,
			})
			if err != nil {
				if errors.Is(err, gitutil.ErrGitAuthFailed) {
					continue
				}
				return inst, err
			}
			if _, err := repo.Self().LoadRemote(ctx); err != nil {
				if errors.Is(err, gitutil.ErrGitAuthFailed) {
					continue
				}
				return inst, err
			}
			return repo, nil
		}

		return inst, fmt.Errorf("failed to determine Git URL protocol")
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

	var (
		sshAuthSock    dagql.ObjectResult[*core.Socket]
		httpAuthToken  dagql.ObjectResult[*core.Secret]
		httpAuthHeader dagql.ObjectResult[*core.Secret]
	)

	switch remote.Scheme {
	case gitutil.SSHProtocol:
		if remote.User == nil {
			// default to git user for SSH, otherwise weird incorrect defaults
			// like "root" can get applied in various places. This matches the
			// git module source implementation.
			remote.User = url.User("git")
		}

		if args.SSHAuthSocket.Valid {
			if args.SSHAuthSocketScoped {
				sshAuthSock, err = args.SSHAuthSocket.Value.Load(ctx, srv)
				if err != nil {
					return inst, err
				}
			} else {
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
				return inst, fmt.Errorf("%w: SSH URLs are not supported without an SSH socket", gitutil.ErrGitAuthFailed)
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
		if httpAuthToken.Self() == nil && httpAuthHeader.Self() == nil {
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
			if clientMetadata.ClientID != parentClientMetadata.ClientID && !isTrustedDepResolution {
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

			public, err := IsRemotePublic(netconfhttp.WithDNSConfig(ctx, dnsConfig), remote)
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
			if public {
				break
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

	inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, repo)
	if err != nil {
		return inst, err
	}

	return inst, nil
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
	}

	return hashutil.HashStrings(dgstInputs...), nil
}

func IsRemotePublic(ctx context.Context, remote *gitutil.GitURL) (bool, error) {
	// check if repo is public
	repo := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
		Name: "origin",
		URLs: []string{remote.Remote()},
	})
	_, err := repo.ListContext(ctx, &git.ListOptions{Auth: nil})
	if err != nil {
		// Some Git hosts return a 200 HTML login page for unauthenticated refs: go-git reports ErrInvalidPktLen
		// treat as auth-required/private
		if errors.Is(err, pktline.ErrInvalidPktLen) {
			return false, nil
		}
		// Azure Repos may also redirect unauthenticated private repository
		// probes to a sign-in endpoint instead of returning a Git transport
		// auth error.
		if strings.Contains(err.Error(), "http redirect:") && strings.Contains(err.Error(), "does not end with /info/refs") {
			return false, nil
		}
		if errors.Is(err, transport.ErrAuthenticationRequired) {
			return false, nil
		}
		// AzureDevops handling
		if strings.Contains(err.Error(), `target "/_signin" does not end`) {
			return false, nil
		}

		return false, err
	}
	return true, nil
}

type refArgs struct {
	Name          string
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
		if ok && entryRemote == remote {
			return true
		}
	}
	return false
}

func (s *gitSchema) ref(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args refArgs) (inst dagql.Result[*core.GitRef], _ error) {
	repo := parent.Self()
	if args.Commit != "" && !gitutil.IsCommitSHA(args.Commit) {
		return inst, fmt.Errorf("invalid commit SHA: %q", args.Commit)
	}
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
			return s.gitRefResult(ctx, parent, ref)
		}
	}

	if args.Commit == "" && gitutil.IsCommitSHA(args.Name) {
		return s.gitRefResult(ctx, parent, &gitutil.Ref{
			Name: args.Name,
			SHA:  args.Name,
		})
	}

	remote, err := repo.LoadRemote(ctx)
	if err != nil {
		return inst, err
	}
	ref, err := remote.Lookup(args.Name)
	if err != nil {
		return inst, err
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

	return s.gitRefResult(ctx, parent, ref)
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
		repo.URL.Value.String(),
		string(ref.Digest()),
		strconv.FormatBool(repo.DiscardGitDir),
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
	}
	if remoteRepo, ok := repo.Backend.(*core.RemoteGitRepository); ok {
		if remoteRepo.SSHAuthSocket.Self() != nil {
			dgstInputs = append(dgstInputs, "sshAuthSock", string(remoteRepo.SSHAuthSocket.Self().Handle))
		}
		if remoteRepo.AuthToken.Self() != nil {
			dgstInputs = append(dgstInputs, "authToken", strconv.FormatBool(remoteRepo.AuthToken.Self() != nil))
		}
		if remoteRepo.AuthHeader.Self() != nil {
			dgstInputs = append(dgstInputs, "authHeader", strconv.FormatBool(remoteRepo.AuthHeader.Self() != nil))
		}
	}
	inst, err = inst.WithContentDigest(ctx, hashutil.HashStrings(dgstInputs...))
	if err != nil {
		return inst, err
	}
	return inst, nil
}

func (s *gitSchema) head(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args struct{}) (inst dagql.Result[*core.GitRef], _ error) {
	return s.ref(ctx, parent, refArgs{
		Name:          "HEAD",
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
		return inst, fmt.Errorf("invalid commit SHA: %q", args.ID)
	}
	ref, err := parent.Self().Remote.Lookup(args.ID)
	if err != nil {
		return inst, err
	}
	return s.gitCommitResult(ctx, parent, ref)
}

func (s *gitSchema) commitRef(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args commitArgs) (inst dagql.Result[*core.GitRef], _ error) {
	if supportsStrictRefs(ctx) && !gitutil.IsCommitSHA(args.ID) {
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
	return dagql.NewStringArray(remote.Filter(patterns).Tags().ShortNames()...), nil
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
	return dagql.NewStringArray(remote.Filter(patterns).Branches().ShortNames()...), nil
}

func (s *gitSchema) cleaned(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args struct{}) (inst dagql.ObjectResult[*core.Directory], _ error) {
	dir, err := parent.Self().Backend.Cleaned(ctx)
	if err != nil {
		return inst, err
	}
	return dir, nil
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
	return syntheticWorkspaceFromGitRef(ctx, ref, args.Cwd)
}

func (s *gitSchema) gitRefAsWorkspace(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args workspaceArgs) (dagql.ObjectResult[*core.Workspace], error) {
	return syntheticWorkspaceFromGitRef(ctx, parent, args.Cwd)
}

type withAuthTokenArgs struct {
	Token core.SecretID
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

type treeArgs struct {
	DiscardGitDir bool `default:"false"`
	Depth         int  `default:"1"`
	IncludeTags   bool `default:"false"`

	SSHKnownHosts dagql.Optional[dagql.String]  `name:"sshKnownHosts"`
	SSHAuthSocket dagql.Optional[core.SocketID] `name:"sshAuthSocket"`
}

func (s *gitSchema) tree(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args treeArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
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

	dir, err := parent.Self().Tree(ctx, srv, args.DiscardGitDir, args.Depth, args.IncludeTags)
	if err != nil {
		return inst, err
	}
	inst, err = dagql.NewObjectResultForCurrentCall(ctx, srv, dir)
	if err != nil {
		return inst, err
	}

	if _, ok := parent.Self().Repo.Self().Backend.(*core.RemoteGitRepository); ok {
		dgst, err := calcGitContentDigest(parent.Self(), args)
		if err != nil {
			return inst, err
		}
		inst, err = inst.WithContentDigest(ctx, dgst)
		if err != nil {
			return inst, err
		}
	}

	return inst, nil
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
		repo.URL.Value.String(),
		ref.SHA,
		strconv.FormatBool(repo.DiscardGitDir),
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
	}
	if remoteRepo, ok := repo.Backend.(*core.RemoteGitRepository); ok {
		if remoteRepo.SSHAuthSocket.Self() != nil {
			dgstInputs = append(dgstInputs, "sshAuthSock", string(remoteRepo.SSHAuthSocket.Self().Handle))
		}
		if remoteRepo.AuthToken.Self() != nil {
			dgstInputs = append(dgstInputs, "authToken", strconv.FormatBool(remoteRepo.AuthToken.Self() != nil))
		}
		if remoteRepo.AuthHeader.Self() != nil {
			dgstInputs = append(dgstInputs, "authHeader", strconv.FormatBool(remoteRepo.AuthHeader.Self() != nil))
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

func (s *gitSchema) commitTree(ctx context.Context, parent dagql.ObjectResult[*core.GitCommit], args commitTreeArgs) (inst dagql.ObjectResult[*core.Directory], _ error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	dir, err := parent.Self().Tree(ctx, srv, args.DiscardGitDir, args.Depth, args.IncludeTags)
	if err != nil {
		return inst, err
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
		inst, err = inst.WithContentDigest(ctx, dgst)
		if err != nil {
			return inst, err
		}
	}

	return inst, nil
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

	ref, err := s.gitRefResult(ctx, parent.Self().Repo, &gitutil.Ref{
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
	TagPrefix string `name:"tagPrefix" default:""`
}

func (s *gitSchema) latest(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRepository],
	args latestArgs,
) (inst dagql.Result[*core.GitRef], _ error) {
	repo := parent.Self()
	remoteRepo, isRemote := repo.Backend.(*core.RemoteGitRepository)
	if !isRemote {
		remote, err := repo.LoadRemote(ctx)
		if err != nil {
			return inst, err
		}
		ref, err := core.SelectLatestGitRefWithTagPrefix(
			remote,
			args.TagPrefix,
		)
		if err != nil {
			return inst, err
		}
		return s.gitRefResult(ctx, parent, ref)
	}

	var lockOptions []workspace.LookupOption
	if args.TagPrefix != "" {
		lockOptions = append(lockOptions, workspace.LookupOption{
			Name:  "tagPrefix",
			Value: args.TagPrefix,
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
		if err := core.ValidateGitLatestRef(
			selectedRef,
			args.TagPrefix,
		); err != nil {
			return inst, fmt.Errorf("%s lock value: %w", workspace.LockOperationGitLatest, err)
		}
	} else {
		remote, err := repo.LoadRemote(ctx)
		if err != nil {
			return inst, err
		}
		ref, err := core.SelectLatestGitRefWithTagPrefix(
			remote,
			args.TagPrefix,
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
		LockOperation: workspace.LockOperationGitSHA,
		LockName:      selectedRef,
	})
}
