package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/sync/errgroup"

	"github.com/dagger/dagger/dagql"
)

type GitRepository struct {
	URL     dagql.Nullable[dagql.String] `field:"true" doc:"The URL of the git repository."`
	Backend GitRepositoryBackend
	Remote  *gitutil.Remote

	DiscardGitDir bool
}

type GitRepositoryBackend interface {
	// Remote returns information about the git remote.
	Remote(ctx context.Context) (*gitutil.Remote, error)
	// Get returns a reference to a specific git ref (branch, tag, or commit).
	Get(ctx context.Context, ref *gitutil.Ref) (GitRefBackend, error)

	// Dirty returns a Directory representing the repository in it's current state.
	Dirty(ctx context.Context) (dagql.ObjectResult[*Directory], error)
	// Cleaned returns a Directory representing the repository with all uncommitted changes discarded.
	Cleaned(ctx context.Context) (dagql.ObjectResult[*Directory], error)

	// mount mounts the repository with the provided refs and executes the given function.
	mount(ctx context.Context, depth int, includeTags bool, refs []GitRefBackend, fn func(*gitutil.GitCLI) error) error
}

type GitRef struct {
	Repo    dagql.ObjectResult[*GitRepository]
	Backend GitRefBackend
	Ref     *gitutil.Ref
}

type GitCommit struct {
	Repo    dagql.ObjectResult[*GitRepository]
	Backend GitRefBackend
	Ref     *gitutil.Ref
}

type GitCommitMetadata struct {
	SHA            string
	ShortSHA       string
	AuthoredDate   string
	CommittedDate  string
	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string
	Message        string
	ParentSHAs     []string
}

type GitRefBackend interface {
	Tree(ctx context.Context, srv *dagql.Server, discard bool, depth int, includeTags bool) (checkout *Directory, err error)

	mount(ctx context.Context, depth int, includeTags bool, fn func(*gitutil.GitCLI) error) error
}

var _ dagql.PersistedObject = (*GitRepository)(nil)
var _ dagql.PersistedObjectDecoder = (*GitRepository)(nil)
var _ dagql.OnReleaser = (*GitRepository)(nil)
var _ dagql.HasDependencyResults = (*GitRepository)(nil)
var _ dagql.PersistedObject = (*GitRef)(nil)
var _ dagql.PersistedObjectDecoder = (*GitRef)(nil)
var _ dagql.HasDependencyResults = (*GitRef)(nil)
var _ dagql.PersistedObject = (*GitCommit)(nil)
var _ dagql.PersistedObjectDecoder = (*GitCommit)(nil)
var _ dagql.HasDependencyResults = (*GitCommit)(nil)

func NewGitRepository(ctx context.Context, backend GitRepositoryBackend) (*GitRepository, error) {
	repo := &GitRepository{
		Backend: backend,
	}

	remote, err := backend.Remote(ctx)
	if err != nil {
		return nil, err
	}
	repo.Remote = remote

	if remoteBackend, ok := backend.(*RemoteGitRepository); ok {
		repo.URL = dagql.NonNull(dagql.String(remoteBackend.URL.String()))
	}

	return repo, nil
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

func (*GitRef) Type() *ast.Type {
	return &ast.Type{
		NamedType: "GitRef",
		NonNull:   true,
	}
}

func (*GitRef) TypeDescription() string {
	return "A git ref (tag, branch, or commit)."
}

func (*GitCommit) Type() *ast.Type {
	return &ast.Type{
		NamedType: "GitCommit",
		NonNull:   true,
	}
}

func (*GitCommit) TypeDescription() string {
	return "An immutable git commit."
}

func (repo *GitRepository) OnRelease(ctx context.Context) error {
	_ = ctx
	return nil
}

func (repo *GitRepository) PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink {
	return nil
}

func (repo *GitRepository) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if repo == nil {
		return nil, nil
	}

	var owned []dagql.AnyResult
	switch backend := repo.Backend.(type) {
	case *LocalGitRepository:
		if backend.Directory.Self() != nil {
			attached, err := attach(backend.Directory)
			if err != nil {
				return nil, fmt.Errorf("attach git repository directory: %w", err)
			}
			typed, ok := attached.(dagql.ObjectResult[*Directory])
			if !ok {
				return nil, fmt.Errorf("attach git repository directory: unexpected result %T", attached)
			}
			backend.Directory = typed
			owned = append(owned, typed)
		}
	case *RemoteGitRepository:
		if backend.Mirror.Self() != nil {
			attached, err := attach(backend.Mirror)
			if err != nil {
				return nil, fmt.Errorf("attach git repository remote mirror: %w", err)
			}
			typed, ok := attached.(dagql.ObjectResult[*RemoteGitMirror])
			if !ok {
				return nil, fmt.Errorf("attach git repository remote mirror: unexpected result %T", attached)
			}
			backend.Mirror = typed
			owned = append(owned, typed)
		}
		if backend.SSHAuthSocket.Self() != nil {
			attached, err := attach(backend.SSHAuthSocket)
			if err != nil {
				return nil, fmt.Errorf("attach git repository ssh auth socket: %w", err)
			}
			typed, ok := attached.(dagql.ObjectResult[*Socket])
			if !ok {
				return nil, fmt.Errorf("attach git repository ssh auth socket: unexpected result %T", attached)
			}
			backend.SSHAuthSocket = typed
			owned = append(owned, typed)
		}
		if backend.AuthToken.Self() != nil {
			attached, err := attach(backend.AuthToken)
			if err != nil {
				return nil, fmt.Errorf("attach git repository auth token: %w", err)
			}
			typed, ok := attached.(dagql.ObjectResult[*Secret])
			if !ok {
				return nil, fmt.Errorf("attach git repository auth token: unexpected result %T", attached)
			}
			backend.AuthToken = typed
			owned = append(owned, typed)
		}
		if backend.AuthHeader.Self() != nil {
			attached, err := attach(backend.AuthHeader)
			if err != nil {
				return nil, fmt.Errorf("attach git repository auth header: %w", err)
			}
			typed, ok := attached.(dagql.ObjectResult[*Secret])
			if !ok {
				return nil, fmt.Errorf("attach git repository auth header: unexpected result %T", attached)
			}
			backend.AuthHeader = typed
			owned = append(owned, typed)
		}
		for i := range backend.Services {
			if backend.Services[i].Service.Self() == nil {
				continue
			}
			attached, err := attach(backend.Services[i].Service)
			if err != nil {
				return nil, fmt.Errorf("attach git repository service binding %q: %w", backend.Services[i].Hostname, err)
			}
			typed, ok := attached.(dagql.ObjectResult[*Service])
			if !ok {
				return nil, fmt.Errorf("attach git repository service binding %q: unexpected result %T", backend.Services[i].Hostname, attached)
			}
			backend.Services[i].Service = typed
			owned = append(owned, typed)
		}
	}

	return owned, nil
}

func (ref *GitRef) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if ref == nil || ref.Repo.Self() == nil {
		return nil, nil
	}
	attached, err := attach(ref.Repo)
	if err != nil {
		return nil, fmt.Errorf("attach git ref repo: %w", err)
	}
	typed, ok := attached.(dagql.ObjectResult[*GitRepository])
	if !ok {
		return nil, fmt.Errorf("attach git ref repo: unexpected result %T", attached)
	}
	ref.Repo = typed
	return []dagql.AnyResult{typed}, nil
}

func (commit *GitCommit) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if commit == nil || commit.Repo.Self() == nil {
		return nil, nil
	}
	attached, err := attach(commit.Repo)
	if err != nil {
		return nil, fmt.Errorf("attach git commit repo: %w", err)
	}
	typed, ok := attached.(dagql.ObjectResult[*GitRepository])
	if !ok {
		return nil, fmt.Errorf("attach git commit repo: unexpected result %T", attached)
	}
	commit.Repo = typed
	return []dagql.AnyResult{typed}, nil
}

const (
	persistedGitRepositoryFormLocal  = "local"
	persistedGitRepositoryFormRemote = "remote"
)

type persistedGitRepositoryPayload struct {
	Form          string          `json:"form"`
	DiscardGitDir bool            `json:"discardGitDir,omitempty"`
	RemoteJSON    json.RawMessage `json:"remoteJson,omitempty"`

	Local  *persistedLocalGitRepositoryPayload  `json:"local,omitempty"`
	Remote *persistedRemoteGitRepositoryPayload `json:"remote,omitempty"`
}

type persistedLocalGitRepositoryPayload struct {
	DirectoryResultID uint64 `json:"directoryResultID"`
}

type persistedRemoteGitRepositoryPayload struct {
	URL           string   `json:"url"`
	SSHKnownHosts string   `json:"sshKnownHosts,omitempty"`
	AuthUsername  string   `json:"authUsername,omitempty"`
	Platform      Platform `json:"platform"`
}

func (repo *GitRepository) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	if repo == nil {
		return nil, fmt.Errorf("encode persisted git repository: nil repository")
	}
	remoteJSON, err := json.Marshal(repo.Remote)
	if err != nil {
		return nil, fmt.Errorf("marshal persisted git repository remote: %w", err)
	}
	payload := persistedGitRepositoryPayload{
		DiscardGitDir: repo.DiscardGitDir,
		RemoteJSON:    remoteJSON,
	}
	switch backend := repo.Backend.(type) {
	case *LocalGitRepository:
		dirID, err := encodePersistedObjectRef(cache, backend.Directory, "git repository directory")
		if err != nil {
			return nil, err
		}
		payload.Form = persistedGitRepositoryFormLocal
		payload.Local = &persistedLocalGitRepositoryPayload{
			DirectoryResultID: dirID,
		}
	case *RemoteGitRepository:
		if backend.URL == nil {
			return nil, fmt.Errorf("encode persisted git repository: remote backend missing URL")
		}
		payload.Form = persistedGitRepositoryFormRemote
		payload.Remote = &persistedRemoteGitRepositoryPayload{
			URL:           backend.URL.String(),
			SSHKnownHosts: backend.SSHKnownHosts,
			AuthUsername:  backend.AuthUsername,
			Platform:      backend.Platform,
		}
	default:
		return nil, fmt.Errorf("encode persisted git repository: unsupported backend %T", repo.Backend)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal persisted git repository payload: %w", err)
	}
	return payloadJSON, nil
}

func (*GitRepository) DecodePersistedObject(ctx context.Context, dag *dagql.Server, resultID uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedGitRepositoryPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted git repository payload: %w", err)
	}
	var remote gitutil.Remote
	if len(persisted.RemoteJSON) > 0 && string(persisted.RemoteJSON) != "null" {
		if err := json.Unmarshal(persisted.RemoteJSON, &remote); err != nil {
			return nil, fmt.Errorf("decode persisted git repository remote: %w", err)
		}
	}

	repo := &GitRepository{
		Remote:        &remote,
		DiscardGitDir: persisted.DiscardGitDir,
	}
	switch persisted.Form {
	case persistedGitRepositoryFormLocal:
		if persisted.Local == nil {
			return nil, fmt.Errorf("decode persisted git repository: missing local payload")
		}
		dir, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.Local.DirectoryResultID, "git repository directory")
		if err != nil {
			return nil, err
		}
		repo.Backend = &LocalGitRepository{Directory: dir}
	case persistedGitRepositoryFormRemote:
		if persisted.Remote == nil {
			return nil, fmt.Errorf("decode persisted git repository: missing remote payload")
		}
		parsedURL, err := gitutil.ParseURL(persisted.Remote.URL)
		if err != nil {
			return nil, fmt.Errorf("decode persisted git repository URL: %w", err)
		}
		backend := &RemoteGitRepository{
			URL:           parsedURL,
			SSHKnownHosts: persisted.Remote.SSHKnownHosts,
			AuthUsername:  persisted.Remote.AuthUsername,
			Platform:      persisted.Remote.Platform,
		}
		var mirror dagql.ObjectResult[*RemoteGitMirror]
		if err := dag.Select(ctx, dag.Root(), &mirror, dagql.Selector{
			Field: "_remoteGitMirror",
			Args: []dagql.NamedInput{
				{Name: "remoteURL", Value: dagql.String(parsedURL.Remote())},
			},
		}); err != nil {
			return nil, fmt.Errorf("decode persisted git repository remote mirror: %w", err)
		}
		backend.Mirror = mirror
		repo.Backend = backend
		repo.URL = dagql.NonNull(dagql.String(parsedURL.String()))
	default:
		return nil, fmt.Errorf("decode persisted git repository: unsupported form %q", persisted.Form)
	}
	return repo, nil
}

type persistedGitRefPayload struct {
	RepoResultID uint64 `json:"repoResultID"`
	Name         string `json:"name,omitempty"`
	SHA          string `json:"sha"`
}

func (ref *GitRef) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	if ref == nil {
		return nil, fmt.Errorf("encode persisted git ref: nil ref")
	}
	if ref.Ref == nil {
		return nil, fmt.Errorf("encode persisted git ref: missing ref")
	}
	repoID, err := encodePersistedObjectRef(cache, ref.Repo, "git ref repo")
	if err != nil {
		return nil, err
	}
	payloadJSON, err := json.Marshal(persistedGitRefPayload{
		RepoResultID: repoID,
		Name:         ref.Ref.Name,
		SHA:          ref.Ref.SHA,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal persisted git ref payload: %w", err)
	}
	return payloadJSON, nil
}

func (*GitRef) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedGitRefPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted git ref payload: %w", err)
	}
	repo, err := loadPersistedObjectResultByResultID[*GitRepository](ctx, dag, persisted.RepoResultID, "git ref repo")
	if err != nil {
		return nil, err
	}
	ref := &gitutil.Ref{
		Name: persisted.Name,
		SHA:  persisted.SHA,
	}
	backend, err := repo.Self().Backend.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	return &GitRef{
		Repo:    repo,
		Backend: backend,
		Ref:     ref,
	}, nil
}

type persistedGitCommitPayload persistedGitRefPayload

func (commit *GitCommit) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	if commit == nil {
		return nil, fmt.Errorf("encode persisted git commit: nil commit")
	}
	if commit.Ref == nil {
		return nil, fmt.Errorf("encode persisted git commit: missing ref")
	}
	repoID, err := encodePersistedObjectRef(cache, commit.Repo, "git commit repo")
	if err != nil {
		return nil, err
	}
	payloadJSON, err := json.Marshal(persistedGitCommitPayload{
		RepoResultID: repoID,
		Name:         commit.Ref.Name,
		SHA:          commit.Ref.SHA,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal persisted git commit payload: %w", err)
	}
	return payloadJSON, nil
}

func (*GitCommit) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedGitCommitPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted git commit payload: %w", err)
	}
	repo, err := loadPersistedObjectResultByResultID[*GitRepository](ctx, dag, persisted.RepoResultID, "git commit repo")
	if err != nil {
		return nil, err
	}
	ref := &gitutil.Ref{
		Name: persisted.Name,
		SHA:  persisted.SHA,
	}
	backend, err := repo.Self().Backend.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	return &GitCommit{
		Repo:    repo,
		Backend: backend,
		Ref:     ref,
	}, nil
}

func (ref *GitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int, includeTags bool) (*Directory, error) {
	return ref.Backend.Tree(ctx, srv, ref.Repo.Self().DiscardGitDir || discardGitDir, depth, includeTags)
}

func (commit *GitCommit) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int, includeTags bool) (*Directory, error) {
	return commit.Backend.Tree(ctx, srv, commit.Repo.Self().DiscardGitDir || discardGitDir, depth, includeTags)
}

func (commit *GitCommit) Metadata(ctx context.Context) (*GitCommitMetadata, error) {
	if commit == nil || commit.Ref == nil {
		return nil, fmt.Errorf("git commit metadata: missing commit")
	}
	if commit.Ref.SHA == "" {
		return nil, fmt.Errorf("git commit metadata: missing commit SHA")
	}

	var out []byte
	err := commit.Backend.mount(ctx, 1, false, func(git *gitutil.GitCLI) error {
		var err error
		out, err = git.Run(ctx, "cat-file", "commit", commit.Ref.SHA)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("read git commit metadata for %s: %w", commit.Ref.SHA, err)
	}

	meta, err := parseGitCommitMetadata(commit.Ref.SHA, string(out))
	if err != nil {
		return nil, fmt.Errorf("read git commit metadata for %s: %w", commit.Ref.SHA, err)
	}
	return meta, nil
}

func (commit *GitCommit) MessageHeadline(ctx context.Context) (string, error) {
	meta, err := commit.Metadata(ctx)
	if err != nil {
		return "", err
	}
	headline, _, _ := strings.Cut(meta.Message, "\n")
	return headline, nil
}

func (commit *GitCommit) MessageBody(ctx context.Context) (string, error) {
	meta, err := commit.Metadata(ctx)
	if err != nil {
		return "", err
	}
	_, body, ok := strings.Cut(meta.Message, "\n")
	if !ok {
		return "", nil
	}
	return strings.TrimPrefix(body, "\n"), nil
}

func parseGitCommitMetadata(sha string, raw string) (*GitCommitMetadata, error) {
	headers, message, _ := strings.Cut(raw, "\n\n")
	meta := &GitCommitMetadata{
		SHA:      sha,
		ShortSHA: sha,
		Message:  strings.TrimSuffix(message, "\n"),
	}
	if len(sha) > 7 {
		meta.ShortSHA = sha[:7]
	}

	for _, line := range strings.Split(headers, "\n") {
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		switch key {
		case "parent":
			meta.ParentSHAs = append(meta.ParentSHAs, value)
		case "author":
			sig, err := parseGitCommitSignature(value)
			if err != nil {
				return nil, fmt.Errorf("parse author: %w", err)
			}
			meta.AuthorName = sig.Name
			meta.AuthorEmail = sig.Email
			meta.AuthoredDate = sig.Date
		case "committer":
			sig, err := parseGitCommitSignature(value)
			if err != nil {
				return nil, fmt.Errorf("parse committer: %w", err)
			}
			meta.CommitterName = sig.Name
			meta.CommitterEmail = sig.Email
			meta.CommittedDate = sig.Date
		}
	}

	if meta.AuthorName == "" || meta.AuthorEmail == "" || meta.AuthoredDate == "" {
		return nil, fmt.Errorf("missing author metadata")
	}
	if meta.CommitterName == "" || meta.CommitterEmail == "" || meta.CommittedDate == "" {
		return nil, fmt.Errorf("missing committer metadata")
	}
	return meta, nil
}

type gitCommitSignature struct {
	Name  string
	Email string
	Date  string
}

func parseGitCommitSignature(raw string) (gitCommitSignature, error) {
	nameEnd := strings.LastIndex(raw, " <")
	emailEnd := strings.LastIndex(raw, "> ")
	if nameEnd < 0 || emailEnd < nameEnd {
		return gitCommitSignature{}, fmt.Errorf("invalid signature %q", raw)
	}
	name := raw[:nameEnd]
	email := raw[nameEnd+2 : emailEnd]
	dateParts := strings.Fields(raw[emailEnd+2:])
	if len(dateParts) != 2 {
		return gitCommitSignature{}, fmt.Errorf("invalid signature date %q", raw)
	}
	seconds, err := strconv.ParseInt(dateParts[0], 10, 64)
	if err != nil {
		return gitCommitSignature{}, fmt.Errorf("parse timestamp: %w", err)
	}
	offset, err := parseGitTimezoneOffset(dateParts[1])
	if err != nil {
		return gitCommitSignature{}, err
	}
	loc := time.FixedZone(dateParts[1], offset)
	return gitCommitSignature{
		Name:  name,
		Email: email,
		Date:  time.Unix(seconds, 0).In(loc).Format(time.RFC3339),
	}, nil
}

func parseGitTimezoneOffset(raw string) (int, error) {
	if len(raw) != 5 || (raw[0] != '+' && raw[0] != '-') {
		return 0, fmt.Errorf("invalid timezone offset %q", raw)
	}
	hours, err := strconv.Atoi(raw[1:3])
	if err != nil {
		return 0, fmt.Errorf("parse timezone hours: %w", err)
	}
	minutes, err := strconv.Atoi(raw[3:5])
	if err != nil {
		return 0, fmt.Errorf("parse timezone minutes: %w", err)
	}
	offset := (hours*60 + minutes) * 60
	if raw[0] == '-' {
		offset = -offset
	}
	return offset, nil
}

// doGitCheckout performs a git checkout using the given git helper.
//
// The provided git dir should *always* be empty.
func doGitCheckout(
	ctx context.Context,
	checkoutGit *gitutil.GitCLI,
	remoteURL string,
	cloneURL string,
	ref *gitutil.Ref,
	depth int,
	discardGitDir bool,
) error {
	checkoutDirGit, err := checkoutGit.GitDir(ctx)
	if err != nil {
		return fmt.Errorf("could not find git dir: %w", err)
	}

	_, err = checkoutGit.Run(ctx, "-c", "init.defaultBranch=main", "init")
	if err != nil {
		return err
	}

	tmpref := "refs/dagger.tmp/" + identity.NewID()

	// TODO: maybe this should use --no-tags by default, but that's a breaking change :(
	// also, we currently don't do any special work to ensure that the fetched
	// tags are consistent with the GitRepository.Remote (oops)
	args := []string{"fetch", "-u"}
	if depth > 0 {
		args = append(args, fmt.Sprintf("--depth=%d", depth))
	}
	args = append(args, cloneURL)
	args = append(args, ref.SHA+":"+tmpref)
	_, err = checkoutGit.Run(ctx, args...)
	if err != nil {
		return err
	}
	if ref.Name == "" {
		_, err = checkoutGit.Run(ctx, "checkout", ref.SHA)
		if err != nil {
			return fmt.Errorf("failed to checkout remote %s: %w", cloneURL, err)
		}
	} else {
		_, err = checkoutGit.Run(ctx, "update-ref", ref.Name, ref.SHA)
		if err != nil {
			return fmt.Errorf("failed to checkout remote %s: %w", cloneURL, err)
		}
		_, err = checkoutGit.Run(ctx, "checkout", strings.TrimPrefix(ref.Name, "refs/heads/"))
		if err != nil {
			return fmt.Errorf("failed to checkout remote %s: %w", cloneURL, err)
		}
		_, err = checkoutGit.Run(ctx, "reset", "--hard", ref.SHA)
		if err != nil {
			return fmt.Errorf("failed to reset ref: %w", err)
		}
	}
	if remoteURL != "" {
		_, err = checkoutGit.Run(ctx, "remote", "add", "origin", remoteURL)
		if err != nil {
			return fmt.Errorf("failed to set remote origin to %s: %w", remoteURL, err)
		}
	}
	_, err = checkoutGit.Run(ctx, "update-ref", "-d", tmpref)
	if err != nil {
		return fmt.Errorf("failed to delete tmp ref: %w", err)
	}
	_, err = checkoutGit.Run(ctx, "reflog", "expire", "--all", "--expire=now")
	if err != nil {
		return fmt.Errorf("failed to expire reflog: %w", err)
	}

	if err := os.Remove(filepath.Join(checkoutDirGit, "FETCH_HEAD")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove FETCH_HEAD: %w", err)
	}

	// TODO: this feels completely out-of-sync from how we do the rest
	// of the clone - caching will not be as great here
	subArgs := []string{"submodule", "update", "--init", "--recursive", "--depth=1"}
	if _, err := checkoutGit.Run(ctx, subArgs...); err != nil {
		if errors.Is(err, gitutil.ErrShallowNotSupported) {
			subArgs = slices.DeleteFunc(subArgs, func(s string) bool {
				return strings.HasPrefix(s, "--depth")
			})
			_, err = checkoutGit.Run(ctx, subArgs...)
		}
		if err != nil {
			return fmt.Errorf("failed to update submodules: %w", err)
		}
	}

	if discardGitDir {
		if err := os.RemoveAll(checkoutDirGit); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to remove .git: %w", err)
		}
	}

	return nil
}

func MergeBase(ctx context.Context, ref1 *GitRef, ref2 *GitRef) (*GitRef, error) {
	ref1RepoDgst, err1 := ref1.Repo.RecipeDigest(ctx)
	if err1 != nil {
		return nil, fmt.Errorf("merge-base ref1 repo ID: %w", err1)
	}
	ref2RepoDgst, err2 := ref2.Repo.RecipeDigest(ctx)
	if err2 != nil {
		return nil, fmt.Errorf("merge-base ref2 repo ID: %w", err2)
	}
	if ref1RepoDgst == ref2RepoDgst { // fast-path, just grab both refs from the same repo
		var mergeBase string
		err := ref1.Repo.Self().Backend.mount(ctx, 0, false, []GitRefBackend{ref1.Backend, ref2.Backend}, func(git *gitutil.GitCLI) error {
			out, err := git.Run(ctx, "merge-base", ref1.Ref.SHA, ref2.Ref.SHA)
			if err != nil {
				return fmt.Errorf("git merge-base failed: %w", err)
			}
			mergeBase = strings.TrimSpace(string(out))
			return nil
		})
		if err != nil {
			return nil, err
		}

		ref := &gitutil.Ref{SHA: mergeBase}
		backend, err := ref1.Repo.Self().Backend.Get(ctx, ref)
		if err != nil {
			return nil, err
		}
		return &GitRef{Repo: ref1.Repo, Backend: backend, Ref: ref}, nil
	}

	git, commits, cleanup, err := refJoin(ctx, []*GitRef{ref1, ref2})
	if err != nil {
		return nil, err
	}
	defer cleanup()

	out, err := git.Run(ctx, append([]string{"merge-base"}, commits...)...)
	if err != nil {
		return nil, fmt.Errorf("git merge-base failed: %w", err)
	}
	mergeBase := strings.TrimSpace(string(out))

	ref := &gitutil.Ref{SHA: mergeBase}
	backend, err := ref1.Repo.Self().Backend.Get(ctx, ref)
	if err != nil {
		return nil, err
	}
	return &GitRef{Repo: ref1.Repo, Backend: backend, Ref: ref}, nil
}

// refJoin creates a temporary git repository, adds the given refs as remotes,
// fetches them, and returns a GitCLI instance.
func refJoin(ctx context.Context, refs []*GitRef) (_ *gitutil.GitCLI, _ []string, _ func() error, rerr error) {
	tmpDir, err := os.MkdirTemp("", "dagger-mergebase")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	cleanup := func() error {
		return os.RemoveAll(tmpDir)
	}
	defer func() {
		if rerr != nil {
			cleanup()
		}
	}()
	git := gitutil.NewGitCLI(
		gitutil.WithDir(tmpDir),
		gitutil.WithGitDir(filepath.Join(tmpDir, ".git")),
	)
	if _, err := git.Run(ctx, "-c", "init.defaultBranch=main", "init"); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to init temp repo: %w", err)
	}

	eg, egCtx := errgroup.WithContext(ctx)
	mu := sync.Mutex{} // cannot simultaneously add+fetch remotes
	commits := make([]string, len(refs))

	for i, ref := range refs {
		eg.Go(func() error {
			commits[i] = ref.Ref.SHA
			return ref.Backend.mount(egCtx, 0, false, func(gitN *gitutil.GitCLI) error {
				remoteURL, err := gitN.URL(egCtx)
				if err != nil {
					return err
				}
				remoteName := fmt.Sprintf("origin%d", i+1)
				mu.Lock()
				defer mu.Unlock()
				if _, err := git.Run(egCtx, "remote", "add", remoteName, remoteURL); err != nil {
					return fmt.Errorf("failed to add remote %s: %w", remoteName, err)
				}
				if _, err := git.Run(egCtx, "fetch", "--no-tags", remoteName, ref.Ref.SHA); err != nil {
					return fmt.Errorf("failed to fetch ref %d: %w", i+1, err)
				}
				return nil
			})
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, nil, nil, err
	}
	return git, commits, cleanup, nil
}
