package core

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/containerd/continuity/fs"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/util/gitutil"
	"github.com/pkg/errors"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/sources/gitdns"
)

type GitRepository struct {
	Backend GitRepositoryBackend

	DiscardGitDir bool
}

type GitRepositoryBackend interface {
	Head(ctx context.Context) (GitRefBackend, error)
	Ref(ctx context.Context, ref string) (GitRefBackend, error)

	Tags(ctx context.Context, patterns []string) ([]string, error)
}

func (*GitRepository) Type() *ast.Type {
	return &ast.Type{
		NamedType: "GitRepository",
		NonNull:   true,
	}
}

func (*GitRepository) TypeDescription() string {
	return "A git repository."
}

func (repo *GitRepository) Head(ctx context.Context) (*GitRef, error) {
	ref, err := repo.Backend.Head(ctx)
	if err != nil {
		return nil, err
	}
	return &GitRef{repo, ref}, nil
}

func (repo *GitRepository) Ref(ctx context.Context, name string) (*GitRef, error) {
	ref, err := repo.Backend.Ref(ctx, name)
	if err != nil {
		return nil, err
	}
	return &GitRef{repo, ref}, nil
}

func (repo *GitRepository) Tags(ctx context.Context, patterns []string) ([]string, error) {
	return repo.Backend.Tags(ctx, patterns)
}

type GitRef struct {
	Repo    *GitRepository
	Backend GitRefBackend
}

type GitRefBackend interface {
	Commit(ctx context.Context) (string, error)
	Tree(ctx context.Context, srv *dagql.Server, discard bool) (*Directory, error)
}

func (*GitRef) Type() *ast.Type {
	return &ast.Type{
		NamedType: "GitRef",
		NonNull:   true,
	}
}

func (*GitRef) TypeDescription() string {
	return "A git ref (tag, branch, or commit)."
}

func (ref *GitRef) Commit(ctx context.Context) (string, error) {
	return ref.Backend.Commit(ctx)
}

func (ref *GitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool) (*Directory, error) {
	return ref.Backend.Tree(ctx, srv, ref.Repo.DiscardGitDir || discardGitDir)
}

type RemoteGitRepository struct {
	Query *Query

	URL string `json:"url"`

	SSHKnownHosts string  `json:"sshKnownHosts"`
	SSHAuthSocket *Socket `json:"sshAuthSocket"`

	Services ServiceBindings `json:"services"`
	Platform Platform        `json:"platform,omitempty"`

	AuthToken  *Secret `json:"authToken"`
	AuthHeader *Secret `json:"authHeader"`
}

var _ GitRepositoryBackend = (*RemoteGitRepository)(nil)

func (repo *RemoteGitRepository) Head(ctx context.Context) (GitRefBackend, error) {
	return &RemoteGitRef{
		Query: repo.Query,
		Repo:  repo,
	}, nil
}

func (repo *RemoteGitRepository) Ref(ctx context.Context, ref string) (GitRefBackend, error) {
	return &RemoteGitRef{
		Query: repo.Query,
		Repo:  repo,
		Ref:   ref,
	}, nil
}

func (repo *RemoteGitRepository) Tags(ctx context.Context, patterns []string) ([]string, error) {
	// standardize to the same ref that goes into the state (see llb.Git)
	remote, err := gitutil.ParseURL(repo.URL)
	if errors.Is(err, gitutil.ErrUnknownProtocol) {
		remote, err = gitutil.ParseURL("https://" + repo.URL)
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
	if len(patterns) > 0 {
		queryArgs = append(queryArgs, "--")
		queryArgs = append(queryArgs, patterns...)
	}
	cmd := exec.CommandContext(ctx, "git", queryArgs...)

	if repo.SSHAuthSocket != nil {
		socketStore, err := repo.Query.Sockets(ctx)
		if err == nil {
			sockpath, cleanup, err := socketStore.MountSocket(ctx, repo.SSHAuthSocket.IDDigest)
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
	if repo.SSHKnownHosts != "" {
		var err error
		knownHostsPath, err = mountKnownHosts(repo.SSHKnownHosts)
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

type RemoteGitRef struct {
	Query *Query

	Repo *RemoteGitRepository
	Ref  string
}

var _ GitRefBackend = (*RemoteGitRef)(nil)

func (ref *RemoteGitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool) (*Directory, error) {
	st, err := ref.getState(ctx, discardGitDir)
	if err != nil {
		return nil, err
	}
	return NewDirectorySt(ctx, ref.Query, st, "", ref.Repo.Platform, ref.Repo.Services)
}

func (ref *RemoteGitRef) Commit(ctx context.Context) (string, error) {
	bk, err := ref.Query.Buildkit(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get buildkit client: %w", err)
	}
	st, err := ref.getState(ctx, true)
	if err != nil {
		return "", err
	}
	p, err := resolveProvenance(ctx, bk, st)
	if err != nil {
		return "", err
	}
	if len(p.Sources.Git) == 0 {
		return "", errors.Errorf("no git commit was resolved")
	}
	return p.Sources.Git[0].Commit, nil
}

func (ref *RemoteGitRef) getState(ctx context.Context, discardGitDir bool) (llb.State, error) {
	opts := []llb.GitOption{}

	if !discardGitDir {
		opts = append(opts, llb.KeepGitDir())
	}
	if ref.Repo.SSHKnownHosts != "" {
		opts = append(opts, llb.KnownSSHHosts(ref.Repo.SSHKnownHosts))
	}
	if ref.Repo.SSHAuthSocket != nil {
		opts = append(opts, llb.MountSSHSock(ref.Repo.SSHAuthSocket.LLBID()))
	}
	if ref.Repo.AuthToken != nil {
		opts = append(opts, llb.AuthTokenSecret(ref.Repo.AuthToken.LLBID()))
	}
	if ref.Repo.AuthHeader != nil {
		opts = append(opts, llb.AuthHeaderSecret(ref.Repo.AuthHeader.LLBID()))
	}

	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return llb.State{}, err
	}

	return gitdns.Git(ref.Repo.URL, ref.Ref, clientMetadata.SessionID, opts...), nil
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

type LocalGitRepository struct {
	Query *Query

	Directory *Directory
}

var _ GitRepositoryBackend = (*LocalGitRepository)(nil)

func (repo *LocalGitRepository) Head(ctx context.Context) (GitRefBackend, error) {
	return &LocalGitRef{
		Query: repo.Query,
		Repo:  repo,
		Ref:   "HEAD",
	}, nil
}

func (repo *LocalGitRepository) Ref(ctx context.Context, ref string) (GitRefBackend, error) {
	return &LocalGitRef{
		Query: repo.Query,
		Repo:  repo,
		Ref:   ref,
	}, nil
}

func (repo *LocalGitRepository) Tags(ctx context.Context, patterns []string) ([]string, error) {
	var tags []string
	err := repo.mount(ctx, func(src string) error {
		out, err := gitCmd(ctx, src, "tag", "-l")
		if err != nil {
			return err
		}
		tags = strings.Split(out, "\n")
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tags, nil
}

func (repo *LocalGitRepository) mount(ctx context.Context, f func(string) error) error {
	return repo.Directory.mount(ctx, func(root string) error {
		src, err := fs.RootPath(root, repo.Directory.Dir)
		if err != nil {
			return err
		}
		return f(src)
	})
}

type LocalGitRef struct {
	Query *Query

	// XXX: error handling if git repo does not exist
	Repo *LocalGitRepository
	Ref  string
}

var _ GitRefBackend = (*LocalGitRef)(nil)

func (ref *LocalGitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool) (*Directory, error) {
	tmpDir, err := os.MkdirTemp("", "local-git-checkout")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	err = ref.Repo.mount(ctx, func(src string) error {
		if _, err := gitCmd(ctx, tmpDir, "init"); err != nil {
			return err
		}
		if _, err := gitCmd(ctx, tmpDir, "fetch", "--depth=1", "file://"+src, ref.Ref); err != nil {
			return err
		}
		if _, err := gitCmd(ctx, tmpDir, "checkout", "FETCH_HEAD"); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if discardGitDir {
		err := os.RemoveAll(filepath.Join(tmpDir, ".git"))
		if err != nil {
			return nil, err
		}
	}

	bk, err := ref.Query.Buildkit(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get buildkit client: %w", err)
	}
	dgst, err := bk.EngineContainerLocalImport(ctx, platforms.DefaultSpec(), tmpDir, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to import container tarball from engine container filesystem: %w", err)
	}
	// XXX: don't unwrap the Instance
	dirInst, err := LoadBlob(ctx, srv, dgst)
	if err != nil {
		return nil, fmt.Errorf("failed to load tarball file blob: %w", err)
	}
	return dirInst.Self, nil
}

func (ref *LocalGitRef) Commit(ctx context.Context) (string, error) {
	var commit string
	err := ref.Repo.mount(ctx, func(src string) error {
		var err error
		commit, err = gitCmd(ctx, src, "rev-parse", ref.Ref)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return commit, nil
}

func gitCmd(ctx context.Context, dir string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("git command failed: %w\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}
