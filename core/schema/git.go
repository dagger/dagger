package schema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/server/resource"
	"github.com/dagger/dagger/engine/slog"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/moby/buildkit/util/gitutil"
)

var _ SchemaResolvers = &gitSchema{}

type gitSchema struct {
	srv *dagql.Server
}

func (s *gitSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.NodeFuncWithCacheKey("git", s.git, nil).
			View(AllVersion).
			Doc(`Queries a Git repository.`).
			ArgDoc("url",
				`URL of the git repository.`,
				"Can be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`.",
				`Suffix ".git" is optional.`).
			ArgDeprecated("keepGitDir", `Set to true to keep .git directory.`).
			ArgDoc("sshKnownHosts", `Set SSH known hosts`).
			ArgDoc("sshAuthSocket", `Set SSH auth socket`).
			ArgDoc("experimentalServiceHost", `A service which must be started before the repo is fetched.`),
		dagql.NodeFuncWithCacheKey("git", s.gitLegacy, nil).
			View(BeforeVersion("v0.13.4")).
			Doc(`Queries a Git repository.`).
			ArgDoc("url",
				`URL of the git repository.`,
				"Can be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`.",
				`Suffix ".git" is optional.`).
			ArgDeprecated("keepGitDir", `Set to true to keep .git directory.`).
			ArgDoc("sshKnownHosts", `Set SSH known hosts`).
			ArgDoc("sshAuthSocket", `Set SSH auth socket`).
			ArgDoc("experimentalServiceHost", `A service which must be started before the repo is fetched.`),
	}.Install(s.srv)

	dagql.Fields[*core.GitRepository]{
		dagql.Func("head", s.head).
			Doc(`Returns details for HEAD.`),
		dagql.Func("ref", s.ref).
			Doc(`Returns details of a ref.`).
			ArgDoc("name", `Ref's name (can be a commit identifier, a tag name, a branch name, or a fully-qualified ref).`),
		dagql.Func("branch", s.branch).
			Doc(`Returns details of a branch.`).
			ArgDoc("name", `Branch's name (e.g., "main").`),
		dagql.Func("tag", s.tag).
			Doc(`Returns details of a tag.`).
			ArgDoc("name", `Tag's name (e.g., "v0.3.9").`),
		dagql.NodeFunc("tags", s.tags).
			Doc(`tags that match any of the given glob patterns.`).
			ArgDoc("patterns", `Glob patterns (e.g., "refs/tags/v*").`),
		dagql.Func("commit", s.commit).
			Doc(`Returns details of a commit.`).
			// TODO: id is normally a reserved word; we should probably rename this
			ArgDoc("id", `Identifier of the commit (e.g., "b6315d8f2810962c601af73f86831f6866ea798b").`),
		dagql.Func("withAuthToken", s.withAuthToken).
			Doc(`Token to authenticate the remote with.`).
			ArgDoc("token", `Secret used to populate the password during basic HTTP Authorization`),
		dagql.Func("withAuthHeader", s.withAuthHeader).
			Doc(`Header to authenticate the remote with.`).
			ArgDoc("header", `Secret used to populate the Authorization HTTP header`),
	}.Install(s.srv)

	dagql.Fields[*core.GitRef]{
		dagql.NodeFunc("tree", s.tree).
			View(AllVersion).
			Doc(`The filesystem tree at this ref.`).
			ArgDoc("discardGitDir", `Set to true to discard .git directory.`),
		dagql.NodeFunc("tree", s.treeLegacy).
			View(BeforeVersion("v0.12.0")).
			Doc(`The filesystem tree at this ref.`).
			ArgDoc("discardGitDir", `Set to true to discard .git directory.`).
			ArgDeprecated("sshKnownHosts", "This option should be passed to `git` instead.").
			ArgDeprecated("sshAuthSocket", "This option should be passed to `git` instead."),
		dagql.NodeFunc("commit", s.fetchCommit).
			Doc(`The resolved commit id at this ref.`),
	}.Install(s.srv)
}

type gitArgs struct {
	URL                     string
	KeepGitDir              *bool `default:"true"`
	ExperimentalServiceHost dagql.Optional[core.ServiceID]

	SSHKnownHosts string                        `name:"sshKnownHosts" default:""`
	SSHAuthSocket dagql.Optional[core.SocketID] `name:"sshAuthSocket"`
}

//nolint:gocyclo
func (s *gitSchema) git(ctx context.Context, parent dagql.Instance[*core.Query], args gitArgs) (inst dagql.Instance[*core.GitRepository], _ error) {
	// 1. Setup experimental service host
	var svcs core.ServiceBindings
	if args.ExperimentalServiceHost.Valid {
		svc, err := args.ExperimentalServiceHost.Value.Load(ctx, s.srv)
		if err != nil {
			return inst, err
		}
		host, err := svc.Self.Hostname(ctx, svc.ID())
		if err != nil {
			return inst, err
		}
		svcs = append(svcs, core.ServiceBinding{
			ID:       svc.ID(),
			Service:  svc.Self,
			Hostname: host,
		})
	}

	// 2. Get client metadata
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get client metadata from context: %w", err)
	}

	// 3. Setup authentication
	var authSock *core.Socket = nil
	var authToken dagql.Instance[*core.Secret]

	// First parse the ref scheme to determine auth strategy
	remote, err := gitutil.ParseURL(args.URL)
	if errors.Is(err, gitutil.ErrUnknownProtocol) {
		remote, err = gitutil.ParseURL("https://" + args.URL)
	}
	if err != nil {
		return inst, fmt.Errorf("failed to parse Git URL: %w", err)
	}

	// Handle explicit SSH socket if provided
	if args.SSHAuthSocket.Valid {
		sock, err := args.SSHAuthSocket.Value.Load(ctx, s.srv)
		if err != nil {
			return inst, err
		}
		authSock = sock.Self
	} else if remote.Scheme == "ssh" && clientMetadata != nil && clientMetadata.SSHAuthSocketPath != "" {
		// For SSH refs, try to load client's SSH socket if no explicit socket was provided
		socketStore, err := parent.Self.Sockets(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to get socket store: %w", err)
		}

		accessor, err := core.GetClientResourceAccessor(ctx, parent.Self, clientMetadata.SSHAuthSocketPath)
		if err != nil {
			return inst, fmt.Errorf("failed to get client resource name: %w", err)
		}

		var sockInst dagql.Instance[*core.Socket]
		if err := s.srv.Select(ctx, s.srv.Root(), &sockInst,
			dagql.Selector{
				Field: "host",
			},
			dagql.Selector{
				Field: "__internalSocket",
				Args: []dagql.NamedInput{
					{
						Name:  "accessor",
						Value: dagql.NewString(accessor),
					},
				},
			},
		); err != nil {
			return inst, fmt.Errorf("failed to select internal socket: %w", err)
		}

		if err := socketStore.AddUnixSocket(sockInst.Self, clientMetadata.ClientID, clientMetadata.SSHAuthSocketPath); err != nil {
			return inst, fmt.Errorf("failed to add unix socket to store: %w", err)
		}
		authSock = sockInst.Self
	}

	// For HTTP(S) refs, handle PAT auth if we're the main client
	if (remote.Scheme == "https" || remote.Scheme == "http") && clientMetadata != nil {
		parentClientMetadata, err := parent.Self.NonModuleParentClientMetadata(ctx)
		if err != nil {
			return inst, fmt.Errorf("failed to retrieve non-module parent client metadata: %w", err)
		}

		if clientMetadata.ClientID == parentClientMetadata.ClientID {
			// Check if repo is public
			repo := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{
				Name: "origin",
				URLs: []string{remote.Remote},
			})

			_, err := repo.ListContext(ctx, &git.ListOptions{Auth: nil})
			switch {
			case err == nil:
				// no auth needed

			case errors.Is(err, transport.ErrAuthenticationRequired):
				// Only proceed with auth if repo requires authentication
				authCtx := engine.ContextWithClientMetadata(ctx, parentClientMetadata)

				bk, err := parent.Self.Buildkit(authCtx)
				if err != nil {
					return inst, fmt.Errorf("failed to get buildkit: %w", err)
				}

				// Retrieve credential from host
				credentials, err := bk.GetCredential(authCtx, remote.Scheme, remote.Host, remote.Path)
				switch {
				case err != nil:
					slog.Warn("failed to retrieve git credentials, continuing without authentication", "error", err, "url", args.URL)

				case credentials == nil || credentials.Password == "":
					slog.Warn("no credentials found, continuing without authentication", "url", args.URL)

				default:
					// Credentials found, create and set auth token
					hash := sha256.Sum256([]byte(credentials.Password))
					secretName := hex.EncodeToString(hash[:])
					var secretAuthToken dagql.Instance[*core.Secret]
					if err := s.srv.Select(authCtx, s.srv.Root(), &secretAuthToken,
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
					authToken = secretAuthToken
				}

			default:
				slog.Warn("failed to list remote refs, continuing without authentication", "error", err, "url", args.URL)
			}
		}
	}

	// 4. Handle git directory configuration
	discardGitDir := false
	if args.KeepGitDir != nil {
		slog.Warn("The 'keepGitDir' argument is deprecated. Use `tree`'s `discardGitDir' instead.")
		discardGitDir = !*args.KeepGitDir
	}

	inst, err = dagql.NewInstanceForCurrentID(ctx, s.srv, parent, &core.GitRepository{
		Backend: &core.RemoteGitRepository{
			Query:         parent.Self,
			URL:           args.URL,
			SSHKnownHosts: args.SSHKnownHosts,
			SSHAuthSocket: authSock,
			Services:      svcs,
			Platform:      parent.Self.Platform(),
		},
		DiscardGitDir: discardGitDir,
	})
	if err != nil {
		return inst, fmt.Errorf("failed to create GitRepository instance: %w", err)
	}

	// set the auth token by selecting withAuthToken so that it shows up in the dagql call
	// as a secret and can thus be passed to functions
	if authToken.Self != nil {
		var instWithToken dagql.Instance[*core.GitRepository]
		err := s.srv.Select(ctx, inst, &instWithToken,
			dagql.Selector{
				Field: "withAuthToken",
				Args: []dagql.NamedInput{
					{
						Name:  "token",
						Value: dagql.NewID[*core.Secret](authToken.ID()),
					},
				},
			},
		)
		if err != nil {
			return inst, fmt.Errorf("failed to set auth token: %w", err)
		}
		inst = instWithToken
	}

	if authToken.Self != nil {
		secretTransferPostCall, err := core.SecretTransferPostCall(ctx, parent.Self, clientMetadata.ClientID, &resource.ID{
			ID: *authToken.ID(),
		})
		if err != nil {
			return inst, fmt.Errorf("failed to create secret transfer post call: %w", err)
		}

		inst = inst.WithPostCall(secretTransferPostCall)
	}
	return inst, nil
}

type gitArgsLegacy struct {
	URL                     string
	KeepGitDir              bool `default:"false"`
	ExperimentalServiceHost dagql.Optional[core.ServiceID]

	SSHKnownHosts string                        `name:"sshKnownHosts" default:""`
	SSHAuthSocket dagql.Optional[core.SocketID] `name:"sshAuthSocket"`
}

func (s *gitSchema) gitLegacy(ctx context.Context, parent dagql.Instance[*core.Query], args gitArgsLegacy) (dagql.Instance[*core.GitRepository], error) {
	return s.git(ctx, parent, gitArgs{
		URL:                     args.URL,
		KeepGitDir:              &args.KeepGitDir,
		ExperimentalServiceHost: args.ExperimentalServiceHost,
		SSHKnownHosts:           args.SSHKnownHosts,
		SSHAuthSocket:           args.SSHAuthSocket,
	})
}

func (s *gitSchema) head(ctx context.Context, parent *core.GitRepository, args struct{}) (*core.GitRef, error) {
	return parent.Head(ctx)
}

type refArgs struct {
	Name string
}

func (s *gitSchema) ref(ctx context.Context, parent *core.GitRepository, args refArgs) (*core.GitRef, error) {
	return parent.Ref(ctx, args.Name)
}

type commitArgs struct {
	ID string
}

func (s *gitSchema) commit(ctx context.Context, parent *core.GitRepository, args commitArgs) (*core.GitRef, error) {
	return parent.Ref(ctx, args.ID)
}

type branchArgs struct {
	Name string
}

func (s *gitSchema) branch(ctx context.Context, parent *core.GitRepository, args branchArgs) (*core.GitRef, error) {
	return parent.Ref(ctx, args.Name)
}

type tagArgs struct {
	Name string
}

func (s *gitSchema) tag(ctx context.Context, parent *core.GitRepository, args tagArgs) (*core.GitRef, error) {
	return parent.Ref(ctx, args.Name)
}

type tagsArgs struct {
	Patterns dagql.Optional[dagql.ArrayInput[dagql.String]] `name:"patterns"`
}

func (s *gitSchema) tags(ctx context.Context, parent dagql.Instance[*core.GitRepository], args tagsArgs) (dagql.Array[dagql.String], error) {
	if parent.Self.UseDagOp() && !core.DagOpInContext[core.RawDagOp](ctx) {
		return DagOp(ctx, s.srv, parent, args, s.tags)
	}

	var patterns []string
	if args.Patterns.Valid {
		for _, pattern := range args.Patterns.Value {
			patterns = append(patterns, pattern.String())
		}
	}
	res, err := parent.Self.Tags(ctx, patterns)
	return dagql.NewStringArray(res...), err
}

type withAuthTokenArgs struct {
	Token core.SecretID
}

func (s *gitSchema) withAuthToken(ctx context.Context, parent *core.GitRepository, args withAuthTokenArgs) (*core.GitRepository, error) {
	token, err := args.Token.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	repo := *parent
	if remote, ok := repo.Backend.(*core.RemoteGitRepository); ok {
		remote := *remote
		remote.AuthToken = token.Self
		repo.Backend = &remote
	}
	return &repo, nil
}

type withAuthHeaderArgs struct {
	Header core.SecretID
}

func (s *gitSchema) withAuthHeader(ctx context.Context, parent *core.GitRepository, args withAuthHeaderArgs) (*core.GitRepository, error) {
	header, err := args.Header.Load(ctx, s.srv)
	if err != nil {
		return nil, err
	}
	repo := *parent
	if remote, ok := repo.Backend.(*core.RemoteGitRepository); ok {
		remote := *remote
		remote.AuthHeader = header.Self
		repo.Backend = &remote
	}
	return &repo, nil
}

type treeArgs struct {
	DiscardGitDir bool `default:"false"`
}

func (s *gitSchema) tree(ctx context.Context, parent dagql.Instance[*core.GitRef], args treeArgs) (inst dagql.Instance[*core.Directory], _ error) {
	if parent.Self.UseDagOp() && !core.DagOpInContext[core.FSDagOp](ctx) {
		return DagOpDirectory(ctx, s.srv, parent, args, s.tree, nil)
	}

	dir, err := parent.Self.Tree(ctx, s.srv, args.DiscardGitDir)
	if err != nil {
		return inst, err
	}
	inst, err = dagql.NewInstanceForCurrentID(ctx, s.srv, parent, dir)
	if err != nil {
		return inst, err
	}
	return inst, nil
}

type treeArgsLegacy struct {
	DiscardGitDir bool `default:"false"`

	SSHKnownHosts dagql.Optional[dagql.String]  `name:"sshKnownHosts"`
	SSHAuthSocket dagql.Optional[core.SocketID] `name:"sshAuthSocket"`
}

func (s *gitSchema) treeLegacy(ctx context.Context, parent dagql.Instance[*core.GitRef], args treeArgsLegacy) (inst dagql.Instance[*core.Directory], _ error) {
	if parent.Self.UseDagOp() && !core.DagOpInContext[core.FSDagOp](ctx) {
		return DagOpDirectory(ctx, s.srv, parent, args, s.treeLegacy, nil)
	}

	var authSock *core.Socket
	if args.SSHAuthSocket.Valid {
		sock, err := args.SSHAuthSocket.Value.Load(ctx, s.srv)
		if err != nil {
			return inst, err
		}
		authSock = sock.Self
	}
	res := parent
	if args.SSHKnownHosts.Valid || args.SSHAuthSocket.Valid {
		repo := *res.Self.Repo
		if remote, ok := repo.Backend.(*core.RemoteGitRepository); ok {
			remote := *remote
			remote.SSHKnownHosts = args.SSHKnownHosts.GetOr("").String()
			remote.SSHAuthSocket = authSock
			repo.Backend = &remote
		}
		res.Self.Repo = &repo
	}
	dir, err := res.Self.Tree(ctx, s.srv, args.DiscardGitDir)
	if err != nil {
		return inst, err
	}
	inst, err = dagql.NewInstanceForCurrentID(ctx, s.srv, parent, dir)
	if err != nil {
		return inst, err
	}
	return inst, nil
}

func (s *gitSchema) fetchCommit(ctx context.Context, parent dagql.Instance[*core.GitRef], args struct{}) (dagql.String, error) {
	if parent.Self.UseDagOp() && !core.DagOpInContext[core.RawDagOp](ctx) {
		return DagOp(ctx, s.srv, parent, args, s.fetchCommit)
	}

	str, err := parent.Self.Commit(ctx)
	if err != nil {
		return "", err
	}
	return dagql.NewString(str), nil
}
