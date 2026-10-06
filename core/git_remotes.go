package core

import (
	"context"
	"encoding/json"
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
		remotes = []GitRemote{{Name: "origin", URL: backend.URL.Remote()}}
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
