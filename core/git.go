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
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

type GitRepository struct {
	URL      dagql.Nullable[dagql.String] `field:"true" doc:"The URL of the git repository."`
	Backend  GitRepositoryBackend
	Remote   *gitutil.Remote
	remoteMu sync.Mutex
	// remoteSession is the session whose ls-remote populated Remote's refs.
	// A remote repository result is shared across sessions, but its refs are
	// only fresh for the session that listed them (RemoteGitRepository.Remote
	// caches ls-remote per session): a later session must list again rather
	// than be answered from an earlier session's tags. Empty for backends
	// whose refs never move, and for restored results, which reload once.
	remoteSession string

	DiscardGitDir bool

	// Remotes is registered routing metadata, not a credential grant: named
	// remotes recorded in materialized checkouts and consulted by push.
	Remotes []GitRemote
}

// GitRemote is a named remote registered on a repository: the remote's name,
// its fetch URL, and any push destinations that differ from it. Registered
// remotes are written into checkouts materialized from the repository and
// route push when no explicit destination is given. They are routing
// metadata only, never a credential grant.
type GitRemote struct {
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	PushURL string `json:"pushURL,omitempty"`
}

func (remote GitRemote) Clone() GitRemote {
	return remote
}

// CloneGitRemotes deep-copies a remote list so registrations never alias.
func CloneGitRemotes(remotes []GitRemote) []GitRemote {
	if remotes == nil {
		return nil
	}
	cloned := make([]GitRemote, len(remotes))
	for i, remote := range remotes {
		cloned[i] = remote.Clone()
	}
	return cloned
}

// WithGitRemote returns remotes with remote registered, replacing any
// existing entry of the same name.
func WithGitRemote(remotes []GitRemote, remote GitRemote) []GitRemote {
	out := CloneGitRemotes(remotes)
	for i := range out {
		if out[i].Name == remote.Name {
			out[i] = remote.Clone()
			return out
		}
	}
	return append(out, remote.Clone())
}

// MergeGitRemotes overlays registered remotes over base ones -- same name,
// the overlay wins -- in a deterministic order (origin first, the rest
// sorted by name) so materialized checkouts converge byte-for-byte.
func MergeGitRemotes(base, overlay []GitRemote) []GitRemote {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	byName := map[string]GitRemote{}
	for _, remote := range base {
		byName[remote.Name] = remote
	}
	for _, remote := range overlay {
		byName[remote.Name] = remote
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		if name != "origin" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if _, ok := byName["origin"]; ok {
		names = append([]string{"origin"}, names...)
	}
	merged := make([]GitRemote, 0, len(names))
	for _, name := range names {
		merged = append(merged, byName[name].Clone())
	}
	return merged
}

// RemoteConfig returns the registered remote with the given name, or nil.
func (repo *GitRepository) RemoteConfig(name string) *GitRemote {
	for i := range repo.Remotes {
		if repo.Remotes[i].Name == name {
			return &repo.Remotes[i]
		}
	}
	return nil
}

type GitRepositoryBackend interface {
	// Remote returns information about the git remote.
	Remote(ctx context.Context) (*gitutil.Remote, error)
	// Get returns a reference to a specific git ref (branch, tag, or commit).
	Get(ctx context.Context, ref *gitutil.Ref) (GitRefBackend, error)

	// ResolveShortSHA expands an abbreviated commit SHA (a 4-40 character hex
	// prefix) to the full SHA of the single matching commit, using only
	// locally available objects. Backends that merely proxy a remote cannot
	// expand prefixes of commits that were never fetched.
	ResolveShortSHA(ctx context.Context, prefix string) (string, error)

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
	Repo     dagql.ObjectResult[*GitRepository]
	Backend  GitRefBackend
	Ref      *gitutil.Ref
	FetchRef *gitutil.Ref

	metadataMu sync.Mutex
	metadata   *GitCommitMetadata
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
	Tree(ctx context.Context, srv *dagql.Server, discard bool, depth int, includeTags bool, remotes []GitRemote) (checkout *Directory, err error)

	mount(ctx context.Context, depth int, includeTags bool, fn func(*gitutil.GitCLI) error) error
}

// SelectLatestGitRef selects the greatest stable release tag in remote after
// normalizing optional v prefixes, incomplete versions, and zero-padded numeric
// components. It falls back to HEAD when no eligible release tag exists.
func SelectLatestGitRef(remote *gitutil.Remote) (*gitutil.Ref, error) {
	return SelectGitRefWithVersionQuery(remote, "", "")
}

// SelectLatestGitRefWithTagPrefix selects the greatest normalized stable
// release tag below tagPrefix. If no matching prefixed release exists,
// repository-wide release tags are considered before falling back to HEAD.
func SelectLatestGitRefWithTagPrefix(
	remote *gitutil.Remote,
	tagPrefix string,
) (*gitutil.Ref, error) {
	return SelectGitRefWithVersionQuery(remote, tagPrefix, "")
}

// SelectGitRefWithVersionQuery selects the greatest release ref that matches
// versionQuery. Tags take priority over branches. An empty query selects the
// latest stable tag and falls back to HEAD when no release exists.
func SelectGitRefWithVersionQuery(
	remote *gitutil.Remote,
	tagPrefix string,
	versionQuery string,
) (*gitutil.Ref, error) {
	if remote == nil {
		return nil, fmt.Errorf("select git ref: nil remote")
	}

	tagPrefix = strings.Trim(tagPrefix, "/")
	if tagPrefix != "" {
		tagPrefix += "/"
	}

	bestRef, err := selectGitRelease(remote.Tags(), tagPrefix, versionQuery)
	if err != nil {
		return nil, err
	}
	if bestRef == "" && tagPrefix != "" {
		bestRef, err = selectGitRelease(remote.Tags(), "", versionQuery)
		if err != nil {
			return nil, err
		}
	}
	if bestRef == "" && versionQuery != "" {
		bestRef, err = selectGitRelease(remote.Branches(), tagPrefix, versionQuery)
		if err != nil {
			return nil, err
		}
		if bestRef == "" && tagPrefix != "" {
			bestRef, err = selectGitRelease(remote.Branches(), "", versionQuery)
			if err != nil {
				return nil, err
			}
		}
	}

	if bestRef == "" {
		if versionQuery != "" {
			return nil, fmt.Errorf("no Git ref matches version query %q", versionQuery)
		}
		ref, err := remote.Lookup("HEAD")
		if err != nil {
			return nil, fmt.Errorf("resolve git remote HEAD: %w", err)
		}
		return ref, nil
	}

	ref, err := remote.Lookup(bestRef)
	if err != nil {
		return nil, fmt.Errorf("resolve latest git release %q: %w", bestRef, err)
	}
	return ref, nil
}

func selectGitRelease(
	remote *gitutil.Remote,
	tagPrefix string,
	versionQuery string,
) (string, error) {
	candidates := make([]releaseTagCandidate, 0, len(remote.Refs))
	refs := map[string]string{}
	for _, ref := range remote.Refs {
		version := ref.ShortName()
		if tagPrefix != "" {
			var ok bool
			version, ok = strings.CutPrefix(version, tagPrefix)
			if !ok {
				continue
			}
		}
		original := ref.ShortName()
		candidates = append(candidates, releaseTagCandidate{
			Name:    original,
			Version: version,
			Target:  ref.SHA,
		})
		refs[original] = ref.Name
	}
	selected, found, err := selectReleaseTag(candidates, versionQuery)
	if err != nil {
		return "", err
	}
	if !found {
		return "", nil
	}
	return refs[selected.Name], nil
}

// ValidateGitLatestRef validates a ref selected by git-latest.
func ValidateGitLatestRef(refName string, tagPrefix string) error {
	return ValidateGitVersionRef(refName, tagPrefix, "")
}

// ValidateGitVersionRef validates a ref selected for versionQuery.
func ValidateGitVersionRef(refName string, tagPrefix string, versionQuery string) error {
	if tag, ok := strings.CutPrefix(refName, "refs/tags/"); ok {
		return validateGitVersionName("tag", tag, tagPrefix, versionQuery)
	}

	if branch, ok := strings.CutPrefix(refName, "refs/heads/"); ok && branch != "" {
		if versionQuery == "" {
			return nil
		}
		return validateGitVersionName("branch", branch, tagPrefix, versionQuery)
	}
	return fmt.Errorf("invalid git-latest ref %q", refName)
}

func validateGitVersionName(kind, name, tagPrefix, versionQuery string) error {
	version := name
	tagPrefix = strings.Trim(tagPrefix, "/")
	if tagPrefix != "" {
		version, _ = strings.CutPrefix(version, tagPrefix+"/")
	}
	parsed, ok := parseReleaseTag(releaseTagCandidate{Name: name, Version: version})
	if !ok {
		return fmt.Errorf("invalid git-latest %s %q: not a semantic version", kind, name)
	}
	query, err := parseReleaseVersionQuery(versionQuery)
	if err != nil {
		return err
	}
	if versionQuery == "" && parsed.Semver.Prerelease != "" {
		return fmt.Errorf("invalid git-latest %s %q: prerelease tags are not supported", kind, name)
	}
	if !query.matches(parsed) {
		return fmt.Errorf("invalid git-latest %s %q: does not match version query %q", kind, name, versionQuery)
	}
	return nil
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

	if remoteBackend, ok := backend.(*RemoteGitRepository); ok {
		repo.URL = dagql.NonNull(dagql.String(remoteBackend.URL.String()))
		repo.Remote = &gitutil.Remote{}
		return repo, nil
	}

	_, err := repo.LoadRemote(ctx)
	if err != nil {
		return nil, err
	}
	return repo, nil
}

// LoadRemote returns remote metadata, loading it once when a resolver needs it.
// Lazy loading allows frozen lock lookups to use pins without network access.
func (repo *GitRepository) LoadRemote(ctx context.Context) (*gitutil.Remote, error) {
	repo.remoteMu.Lock()
	defer repo.remoteMu.Unlock()

	var session string
	if _, remote := repo.Backend.(*RemoteGitRepository); remote {
		if clientMetadata, err := engine.ClientMetadataFromContext(ctx); err == nil {
			session = clientMetadata.SessionID
		}
	}
	if repo.Remote != nil && (repo.Remote.Refs != nil || repo.Remote.Symrefs != nil) && repo.remoteSession == session {
		return repo.Remote, nil
	}

	var head *gitutil.Ref
	if repo.Remote != nil {
		head = repo.Remote.Head
	}
	remote, err := repo.Backend.Remote(ctx)
	if err != nil {
		return nil, err
	}
	if head != nil {
		remote.Head = head
	}
	repo.Remote = remote
	repo.remoteSession = session
	return remote, nil
}

// ResolveShortSHA expands an abbreviated commit SHA the way `git rev-parse`
// does, using the repository's locally available objects. Workspace-backed
// and other engine-side repositories carry their whole object database, so
// any commit's prefix resolves. A remote repository is resolved via
// ls-remote, which only advertises refs: its prefixes can only be expanded
// against commits that have already been fetched into the engine's mirror.
func (repo *GitRepository) ResolveShortSHA(ctx context.Context, prefix string) (string, error) {
	return repo.Backend.ResolveShortSHA(ctx, prefix)
}

// CloneWithBackend returns a repository with fresh remote metadata state. This
// is used when changing authentication so metadata loaded with one credential
// set cannot be reused with another.
func (repo *GitRepository) CloneWithBackend(backend GitRepositoryBackend) *GitRepository {
	repo.remoteMu.Lock()
	defer repo.remoteMu.Unlock()

	clone := &GitRepository{
		URL:           repo.URL,
		Remotes:       CloneGitRemotes(repo.Remotes),
		Backend:       backend,
		Remote:        &gitutil.Remote{},
		DiscardGitDir: repo.DiscardGitDir,
	}
	if repo.Remote != nil && repo.Remote.Head != nil {
		head := *repo.Remote.Head
		clone.Remote.Head = &head
	}
	return clone
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
		if backend.CheckoutBase != nil {
			parent, err := attachLazyInput(attach, backend.CheckoutBase.Parent, "git checkout parent")
			if err != nil {
				return nil, err
			}
			backend.CheckoutBase = &GitCheckoutBase{Parent: parent, CommitSHA: backend.CheckoutBase.CommitSHA}
			owned = append(owned, parent)
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
	if ref == nil {
		return nil, nil
	}
	return attachGitObjectRepo(&ref.Repo, "git ref", attach)
}

func (commit *GitCommit) AttachDependencyResults(
	ctx context.Context,
	_ dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if commit == nil {
		return nil, nil
	}
	return attachGitObjectRepo(&commit.Repo, "git commit", attach)
}

func attachGitObjectRepo(
	repo *dagql.ObjectResult[*GitRepository],
	label string,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	if repo == nil || repo.Self() == nil {
		return nil, nil
	}
	attached, err := attach(*repo)
	if err != nil {
		return nil, fmt.Errorf("attach %s repo: %w", label, err)
	}
	typed, ok := attached.(dagql.ObjectResult[*GitRepository])
	if !ok {
		return nil, fmt.Errorf("attach %s repo: unexpected result %T", label, attached)
	}
	*repo = typed
	return []dagql.AnyResult{typed}, nil
}

const (
	persistedGitRepositoryFormLocal  = "local"
	persistedGitRepositoryFormRemote = "remote"
)

type persistedGitRepositoryPayload struct {
	Form          string                      `json:"form"`
	URL           string                      `json:"url,omitempty"`
	Remotes       []persistedGitRemotePayload `json:"remotes,omitempty"`
	DiscardGitDir bool                        `json:"discardGitDir,omitempty"`
	RemoteJSON    json.RawMessage             `json:"remoteJson,omitempty"`

	Local  *persistedLocalGitRepositoryPayload  `json:"local,omitempty"`
	Remote *persistedRemoteGitRepositoryPayload `json:"remote,omitempty"`
}

type persistedGitRemotePayload struct {
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	PushURL string `json:"pushURL,omitempty"`
}

type persistedLocalGitRepositoryPayload struct {
	DirectoryResultID uint64                    `json:"directoryResultID"`
	CheckoutBase      *persistedGitCheckoutBase `json:"checkoutBase,omitempty"`
}

type persistedGitCheckoutBase struct {
	ParentResultID uint64 `json:"parentResultID"`
	CommitSHA      string `json:"commitSHA"`
}

func (p *persistedGitCheckoutBase) validate() error {
	if p.ParentResultID == 0 || !IsFullGitSHA(p.CommitSHA) {
		return fmt.Errorf("git checkout base: expected parent result and complete commit SHA")
	}
	return nil
}

type persistedRemoteGitRepositoryPayload struct {
	URL           string   `json:"url"`
	SSHKnownHosts string   `json:"sshKnownHosts,omitempty"`
	AuthUsername  string   `json:"authUsername,omitempty"`
	Platform      Platform `json:"platform"`

	// The retained mirror, authentication handles and service bindings are
	// exact row references. Attachment owns them; decode loads them exactly
	// and never selects a mirror by URL. Zero keeps an absent value absent,
	// and the ordinary use path reports a missing mirror. Secret and socket
	// material is resolved only through fresh session binding at use time.
	MirrorResultID        uint64                    `json:"mirrorResultID,omitempty"`
	SSHAuthSocketResultID uint64                    `json:"sshAuthSocketResultID,omitempty"`
	AuthTokenResultID     uint64                    `json:"authTokenResultID,omitempty"`
	AuthHeaderResultID    uint64                    `json:"authHeaderResultID,omitempty"`
	Services              []persistedServiceBinding `json:"services,omitempty"`
}

func encodePersistedRemoteGitRepository(enc *dagql.PersistEncodeContext, backend *RemoteGitRepository) (*persistedRemoteGitRepositoryPayload, error) {
	if backend.URL == nil {
		return nil, fmt.Errorf("encode persisted git repository: remote backend missing URL")
	}
	payload := &persistedRemoteGitRepositoryPayload{
		URL:           backend.URL.String(),
		SSHKnownHosts: backend.SSHKnownHosts,
		AuthUsername:  backend.AuthUsername,
		Platform:      backend.Platform,
	}
	var err error
	if backend.Mirror.Self() != nil {
		if payload.MirrorResultID, err = encodePersistedObjectRef(enc, backend.Mirror, "git repository remote mirror"); err != nil {
			return nil, err
		}
	}
	if backend.SSHAuthSocket.Self() != nil {
		if payload.SSHAuthSocketResultID, err = encodePersistedObjectRef(enc, backend.SSHAuthSocket, "git repository ssh auth socket"); err != nil {
			return nil, err
		}
	}
	if backend.AuthToken.Self() != nil {
		if payload.AuthTokenResultID, err = encodePersistedObjectRef(enc, backend.AuthToken, "git repository auth token"); err != nil {
			return nil, err
		}
	}
	if backend.AuthHeader.Self() != nil {
		if payload.AuthHeaderResultID, err = encodePersistedObjectRef(enc, backend.AuthHeader, "git repository auth header"); err != nil {
			return nil, err
		}
	}
	if payload.Services, err = encodePersistedServiceBindings(enc, "git repository", backend.Services); err != nil {
		return nil, err
	}
	return payload, nil
}

func decodePersistedRemoteGitRepository(ctx context.Context, dec *dagql.PersistDecodeContext, persisted *persistedRemoteGitRepositoryPayload) (*RemoteGitRepository, error) {
	parsedURL, err := gitutil.ParseURL(persisted.URL)
	if err != nil {
		return nil, fmt.Errorf("decode persisted git repository URL: %w", err)
	}
	backend := &RemoteGitRepository{
		URL:           parsedURL,
		SSHKnownHosts: persisted.SSHKnownHosts,
		AuthUsername:  persisted.AuthUsername,
		Platform:      persisted.Platform,
	}
	if backend.Mirror, err = loadPersistedObjectResultByResultID[*RemoteGitMirror](ctx, dec, persisted.MirrorResultID, "git repository remote mirror"); err != nil {
		return nil, err
	}
	if backend.SSHAuthSocket, err = loadPersistedObjectResultByResultID[*Socket](ctx, dec, persisted.SSHAuthSocketResultID, "git repository ssh auth socket"); err != nil {
		return nil, err
	}
	if backend.AuthToken, err = loadPersistedObjectResultByResultID[*Secret](ctx, dec, persisted.AuthTokenResultID, "git repository auth token"); err != nil {
		return nil, err
	}
	if backend.AuthHeader, err = loadPersistedObjectResultByResultID[*Secret](ctx, dec, persisted.AuthHeaderResultID, "git repository auth header"); err != nil {
		return nil, err
	}
	if backend.Services, err = decodePersistedServiceBindings(ctx, dec, "git repository", persisted.Services); err != nil {
		return nil, err
	}
	return backend, nil
}

func (repo *GitRepository) EncodePersistedObject(ctx context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	if repo == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted git repository: nil repository")
	}
	remoteJSON, err := json.Marshal(repo.Remote)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted git repository remote: %w", err)
	}
	payload := persistedGitRepositoryPayload{
		DiscardGitDir: repo.DiscardGitDir,
		RemoteJSON:    remoteJSON,
	}
	for _, remote := range repo.Remotes {
		payload.Remotes = append(payload.Remotes, persistedGitRemotePayload(remote))
	}
	if repo.URL.Valid {
		payload.URL = repo.URL.Value.String()
	}
	switch backend := repo.Backend.(type) {
	case *LocalGitRepository:
		dirID, err := encodePersistedObjectRef(enc, backend.Directory, "git repository directory")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.Form = persistedGitRepositoryFormLocal
		payload.Local = &persistedLocalGitRepositoryPayload{
			DirectoryResultID: dirID,
		}
		if base := backend.CheckoutBase; base != nil {
			parentID, err := encodePersistedObjectRef(enc, base.Parent, "git checkout parent")
			if err != nil {
				return dagql.PersistedObjectEncoding{}, err
			}
			payload.Local.CheckoutBase = &persistedGitCheckoutBase{ParentResultID: parentID, CommitSHA: base.CommitSHA}
			if err := payload.Local.CheckoutBase.validate(); err != nil {
				return dagql.PersistedObjectEncoding{}, err
			}
		}
	case *RemoteGitRepository:
		remote, err := encodePersistedRemoteGitRepository(enc, backend)
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		payload.Form = persistedGitRepositoryFormRemote
		payload.Remote = remote
	default:
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted git repository: unsupported backend %T", repo.Backend)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted git repository payload: %w", err)
	}
	return encodePersistedObjectRawJSON(payloadJSON), nil
}

func (*GitRepository) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
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
	for _, persistedRemote := range persisted.Remotes {
		repo.Remotes = append(repo.Remotes, GitRemote(persistedRemote))
	}
	if persisted.URL != "" {
		repo.URL = dagql.NonNull(dagql.String(persisted.URL))
	}
	switch persisted.Form {
	case persistedGitRepositoryFormLocal:
		if persisted.Local == nil {
			return nil, fmt.Errorf("decode persisted git repository: missing local payload")
		}
		dir, err := loadPersistedObjectResultByResultID[*Directory](ctx, dec, persisted.Local.DirectoryResultID, "git repository directory")
		if err != nil {
			return nil, err
		}
		backend := &LocalGitRepository{Directory: dir}
		if base := persisted.Local.CheckoutBase; base != nil {
			if err := base.validate(); err != nil {
				return nil, err
			}
			parent, err := loadPersistedObjectResultByResultID[*GitRef](ctx, dec, base.ParentResultID, "git checkout parent")
			if err != nil {
				return nil, err
			}
			backend.CheckoutBase = &GitCheckoutBase{Parent: parent, CommitSHA: base.CommitSHA}
		}
		repo.Backend = backend
	case persistedGitRepositoryFormRemote:
		if persisted.Remote == nil {
			return nil, fmt.Errorf("decode persisted git repository: missing remote payload")
		}
		backend, err := decodePersistedRemoteGitRepository(ctx, dec, persisted.Remote)
		if err != nil {
			return nil, err
		}
		repo.Backend = backend
		repo.URL = dagql.NonNull(dagql.String(backend.URL.String()))
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

func (ref *GitRef) EncodePersistedObject(ctx context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	if ref == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted git ref: nil ref")
	}
	if ref.Ref == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted git ref: missing ref")
	}
	repoID, err := encodePersistedObjectRef(enc, ref.Repo, "git ref repo")
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	payloadJSON, err := json.Marshal(persistedGitRefPayload{
		RepoResultID: repoID,
		Name:         ref.Ref.Name,
		SHA:          ref.Ref.SHA,
	})
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted git ref payload: %w", err)
	}
	return encodePersistedObjectRawJSON(payloadJSON), nil
}

func (*GitRef) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedGitRefPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted git ref payload: %w", err)
	}
	repo, err := loadPersistedObjectResultByResultID[*GitRepository](ctx, dec, persisted.RepoResultID, "git ref repo")
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

type persistedGitCommitPayload struct {
	RepoResultID uint64 `json:"repoResultID"`
	SHA          string `json:"sha"`
	FetchName    string `json:"fetchName,omitempty"`
}

func (commit *GitCommit) EncodePersistedObject(ctx context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	if commit == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted git commit: nil commit")
	}
	if commit.Ref == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted git commit: missing ref")
	}
	if commit.Ref.SHA == "" {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted git commit: missing commit SHA")
	}
	repoID, err := encodePersistedObjectRef(enc, commit.Repo, "git commit repo")
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	fetchName := ""
	if commit.FetchRef != nil {
		fetchName = commit.FetchRef.Name
	}
	payloadJSON, err := json.Marshal(persistedGitCommitPayload{
		RepoResultID: repoID,
		SHA:          commit.Ref.SHA,
		FetchName:    fetchName,
	})
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted git commit payload: %w", err)
	}
	return encodePersistedObjectRawJSON(payloadJSON), nil
}

func (*GitCommit) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedGitCommitPayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted git commit payload: %w", err)
	}
	repo, err := loadPersistedObjectResultByResultID[*GitRepository](ctx, dec, persisted.RepoResultID, "git commit repo")
	if err != nil {
		return nil, err
	}
	ref := &gitutil.Ref{SHA: persisted.SHA}
	fetchRef := ref
	if persisted.FetchName != "" {
		fetchRef = &gitutil.Ref{
			Name: persisted.FetchName,
			SHA:  persisted.SHA,
		}
	}
	backend, err := repo.Self().Backend.Get(ctx, fetchRef)
	if err != nil {
		return nil, err
	}
	return &GitCommit{
		Repo:     repo,
		Backend:  backend,
		Ref:      ref,
		FetchRef: fetchRef,
	}, nil
}

func (ref *GitRef) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int, includeTags bool) (*Directory, error) {
	return ref.Backend.Tree(ctx, srv, ref.Repo.Self().DiscardGitDir || discardGitDir, depth, includeTags, ref.Repo.Self().Remotes)
}

func (commit *GitCommit) Tree(ctx context.Context, srv *dagql.Server, discardGitDir bool, depth int, includeTags bool) (*Directory, error) {
	if commit == nil || commit.Ref == nil {
		return nil, fmt.Errorf("git commit tree: missing commit")
	}
	if err := commit.prefetch(ctx, depth, includeTags); err != nil {
		return nil, err
	}
	backend, err := commit.Repo.Self().Backend.Get(ctx, commit.Ref)
	if err != nil {
		return nil, err
	}
	return backend.Tree(ctx, srv, commit.Repo.Self().DiscardGitDir || discardGitDir, depth, includeTags, commit.Repo.Self().Remotes)
}

func (commit *GitCommit) Metadata(ctx context.Context) (*GitCommitMetadata, error) {
	if commit == nil || commit.Ref == nil {
		return nil, fmt.Errorf("git commit metadata: missing commit")
	}
	if commit.Ref.SHA == "" {
		return nil, fmt.Errorf("git commit metadata: missing commit SHA")
	}
	if commit.Backend == nil {
		return nil, fmt.Errorf("git commit metadata: missing backend")
	}

	commit.metadataMu.Lock()
	defer commit.metadataMu.Unlock()
	if commit.metadata != nil {
		return commit.metadata, nil
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
	commit.metadata = meta
	return meta, nil
}

// PrefillMetadata seeds the commit's cached metadata, so that reading its
// fields doesn't have to mount the repository again. Metadata that has already
// been read wins; it was read from the same commit object either way.
func (commit *GitCommit) PrefillMetadata(meta *GitCommitMetadata) {
	if commit == nil || meta == nil {
		return
	}
	commit.metadataMu.Lock()
	defer commit.metadataMu.Unlock()
	if commit.metadata == nil {
		commit.metadata = meta
	}
}

func (commit *GitCommit) prefetch(ctx context.Context, depth int, includeTags bool) error {
	return commit.Mount(ctx, depth, includeTags, func(*gitutil.GitCLI) error {
		return nil
	})
}

// Mount mounts the commit's repository with this commit available at the requested depth.
func (commit *GitCommit) Mount(ctx context.Context, depth int, includeTags bool, fn func(*gitutil.GitCLI) error) error {
	if commit == nil || commit.Backend == nil {
		return fmt.Errorf("git commit: missing backend")
	}
	if fn == nil {
		fn = func(*gitutil.GitCLI) error { return nil }
	}
	return commit.Backend.mount(ctx, depth, includeTags, fn)
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
	// git tolerates malformed and oversized timezone offsets in commit
	// objects, and imported history commonly has them; the timestamp itself
	// is still exact, so degrade to UTC rather than failing the commit (and
	// with it any log containing the commit)
	loc := time.UTC
	if offset, ok := parseGitTimezoneOffset(dateParts[1]); ok {
		loc = time.FixedZone(dateParts[1], offset)
	}
	return gitCommitSignature{
		Name:  name,
		Email: email,
		Date:  time.Unix(seconds, 0).In(loc).Format(time.RFC3339),
	}, nil
}

// parseGitTimezoneOffset parses a timezone offset the way git does: a sign
// followed by decimal digits interpreted as hours*100+minutes, so oversized
// forms like +051800 (git renders it as +518:00) are accepted. Offsets that
// RFC3339 cannot represent (beyond +/-23:59) report !ok so the caller can
// fall back to UTC.
func parseGitTimezoneOffset(raw string) (int, bool) {
	if len(raw) < 2 || (raw[0] != '+' && raw[0] != '-') {
		return 0, false
	}
	n, err := strconv.Atoi(raw[1:])
	if err != nil || n < 0 {
		return 0, false
	}
	offset := ((n/100)*60 + n%100) * 60
	if raw[0] == '-' {
		offset = -offset
	}
	if offset <= -24*60*60 || offset >= 24*60*60 {
		return 0, false
	}
	return offset, true
}

// doGitCheckout performs a git checkout using the given git helper.
//
// remotes are written into the checkout's configuration (remote.<name>.url
// and remote.<name>.pushurl), so the result stays resolvable by remote-aware
// tooling; the checkout itself always fetches from cloneURL.
//
// The provided git dir should *always* be empty.
func doGitCheckout(
	ctx context.Context,
	checkoutGit *gitutil.GitCLI,
	remotes []GitRemote,
	cloneURL string,
	ref *gitutil.Ref,
	depth int,
	discardGitDir bool,
) error {
	_, err := checkoutGit.Run(ctx, "-c", "init.defaultBranch=main", "init")
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
	return finishGitCheckout(ctx, checkoutGit, remotes, cloneURL, ref, discardGitDir, tmpref)
}

// finishGitCheckout materializes a ref whose objects are already available.
// tmpref is set only when the caller fetched the objects into a temporary ref.
func finishGitCheckout(
	ctx context.Context,
	checkoutGit *gitutil.GitCLI,
	remotes []GitRemote,
	cloneURL string,
	ref *gitutil.Ref,
	discardGitDir bool,
	tmpref string,
) error {
	checkoutDirGit, err := checkoutGit.GitDir(ctx)
	if err != nil {
		return fmt.Errorf("could not find git dir: %w", err)
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
	for _, remote := range remotes {
		if err := writeGitCheckoutRemote(ctx, checkoutGit, remote); err != nil {
			return err
		}
	}
	if tmpref != "" {
		_, err = checkoutGit.Run(ctx, "update-ref", "-d", tmpref)
		if err != nil {
			return fmt.Errorf("failed to delete tmp ref: %w", err)
		}
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

	if !discardGitDir {
		if _, err := checkoutGit.Run(ctx, "read-tree", "HEAD"); err != nil {
			return fmt.Errorf("failed to normalize git index: %w", err)
		}
	}

	if discardGitDir {
		if err := os.RemoveAll(checkoutDirGit); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to remove .git: %w", err)
		}
	}

	checkoutDir, err := checkoutGit.WorkTree(ctx)
	if err != nil {
		return fmt.Errorf("could not find worktree: %w", err)
	}
	// Use a deterministic non-zero timestamp. Some build tools treat missing
	// outputs as epoch and skip initial copies when sources are also epoch.
	normalizedTime := []unix.Timespec{{Sec: 1}, {Sec: 1}}
	if err := filepath.WalkDir(checkoutDir, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return unix.UtimesNanoAt(unix.AT_FDCWD, path, normalizedTime, unix.AT_SYMLINK_NOFOLLOW)
	}); err != nil {
		return fmt.Errorf("failed to normalize checkout timestamps: %w", err)
	}

	return nil
}

// writeGitCheckoutRemote records one remote in a checkout's configuration:
// its fetch URL (when known) and its push destination, if any.
func writeGitCheckoutRemote(ctx context.Context, checkoutGit *gitutil.GitCLI, remote GitRemote) error {
	if remote.Name == "" || (remote.URL == "" && remote.PushURL == "") {
		return nil
	}
	if remote.URL != "" {
		if _, err := checkoutGit.Run(ctx, "remote", "add", remote.Name, remote.URL); err != nil {
			return fmt.Errorf("failed to add remote %s: %w", remote.Name, err)
		}
	}
	if remote.PushURL != "" {
		if _, err := checkoutGit.Run(ctx, "config", "remote."+remote.Name+".pushurl", remote.PushURL); err != nil {
			return fmt.Errorf("failed to set remote %s push URL: %w", remote.Name, err)
		}
	}
	return nil
}

// mountRefs mounts the given refs with their full history and calls fn with a
// GitCLI positioned in a repository containing all of them, along with their
// resolved commit SHAs (in the same order as refs).
//
// Refs sharing a repository are mounted together. Local repositories can share
// their cached objects read-only; other repositories use refJoin.
func mountRefs(ctx context.Context, refs []*GitRef, fn func(git *gitutil.GitCLI, shas []string) error) error {
	if len(refs) == 0 {
		return fmt.Errorf("mount refs: no refs given")
	}
	// A single ref needs neither repository identity nor a joined object store.
	if len(refs) == 1 {
		return refs[0].Backend.mount(ctx, 0, false, func(git *gitutil.GitCLI) error {
			return fn(git, []string{refs[0].Ref.SHA})
		})
	}

	allLocal := true
	for _, ref := range refs {
		if _, ok := ref.Backend.(*LocalGitRef); !ok {
			allLocal = false
			break
		}
	}
	if allLocal {
		err := mountCachedGitRefs(ctx, refs, fn)
		if !errors.Is(err, errShallowCachedGitHistory) {
			return err
		}
	}

	shas := make([]string, len(refs))
	backends := make([]GitRefBackend, len(refs))
	sameRepo := true
	var repoDgst digest.Digest
	for i, ref := range refs {
		shas[i] = ref.Ref.SHA
		backends[i] = ref.Backend

		dgst, err := ref.Repo.RecipeDigest(ctx)
		if err != nil {
			return fmt.Errorf("mount refs: ref %d repo ID: %w", i+1, err)
		}
		if i == 0 {
			repoDgst = dgst
		} else if dgst != repoDgst {
			sameRepo = false
		}
	}

	if sameRepo { // fast-path, just grab all the refs from the same repo
		// depth 0 = full fetch, so that history is available
		return refs[0].Repo.Self().Backend.mount(ctx, 0, false, backends, func(git *gitutil.GitCLI) error {
			return fn(git, shas)
		})
	}

	git, shas, cleanup, err := refJoin(ctx, refs)
	if err != nil {
		return err
	}
	defer cleanup()

	return fn(git, shas)
}

func MergeBase(ctx context.Context, ref1 *GitRef, ref2 *GitRef) (*GitRef, error) {
	var mergeBase string
	err := mountRefs(ctx, []*GitRef{ref1, ref2}, func(git *gitutil.GitCLI, shas []string) error {
		out, err := git.Run(ctx, append([]string{"merge-base"}, shas...)...)
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

type GitLogOptions struct {
	// Limit is the maximum number of commits to return. Must be at least 1.
	Limit int
	// Paths restricts the log to commits touching any of these repo-root-relative
	// paths.
	Paths []string
	// Base excludes commits reachable from this ref, i.e. base..ref.
	Base *GitRef
}

// Log returns metadata for the commits reachable from the ref, newest first,
// starting with the ref's own commit.
func (ref *GitRef) Log(ctx context.Context, opts GitLogOptions) ([]*GitCommitMetadata, error) {
	if ref == nil || ref.Ref == nil {
		return nil, fmt.Errorf("git log: missing ref")
	}
	if opts.Limit < 1 {
		return nil, fmt.Errorf("git log: limit must be at least 1, got %d", opts.Limit)
	}

	refs := []*GitRef{ref}
	if opts.Base != nil {
		refs = append(refs, opts.Base)
	}

	var commits []*GitCommitMetadata
	needsFullHistory := false
	readLog := func(git *gitutil.GitCLI, shas []string) error {
		args := []string{"rev-list", "-n", strconv.Itoa(opts.Limit), shas[0]}
		if len(shas) > 1 {
			args = append(args, "^"+shas[1])
		}
		if len(opts.Paths) > 0 {
			args = append(args, "--")
			args = append(args, opts.Paths...)
		}
		out, err := git.Run(ctx, args...)
		if err != nil {
			return fmt.Errorf("git rev-list failed: %w", err)
		}

		logSHAs := strings.Fields(string(out))
		if opts.Base == nil && len(opts.Paths) == 0 && !needsFullHistory {
			// A shared mirror can have uneven shallow boundaries (for example,
			// a merge's second parent fetched separately). The mount's depth
			// estimate alone does not guarantee this walk is complete.
			needsFullHistory, err = gitLogReachesShallowBoundary(ctx, git, logSHAs, opts.Limit)
			if err != nil || needsFullHistory {
				return err
			}
		}

		// read every commit while the repo is still mounted, rather than leaving
		// each one to mount again on demand
		for _, sha := range logSHAs {
			raw, err := git.Run(ctx, "cat-file", "commit", sha)
			if err != nil {
				return fmt.Errorf("read git commit metadata for %s: %w", sha, err)
			}
			meta, err := parseGitCommitMetadata(sha, string(raw))
			if err != nil {
				return fmt.Errorf("read git commit metadata for %s: %w", sha, err)
			}
			commits = append(commits, meta)
		}
		return nil
	}

	var err error
	if opts.Base == nil && len(opts.Paths) == 0 {
		// Without filtering, at most Limit generations can contribute to the
		// first Limit commits. Avoid unshallowing the entire remote just to
		// read a short log. Path filters and base exclusions need full history.
		err = ref.Backend.mount(ctx, opts.Limit, false, func(git *gitutil.GitCLI) error {
			return readLog(git, []string{ref.Ref.SHA})
		})
	} else {
		needsFullHistory = true
	}
	if err == nil && needsFullHistory {
		err = mountRefs(ctx, refs, readLog)
	}
	if err != nil {
		return nil, err
	}
	return commits, nil
}

// gitLogReachesShallowBoundary reports whether the bounded walk may have omitted
// ancestors. The final commit need not have traversable parents when the limit
// was reached: its metadata is read from the raw object, not the shallow graph.
func gitLogReachesShallowBoundary(ctx context.Context, git *gitutil.GitCLI, shas []string, limit int) (bool, error) {
	if len(shas) == limit {
		shas = shas[:len(shas)-1]
	}
	if len(shas) == 0 {
		return false, nil
	}
	// Linked worktrees keep shallow boundaries in the common directory, not
	// their own git directory. Let Git resolve the effective shallow file.
	shallowPath, err := git.Run(ctx, "rev-parse", "--path-format=absolute", "--git-path", "shallow")
	if err != nil {
		return false, err
	}
	shallow, err := os.ReadFile(strings.TrimSuffix(string(shallowPath), "\n"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read git shallow boundaries: %w", err)
	}
	boundaries := make(map[string]struct{})
	for _, sha := range strings.Fields(string(shallow)) {
		boundaries[sha] = struct{}{}
	}
	for _, sha := range shas {
		if _, ok := boundaries[sha]; ok {
			return true, nil
		}
	}
	return false, nil
}

var errShallowCachedGitHistory = errors.New("cannot share shallow git history")

// mountCachedGitRefs borrows object databases for the duration of fn, without
// fetching or copying any objects. Only use this for local, read-only mounts:
// recursively mounting remote refs can deadlock on a shared mirror lock.
func mountCachedGitRefs(ctx context.Context, refs []*GitRef, fn func(*gitutil.GitCLI, []string) error) error {
	var objects []string
	var shas []string
	var objectFormat string
	var mountNext func(int) error
	mountNext = func(i int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i == len(refs) {
			return withGitObjectView(ctx, objects, objectFormat, func(git *gitutil.GitCLI) error {
				return fn(git, shas)
			})
		}
		return refs[i].Backend.mount(ctx, 0, false, func(git *gitutil.GitCLI) error {
			shallow, err := git.Run(ctx, "rev-parse", "--is-shallow-repository")
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(shallow)) != "false" {
				// Alternates share objects, not shallow boundaries. Dropping the
				// boundaries invents history; unioning them can truncate another
				// source's complete history. Leave shallow inputs to refJoin.
				return errShallowCachedGitHistory
			}
			format, err := git.Run(ctx, "rev-parse", "--show-object-format")
			if err != nil {
				return err
			}
			if i == 0 {
				objectFormat = strings.TrimSpace(string(format))
			} else if strings.TrimSpace(string(format)) != objectFormat {
				return fmt.Errorf("cannot compare git repositories with different object formats")
			}
			// --git-path resolves the common directory for linked worktrees.
			// Strip only the output terminator: whitespace can be part of a path.
			path, err := git.Run(ctx, "rev-parse", "--path-format=absolute", "--git-path", "objects")
			if err != nil {
				return err
			}
			objects = append(objects, strings.TrimSuffix(string(path), "\n"))
			shas = append(shas, refs[i].Ref.SHA)
			return mountNext(i + 1)
		})
	}
	return mountNext(0)
}

// withGitObjectView creates only private repository metadata. Every source
// object database (including its own alternates) stays read-only and mounted
// until the callback returns. No refs or configuration are imported.
func withGitObjectView(ctx context.Context, objects []string, format string, fn func(*gitutil.GitCLI) error) error {
	tmp, err := os.MkdirTemp("", "dagger-git-history-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	git := gitutil.NewGitCLI(gitutil.WithDir(tmp), gitutil.WithGitDir(tmp))
	if _, err := git.Run(ctx, "-c", "init.defaultBranch=main", "init", "--bare", "--object-format="+format); err != nil {
		return fmt.Errorf("initialize git history view: %w", err)
	}
	var alternates strings.Builder
	for _, path := range objects {
		// Git's alternates file accepts C-quoted paths, one per line. In
		// particular, a newline or quote in a mount path must not add an entry.
		alternates.WriteByte('"')
		alternates.WriteString(strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n").Replace(path))
		alternates.WriteString("\"\n")
	}
	if err := os.WriteFile(filepath.Join(tmp, "objects", "info", "alternates"), []byte(alternates.String()), 0600); err != nil {
		return fmt.Errorf("write git history alternates: %w", err)
	}
	return fn(git)
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

// visitPersistedRemoteGitRepositoryRefs walks every declared reference of a
// remote Git repository payload: the retained mirror row, the authentication
// handles and the ordered service bindings.
func visitPersistedRemoteGitRepositoryRefs(w *persistedRefWalker, p *persistedRemoteGitRepositoryPayload) error {
	if err := w.child("mirrorResultID", &p.MirrorResultID); err != nil {
		return err
	}
	if err := w.child("sshAuthSocketResultID", &p.SSHAuthSocketResultID); err != nil {
		return err
	}
	if err := w.child("authTokenResultID", &p.AuthTokenResultID); err != nil {
		return err
	}
	if err := w.child("authHeaderResultID", &p.AuthHeaderResultID); err != nil {
		return err
	}
	return w.services("services", p.Services)
}

const persistedDirectoryLazyKindGitTree = "gitTree"

type DirectoryGitTreeLazy struct {
	LazyState
	ContentDigest digest.Digest
	Ref           dagql.ObjectResult[*GitRef]
	DiscardGitDir bool
	Depth         int
	IncludeTags   bool
}

type persistedDirectoryGitTreeLazy struct {
	ContentDigest digest.Digest `json:"contentDigest,omitempty"`
	RefResultID   uint64        `json:"refResultID"`
	DiscardGitDir bool          `json:"discardGitDir"`
	Depth         int           `json:"depth"`
	IncludeTags   bool          `json:"includeTags"`
}

func (p *persistedDirectoryGitTreeLazy) validate() error {
	if p.RefResultID == 0 {
		return fmt.Errorf("DirectoryGitTreeLazy: missing refResultID")
	}
	return nil
}
func (lazy *DirectoryGitTreeLazy) Evaluate(ctx context.Context, dir *Directory) error {
	if err := deferGitTreeContentDigest(ctx, dir, lazy.ContentDigest); err != nil {
		return err
	}
	return evaluateGitTreeInto(ctx, &lazy.LazyState, "GitRef.tree", dir, func(ctx context.Context, srv *dagql.Server) (*Directory, error) {
		input := lazy.Ref.Self()
		if input == nil || input.Ref == nil || input.Ref.SHA == "" {
			return nil, fmt.Errorf("DirectoryGitTreeLazy: missing Ref SHA")
		}
		return gitRefTreeInto(ctx, dir, input, srv, lazy.DiscardGitDir, lazy.Depth, lazy.IncludeTags)
	})
}

// A tree's recipe retains its credential-bearing ref until materialization.
// Only successful materialization may equate outputs across those scopes.
func deferGitTreeContentDigest(ctx context.Context, dir *Directory, contentDigest digest.Digest) error {
	if contentDigest == "" || dir == nil || dir.PartHostBinding() == nil {
		return nil
	}
	return dir.PartHostBinding().SetContentDigestAfterEvaluation(ctx, contentDigest, call.ExtraDigestLabelRemoteCache)
}

func deferPrivateGitTreeContentDigest(ctx context.Context, operation Lazy[*Directory]) error {
	var contentDigest digest.Digest
	switch lazy := operation.(type) {
	case *DirectoryGitTreeLazy:
		contentDigest = lazy.ContentDigest
	case *DirectoryGitCommitTreeLazy:
		contentDigest = lazy.ContentDigest
	}
	if contentDigest == "" {
		return nil
	}
	return dagql.PartTaskFromContext(ctx).SetContentDigestAfterEvaluation(contentDigest, call.ExtraDigestLabelRemoteCache)
}

// evaluateGitTreeInto is the shared evaluation of the two git tree recipes:
// validate the receiver, resolve the server, produce the tree into dir, and
// release whatever the producer left unmoved.
func evaluateGitTreeInto(ctx context.Context, state *LazyState, op string, dir *Directory, produce func(context.Context, *dagql.Server) (*Directory, error)) error {
	var unmoved *Directory
	err := dir.evaluateLazy(ctx, state, op, func(ctx context.Context) error {
		if err := validateLazyDirectoryReceiver(dir); err != nil {
			return err
		}
		srv, err := CurrentDagqlServer(ctx)
		if err != nil {
			return err
		}
		unmoved, err = produce(ctx, srv)
		return err
	})
	if unmoved != nil {
		err = errors.Join(err, unmoved.OnRelease(context.WithoutCancel(ctx)))
	}
	return err
}

func gitRefTreeInto(ctx context.Context, dst *Directory, input *GitRef, srv *dagql.Server, discardGitDir bool, depth int, includeTags bool) (*Directory, error) {
	if err := validateLazyDirectoryReceiver(dst); err != nil {
		return nil, err
	}
	src, err := input.Tree(ctx, srv, discardGitDir, depth, includeTags)
	if err != nil {
		return nil, err
	}
	if err := moveDirectoryOutput(dst, src); err != nil {
		return src, err
	}
	return nil, nil
}
func (lazy *DirectoryGitTreeLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	ref, err := attachLazyInput(attach, lazy.Ref, "DirectoryGitTreeLazy.Ref")
	if err != nil {
		return nil, err
	}
	lazy.Ref = ref
	return []dagql.AnyResult{ref}, nil
}
func (lazy *DirectoryGitTreeLazy) EncodePersisted(ctx context.Context, enc *dagql.PersistEncodeContext) (json.RawMessage, error) {
	refID, err := encodePersistedObjectRef(enc, lazy.Ref, "DirectoryGitTreeLazy.Ref")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryGitTreeLazy{ContentDigest: lazy.ContentDigest, RefResultID: refID, DiscardGitDir: lazy.DiscardGitDir, Depth: lazy.Depth, IncludeTags: lazy.IncludeTags})
}
func decodeDirectoryGitTreeLazy(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (Lazy[*Directory], error) {
	var p persistedDirectoryGitTreeLazy
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("decode DirectoryGitTreeLazy: %w", err)
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	ref, err := loadPersistedObjectResultByResultID[*GitRef](ctx, dec, p.RefResultID, "DirectoryGitTreeLazy.Ref")
	if err != nil {
		return nil, err
	}
	return &DirectoryGitTreeLazy{LazyState: NewLazyState(), ContentDigest: p.ContentDigest, Ref: ref, DiscardGitDir: p.DiscardGitDir, Depth: p.Depth, IncludeTags: p.IncludeTags}, nil
}

const persistedDirectoryLazyKindGitCommitTree = "gitCommitTree"

type DirectoryGitCommitTreeLazy struct {
	LazyState
	ContentDigest digest.Digest
	Commit        dagql.ObjectResult[*GitCommit]
	DiscardGitDir bool
	Depth         int
	IncludeTags   bool
}

type persistedDirectoryGitCommitTreeLazy struct {
	ContentDigest  digest.Digest `json:"contentDigest,omitempty"`
	CommitResultID uint64        `json:"commitResultID"`
	DiscardGitDir  bool          `json:"discardGitDir"`
	Depth          int           `json:"depth"`
	IncludeTags    bool          `json:"includeTags"`
}

func (p *persistedDirectoryGitCommitTreeLazy) validate() error {
	if p.CommitResultID == 0 {
		return fmt.Errorf("DirectoryGitCommitTreeLazy: missing commitResultID")
	}
	return nil
}
func (lazy *DirectoryGitCommitTreeLazy) Evaluate(ctx context.Context, dir *Directory) error {
	if err := deferGitTreeContentDigest(ctx, dir, lazy.ContentDigest); err != nil {
		return err
	}
	return evaluateGitTreeInto(ctx, &lazy.LazyState, "GitCommit.tree", dir, func(ctx context.Context, srv *dagql.Server) (*Directory, error) {
		input := lazy.Commit.Self()
		if input == nil || input.Ref == nil || input.Ref.SHA == "" {
			return nil, fmt.Errorf("DirectoryGitCommitTreeLazy: missing Commit SHA")
		}
		return gitCommitTreeInto(ctx, dir, input, srv, lazy.DiscardGitDir, lazy.Depth, lazy.IncludeTags)
	})
}

func gitCommitTreeInto(ctx context.Context, dst *Directory, input *GitCommit, srv *dagql.Server, discardGitDir bool, depth int, includeTags bool) (*Directory, error) {
	if err := validateLazyDirectoryReceiver(dst); err != nil {
		return nil, err
	}
	src, err := input.Tree(ctx, srv, discardGitDir, depth, includeTags)
	if err != nil {
		return nil, err
	}
	if err := moveDirectoryOutput(dst, src); err != nil {
		return src, err
	}
	return nil, nil
}
func (lazy *DirectoryGitCommitTreeLazy) AttachDependencies(ctx context.Context, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	commit, err := attachLazyInput(attach, lazy.Commit, "DirectoryGitCommitTreeLazy.Commit")
	if err != nil {
		return nil, err
	}
	lazy.Commit = commit
	return []dagql.AnyResult{commit}, nil
}
func (lazy *DirectoryGitCommitTreeLazy) EncodePersisted(ctx context.Context, enc *dagql.PersistEncodeContext) (json.RawMessage, error) {
	commitID, err := encodePersistedObjectRef(enc, lazy.Commit, "DirectoryGitCommitTreeLazy.Commit")
	if err != nil {
		return nil, err
	}
	return json.Marshal(persistedDirectoryGitCommitTreeLazy{ContentDigest: lazy.ContentDigest, CommitResultID: commitID, DiscardGitDir: lazy.DiscardGitDir, Depth: lazy.Depth, IncludeTags: lazy.IncludeTags})
}
func decodeDirectoryGitCommitTreeLazy(ctx context.Context, dec *dagql.PersistDecodeContext, payload json.RawMessage) (Lazy[*Directory], error) {
	var p persistedDirectoryGitCommitTreeLazy
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("decode DirectoryGitCommitTreeLazy: %w", err)
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	commit, err := loadPersistedObjectResultByResultID[*GitCommit](ctx, dec, p.CommitResultID, "DirectoryGitCommitTreeLazy.Commit")
	if err != nil {
		return nil, err
	}
	return &DirectoryGitCommitTreeLazy{LazyState: NewLazyState(), ContentDigest: p.ContentDigest, Commit: commit, DiscardGitDir: p.DiscardGitDir, Depth: p.Depth, IncludeTags: p.IncludeTags}, nil
}
