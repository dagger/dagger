package schema

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/sources/gitdns"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/gitutil"
)

var _ SchemaResolvers = &gitSchema{}

type gitSchema struct {
	srv *dagql.Server
}

func (s *gitSchema) Install() {
	dagql.Fields[*core.Query]{
		dagql.Func("git", s.git).
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
		dagql.Func("git", s.gitLegacy).
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
		dagql.Func("tags", s.tags).
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
		dagql.Func("tree", s.tree).
			View(AllVersion).
			Doc(`The filesystem tree at this ref.`).
			ArgDoc("discardGitDir", `Set to true to discard .git directory.`),
		dagql.Func("authenticate", s.authenticate).
			View(AllVersion).
			Doc(`Authenticate the git repository and return the result.`),
		dagql.Func("tree", s.treeLegacy).
			View(BeforeVersion("v0.12.0")).
			Doc(`The filesystem tree at this ref.`).
			ArgDoc("discardGitDir", `Set to true to discard .git directory.`).
			ArgDeprecated("sshKnownHosts", "This option should be passed to `git` instead.").
			ArgDeprecated("sshAuthSocket", "This option should be passed to `git` instead."),
		dagql.Func("commit", s.fetchCommit).
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

func (s *gitSchema) git(ctx context.Context, parent *core.Query, args gitArgs) (*core.GitRepository, error) {
	var svcs core.ServiceBindings
	if args.ExperimentalServiceHost.Valid {
		svc, err := args.ExperimentalServiceHost.Value.Load(ctx, s.srv)
		if err != nil {
			return nil, err
		}
		host, err := svc.Self.Hostname(ctx, svc.ID())
		if err != nil {
			return nil, err
		}
		svcs = append(svcs, core.ServiceBinding{
			ID:       svc.ID(),
			Service:  svc.Self,
			Hostname: host,
		})
	}
	var authSock *core.Socket = nil
	if args.SSHAuthSocket.Valid {
		sock, err := args.SSHAuthSocket.Value.Load(ctx, s.srv)
		if err != nil {
			return nil, err
		}
		authSock = sock.Self
	} else {
		// Fallback to using the client's SSH agent socket if available
		socketStore, err := parent.Sockets(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get socket store: %w", err)
		}

		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get client metadata from context: %w", err)
		}

		if clientMetadata != nil && clientMetadata.SSHAuthSocketPath != "" {
			accessor, err := core.GetClientResourceAccessor(ctx, parent, clientMetadata.SSHAuthSocketPath)
			if err != nil {
				return nil, fmt.Errorf("failed to get client resource name: %w", err)
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
				return nil, fmt.Errorf("failed to select internal socket: %w", err)
			}

			if err := socketStore.AddUnixSocket(sockInst.Self, clientMetadata.ClientID, clientMetadata.SSHAuthSocketPath); err != nil {
				return nil, fmt.Errorf("failed to add unix socket to store: %w", err)
			}
			authSock = sockInst.Self
		}
	}

	discardGitDir := false
	if args.KeepGitDir != nil {
		slog.Warn("The 'keepGitDir' argument is deprecated. Use `tree`'s `discardGitDir' instead.")
		discardGitDir = !*args.KeepGitDir
	}

	// infer protocol, host, path from ref
	remote, err := gitutil.ParseURL(args.URL)
	if errors.Is(err, gitutil.ErrUnknownProtocol) {
		remote, err = gitutil.ParseURL("https://" + args.URL)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to parse Git URL: %w", err)
	}

	var authToken *core.Secret = nil
	if remote.Scheme == "https" || remote.Scheme == "http" {
		// Auth token retrieval
		// todo(guillaume): MIGHT NEED ACCESSOR
		bk, err := parent.Buildkit(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get buildkit: %w", err)
		}

		bklog.G(ctx).Debugf("🎃 remote: |%+v|\n", remote)

		credentials, err := bk.GetCredential(ctx, remote.Scheme, remote.Host, remote.Path)
		// credentials, err := bk.GetCredential(ctx, "https", "github.com", "dagger/dagger.io")
		if err != nil {
			return nil, fmt.Errorf("core/schema: failed to retrieve git credentials from host: %w", err)
		}
		bklog.G(ctx).Debugf("🎃🎃 credentials.Password |%v|%+v|\n", remote, credentials)

		// todo(guillaume): scope accessor ressource ??

		var secretAuthToken dagql.Instance[*core.Secret]
		if err := s.srv.Select(ctx, s.srv.Root(), &secretAuthToken,
			dagql.Selector{
				Field: "setSecret",
				Args: []dagql.NamedInput{
					{
						Name:  "name",
						Value: dagql.NewString("gitAuthtoken"),
					},
					{
						Name:  "plaintext",
						Value: dagql.NewString(credentials.Password),
					},
				},
			},
		); err != nil {
			return nil, fmt.Errorf("failed to create a new secret with the git the auth token: %w", err)
		}
		authToken = secretAuthToken.Self

		bklog.G(ctx).Debugf("🎃🎃 secretAuthToken |%+v|%+v|\n", remote, secretAuthToken)
	}

	return &core.GitRepository{
		Query:         parent,
		URL:           args.URL,
		DiscardGitDir: discardGitDir,
		SSHKnownHosts: args.SSHKnownHosts,
		SSHAuthSocket: authSock,
		Services:      svcs,
		Platform:      parent.Platform(),
		AuthToken:     authToken,
	}, nil
}

type gitArgsLegacy struct {
	URL                     string
	KeepGitDir              bool `default:"false"`
	ExperimentalServiceHost dagql.Optional[core.ServiceID]

	SSHKnownHosts string                        `name:"sshKnownHosts" default:""`
	SSHAuthSocket dagql.Optional[core.SocketID] `name:"sshAuthSocket"`
}

func (s *gitSchema) gitLegacy(ctx context.Context, parent *core.Query, args gitArgsLegacy) (*core.GitRepository, error) {
	return s.git(ctx, parent, gitArgs{
		URL:                     args.URL,
		KeepGitDir:              &args.KeepGitDir,
		ExperimentalServiceHost: args.ExperimentalServiceHost,
		SSHKnownHosts:           args.SSHKnownHosts,
		SSHAuthSocket:           args.SSHAuthSocket,
	})
}

func (s *gitSchema) head(ctx context.Context, parent *core.GitRepository, args struct{}) (*core.GitRef, error) {
	return &core.GitRef{
		Query: parent.Query,
		Repo:  parent,
	}, nil
}

type refArgs struct {
	Name string
}

func (s *gitSchema) ref(ctx context.Context, parent *core.GitRepository, args refArgs) (*core.GitRef, error) {
	return &core.GitRef{
		Query: parent.Query,
		Ref:   args.Name,
		Repo:  parent,
	}, nil
}

type commitArgs struct {
	ID string
}

func (s *gitSchema) commit(ctx context.Context, parent *core.GitRepository, args commitArgs) (*core.GitRef, error) {
	return &core.GitRef{
		Query: parent.Query,
		Ref:   args.ID,
		Repo:  parent,
	}, nil
}

type branchArgs struct {
	Name string
}

func (s *gitSchema) branch(ctx context.Context, parent *core.GitRepository, args branchArgs) (*core.GitRef, error) {
	return &core.GitRef{
		Query: parent.Query,
		Ref:   args.Name,
		Repo:  parent,
	}, nil
}

type tagArgs struct {
	Name string
}

func (s *gitSchema) tag(ctx context.Context, parent *core.GitRepository, args tagArgs) (*core.GitRef, error) {
	return &core.GitRef{
		Query: parent.Query,
		Ref:   args.Name,
		Repo:  parent,
	}, nil
}

type tagsArgs struct {
	Patterns dagql.Optional[dagql.ArrayInput[dagql.String]] `name:"patterns"`
}

func (s *gitSchema) tags(ctx context.Context, parent *core.GitRepository, args tagsArgs) ([]string, error) {
	// standardize to the same ref that goes into the state (see llb.Git)
	remote, err := gitutil.ParseURL(parent.URL)
	if errors.Is(err, gitutil.ErrUnknownProtocol) {
		remote, err = gitutil.ParseURL("https://" + parent.URL)
	}
	if err != nil {
		return nil, err
	}

	queryArgs := []string{
		"ls-remote",
		"--tags", // we only want tags
		"--refs", // we don't want to include ^{} entries for annotated tags
		remote.Remote,
	}

	if args.Patterns.Valid {
		val := args.Patterns.Value.ToArray()

		for _, p := range val {
			queryArgs = append(queryArgs, p.String())
		}
	}
	cmd := exec.CommandContext(ctx, "git", queryArgs...)

	if parent.SSHAuthSocket != nil {
		socketStore, err := parent.Query.Sockets(ctx)
		if err == nil {
			sockpath, cleanup, err := socketStore.MountSocket(ctx, parent.SSHAuthSocket.IDDigest)
			if err != nil {
				return nil, fmt.Errorf("failed to mount SSH socket: %w", err)
			}
			defer func() {
				err := cleanup()
				if err != nil {
					slog.Error("failed to cleanup SSH socket", "error", err)
				}
			}()

			cmd.Env = append(cmd.Env, "SSH_AUTH_SOCK="+sockpath)
		}
	}

	// Handle known hosts
	var knownHostsPath string
	if parent.SSHKnownHosts != "" {
		var err error
		knownHostsPath, err = mountKnownHosts(parent.SSHKnownHosts)
		if err != nil {
			return nil, fmt.Errorf("failed to mount known hosts: %w", err)
		}
		defer os.Remove(knownHostsPath)
	}

	// Set GIT_SSH_COMMAND
	cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND="+gitdns.GetGitSSHCommand(knownHostsPath))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("git command failed: %w\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	tags := []string{}
	scanner := bufio.NewScanner(&stdout)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}

		// this API is to fetch tags, not refs, so we can drop the `refs/tags/`
		// prefix
		tag := strings.TrimPrefix(fields[1], "refs/tags/")

		tags = append(tags, tag)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error scanning git output: %w", err)
	}

	return tags, nil
}

func mountKnownHosts(knownHosts string) (string, error) {
	tempFile, err := os.CreateTemp("", "known_hosts")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary known_hosts file: %w", err)
	}

	_, err = tempFile.WriteString(knownHosts)
	if err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to write known_hosts content: %w", err)
	}

	err = tempFile.Close()
	if err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to close temporary known_hosts file: %w", err)
	}

	return tempFile.Name(), nil
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
	repo.AuthToken = token.Self
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
	repo.AuthHeader = header.Self
	return &repo, nil
}

type treeArgs struct {
	DiscardGitDir bool `default:"false"`
}

func (s *gitSchema) tree(ctx context.Context, parent *core.GitRef, args treeArgs) (*core.Directory, error) {
	return parent.Tree(ctx, args.DiscardGitDir)
}

type authArgs struct {
	Protocol string
	Host     string
	Path     string
}

func (s *gitSchema) authenticate(ctx context.Context, parent *core.GitRef, args authArgs) (dagql.String, error) {
	result, err := parent.Authenticate(ctx, args.Protocol, args.Host, args.Path)
	if err != nil {
		return "", fmt.Errorf("authentication error: %w", err)
	}
	return dagql.NewString(result), nil
}

type treeArgsLegacy struct {
	DiscardGitDir bool `default:"false"`

	SSHKnownHosts dagql.Optional[dagql.String]  `name:"sshKnownHosts"`
	SSHAuthSocket dagql.Optional[core.SocketID] `name:"sshAuthSocket"`
}

func (s *gitSchema) treeLegacy(ctx context.Context, parent *core.GitRef, args treeArgsLegacy) (*core.Directory, error) {
	var authSock *core.Socket
	if args.SSHAuthSocket.Valid {
		sock, err := args.SSHAuthSocket.Value.Load(ctx, s.srv)
		if err != nil {
			return nil, err
		}
		authSock = sock.Self
	}
	res := parent
	if args.SSHKnownHosts.Valid || args.SSHAuthSocket.Valid {
		cp := *res.Repo
		cp.SSHKnownHosts = args.SSHKnownHosts.GetOr("").String()
		cp.SSHAuthSocket = authSock
		res.Repo = &cp
	}
	return res.Tree(ctx, args.DiscardGitDir)
}

func (s *gitSchema) fetchCommit(ctx context.Context, parent *core.GitRef, _ struct{}) (dagql.String, error) {
	str, err := parent.Commit(ctx)
	if err != nil {
		return "", err
	}
	return dagql.NewString(str), nil
}

func isSemver(ver string) bool {
	re := regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	return re.MatchString(ver)
}

// Match a version string in a list of versions with optional subPath
// e.g. github.com/foo/daggerverse/mod@mod/v1.0.0
// e.g. github.com/foo/mod@v1.0.0
// TODO smarter matching logic, e.g. v1 == v1.0.0
func matchVersion(versions []string, match, subPath string) (string, error) {
	// If theres a subPath, first match on {subPath}/{match} for monorepo tags
	if subPath != "/" {
		rawSubPath, _ := strings.CutPrefix(subPath, "/")
		matched, err := matchVersion(versions, fmt.Sprintf("%s/%s", rawSubPath, match), "/")
		// no error means there's a match with subpath/match
		if err == nil {
			return matched, nil
		}
	}

	for _, v := range versions {
		if v == match {
			return v, nil
		}
	}
	return "", fmt.Errorf("unable to find version %s", match)
}
