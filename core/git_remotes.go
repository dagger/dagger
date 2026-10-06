package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/vektah/gqlparser/v2/ast"
)

// GitRemoteHandle exposes routing metadata without granting credentials. Its
// source dependency retains any remote capability the repository already owns.
type GitRemoteHandle struct {
	Name   dagql.String `field:"true" doc:"The remote's name."`
	URL    string
	Source dagql.ObjectResult[*GitRepository]
}

func (*GitRemoteHandle) Type() *ast.Type {
	return &ast.Type{NamedType: "GitRemote", NonNull: true}
}

func (*GitRemoteHandle) TypeDescription() string {
	return "A named reference to a remote Git repository."
}

func (remote *GitRemoteHandle) AttachDependencyResults(_ context.Context, _ dagql.AnyResult, attach func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return attachGitObjectRepo(&remote.Source, "git remote", attach)
}

type persistedGitRemoteHandle struct {
	Name           string `json:"name"`
	URL            string `json:"url,omitempty"`
	SourceResultID uint64 `json:"sourceResultID"`
}

func (remote *GitRemoteHandle) EncodePersistedObject(_ context.Context, enc *dagql.PersistEncodeContext) (dagql.PersistedObjectEncoding, error) {
	id, err := encodePersistedObjectRef(enc, remote.Source, "git remote source")
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	data, err := json.Marshal(persistedGitRemoteHandle{Name: remote.Name.String(), URL: remote.URL, SourceResultID: id})
	if err != nil {
		return dagql.PersistedObjectEncoding{}, err
	}
	return encodePersistedObjectRawJSON(data), nil
}

func (*GitRemoteHandle) DecodePersistedObject(ctx context.Context, dec *dagql.PersistDecodeContext, data json.RawMessage) (dagql.Typed, error) {
	var payload persistedGitRemoteHandle
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.Name == "" || payload.SourceResultID == 0 {
		return nil, fmt.Errorf("decode git remote: missing name or source")
	}
	source, err := loadPersistedObjectResultByResultID[*GitRepository](ctx, dec, payload.SourceResultID, "git remote source")
	if err != nil {
		return nil, err
	}
	return &GitRemoteHandle{Name: dagql.String(payload.Name), URL: payload.URL, Source: source}, nil
}

// ConfiguredRemotes reads the effective configuration without contacting any
// remote. Explicit registrations override storage configuration by name.
func (repo *GitRepository) ConfiguredRemotes(ctx context.Context) ([]GitRemote, string, error) {
	if repo.UpstreamRemote != nil {
		return MergeGitRemotes(nil, repo.Remotes), *repo.UpstreamRemote, nil
	}
	var remotes []GitRemote
	var upstream string
	switch backend := repo.Backend.(type) {
	case *LocalGitRepository:
		err := backend.mount(ctx, 0, false, nil, func(git *gitutil.GitCLI) error {
			var err error
			remotes, err = readGitConfigRemotes(ctx, git)
			if err != nil {
				return err
			}
			branch, err := git.New(gitutil.WithIgnoreError()).Run(ctx, "symbolic-ref", "-q", "HEAD")
			if err != nil {
				return err
			}
			if repo.Remote != nil && repo.Remote.Head != nil {
				branch = []byte(repo.Remote.Head.Name)
			}
			upstream, err = gitBranchUpstream(ctx, git, strings.TrimSpace(string(branch)))
			return err
		})
		if err != nil {
			return nil, "", err
		}
	case *RemoteGitRepository:
		remotes = []GitRemote{{Name: "origin", URL: backend.URL.Remote(), Implicit: true}}
	}
	return MergeGitRemotes(remotes, repo.Remotes), upstream, nil
}

func gitBranchUpstream(ctx context.Context, git *gitutil.GitCLI, name string) (string, error) {
	if !strings.HasPrefix(name, "refs/heads/") {
		return "", nil
	}
	out, err := git.Run(ctx, "for-each-ref", "--format=%(upstream:remotename)", "--", name)
	return strings.TrimSpace(string(out)), err
}

func readGitRemoteSelection(ctx context.Context, git *gitutil.GitCLI) ([]GitRemote, *string, error) {
	out, err := git.New(gitutil.WithIgnoreError()).Run(ctx, "config", "--get-regexp", "^dagger\\.upstreamremote$")
	if err != nil {
		return nil, nil, err
	}
	key, upstream, found := strings.Cut(strings.TrimSuffix(string(out), "\n"), " ")
	if !found || key == "" {
		return nil, nil, nil
	}
	data, err := git.Run(ctx, "config", "--get", "dagger.remotes")
	if err != nil {
		return nil, nil, err
	}
	var remotes []GitRemote
	if err := json.Unmarshal(data, &remotes); err != nil {
		return nil, nil, err
	}
	return remotes, &upstream, nil
}

// readGitRemoteSelectionForRef prefers a saved selection over branch tracking.
func readGitRemoteSelectionForRef(ctx context.Context, git *gitutil.GitCLI, name string) ([]GitRemote, string, error) {
	remotes, selection, err := readGitRemoteSelection(ctx, git)
	if err != nil {
		return nil, "", err
	}
	if selection != nil {
		return remotes, *selection, nil
	}
	remotes, err = readGitConfigRemotes(ctx, git)
	if err != nil {
		return nil, "", err
	}
	upstream, err := gitBranchUpstream(ctx, git, name)
	return remotes, upstream, err
}

// Retained trees are rebuilt from objects rather than copied. Keep captured
// selection separately from HEAD, which can legitimately be detached.
func writeGitRemoteSelection(ctx context.Context, git *gitutil.GitCLI, remotes []GitRemote, upstream string) error {
	if _, err := git.Run(ctx, "config", "dagger.upstreamRemote", upstream); err != nil {
		return err
	}
	data, err := json.Marshal(remotes)
	if err != nil {
		return err
	}
	if _, err := git.Run(ctx, "config", "dagger.remotes", string(data)); err != nil {
		return err
	}
	gitDir, err := git.GitDir(ctx)
	if err != nil {
		return err
	}
	return os.Chtimes(filepath.Join(gitDir, "config"), time.Unix(1, 0), time.Unix(1, 0))
}

// SelectDefaultGitRemote applies the issue's selection order. A local upstream
// (branch.remote = ".") is not a named remote.
func SelectDefaultGitRemote(remotes []GitRemote, upstream string) *GitRemote {
	if len(remotes) == 1 {
		return &remotes[0]
	}
	for i := range remotes {
		if remotes[i].Name == "origin" {
			return &remotes[i]
		}
	}
	for i := range remotes {
		if remotes[i].Name == upstream {
			return &remotes[i]
		}
	}
	return nil
}

var ErrGitHistoryIncomplete = errors.New("git ancestry requires complete history")

// Contains compares commit ancestry, borrowing cached objects through the same
// mount pipeline as logs and merge bases.
func (ref *GitRef) Contains(ctx context.Context, other *GitRef) (bool, error) {
	if ref == nil || other == nil || ref.Ref == nil || other.Ref == nil || ref.Ref.SHA == "" || other.Ref.SHA == "" {
		return false, fmt.Errorf("contains requires two resolved Git commits")
	}
	if err := context.Cause(ctx); err != nil {
		return false, err
	}
	if ref.Ref.SHA == other.Ref.SHA {
		return true, nil
	}
	var contains bool
	refs := []*GitRef{ref, other}
	err := mountRefs(ctx, refs, func(git *gitutil.GitCLI, shas []string) error {
		var err error
		contains, err = gitContains(ctx, git, shas[0], shas[1])
		return err
	})
	if errors.Is(err, ErrGitHistoryIncomplete) {
		return containsWithFullHistory(ctx, refs)
	}
	return contains, err
}

// Fill shallow boundaries through retained capabilities, in a private joined
// repository. The supplied checkout and its remote configuration stay untouched.
func containsWithFullHistory(ctx context.Context, refs []*GitRef) (bool, error) {
	var history []*GitRef
	for _, ref := range refs {
		local, ok := ref.Backend.(*LocalGitRef)
		if !ok || local.repo.Upstream.Self() == nil {
			continue
		}
		upstream := local.repo.Upstream
		err := ref.Backend.mount(ctx, 0, false, func(git *gitutil.GitCLI) error {
			boundaries, err := gitShallowBoundaries(ctx, git)
			if err != nil {
				return err
			}
			for _, sha := range boundaries {
				commit := &gitutil.Ref{SHA: sha}
				backend, err := upstream.Self().Backend.Get(ctx, commit)
				if err != nil {
					return err
				}
				history = append(history, &GitRef{Repo: upstream, Backend: backend, Ref: commit})
			}
			return nil
		})
		if err != nil {
			return false, err
		}
	}
	if len(history) == 0 {
		return false, ErrGitHistoryIncomplete
	}
	git, shas, cleanup, err := refJoin(ctx, refs)
	if err != nil {
		return false, err
	}
	defer cleanup()
	for _, ref := range history {
		boundaries, err := gitShallowBoundaries(ctx, git)
		if err != nil {
			return false, err
		}
		if len(boundaries) == 0 {
			break
		}
		err = ref.Backend.mount(ctx, 0, false, func(source *gitutil.GitCLI) error {
			url, err := source.URL(ctx)
			if err != nil {
				return err
			}
			_, err = git.Run(ctx, "fetch", "--no-tags", "--unshallow", url, ref.Ref.SHA)
			return err
		})
		if err != nil {
			return false, fmt.Errorf("%w: fetch boundary %s: %w", ErrGitHistoryIncomplete, ref.Ref.SHA, err)
		}
	}
	return gitContains(ctx, git, shas[0], shas[1])
}

func gitShallowBoundaries(ctx context.Context, git *gitutil.GitCLI) ([]string, error) {
	path, err := git.Run(ctx, "rev-parse", "--path-format=absolute", "--git-path", "shallow")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(strings.TrimSuffix(string(path), "\n"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Git shallow boundaries: %w", err)
	}
	return strings.Fields(string(data)), nil
}

func gitContains(ctx context.Context, git *gitutil.GitCLI, commit, ancestor string) (bool, error) {
	contains, err := gitIsAncestor(ctx, git, ancestor, commit)
	if err != nil || contains {
		return contains, err
	}
	boundaries, err := gitShallowBoundaries(ctx, git)
	if err != nil {
		return false, err
	}
	for _, boundary := range boundaries {
		// Missing history is older than the boundary. If the candidate is
		// at or above it, that history cannot change a negative answer.
		// This keeps native workspace comparisons within their owned store.
		above, err := gitIsAncestor(ctx, git, boundary, ancestor)
		if err != nil {
			return false, err
		}
		if !above {
			return false, ErrGitHistoryIncomplete
		}
	}
	return false, nil
}
