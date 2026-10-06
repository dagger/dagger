package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
)

type gitRemoteArgs struct{ Name string }

func (s *gitSchema) remotes(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], _ struct{}) (dagql.ObjectResultArray[*core.GitRemoteHandle], error) {
	remotes, _, err := parent.Self().ConfiguredRemotes(ctx)
	if err != nil {
		return nil, err
	}
	srv := dagql.CurrentDagqlServer(ctx)
	results := make(dagql.ObjectResultArray[*core.GitRemoteHandle], 0, len(remotes))
	for _, remote := range remotes {
		var result dagql.ObjectResult[*core.GitRemoteHandle]
		if err := srv.Select(ctx, parent, &result, dagql.Selector{Field: "remote", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(remote.Name)}}}); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *gitSchema) remote(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], args gitRemoteArgs) (dagql.ObjectResult[*core.GitRemoteHandle], error) {
	remotes, _, err := parent.Self().ConfiguredRemotes(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.GitRemoteHandle]{}, err
	}
	for _, remote := range remotes {
		if remote.Name == args.Name {
			return dagql.NewObjectResultForCurrentCall(ctx, dagql.CurrentDagqlServer(ctx), &core.GitRemoteHandle{Name: dagql.String(remote.Name), URL: remote.URL, Source: parent})
		}
	}
	return dagql.ObjectResult[*core.GitRemoteHandle]{}, fmt.Errorf("no remote named %q", args.Name)
}

func (s *gitSchema) defaultRemote(ctx context.Context, parent dagql.ObjectResult[*core.GitRepository], _ struct{}) (dagql.Nullable[dagql.ObjectResult[*core.GitRemoteHandle]], error) {
	none := dagql.Null[dagql.ObjectResult[*core.GitRemoteHandle]]()
	remotes, upstream, err := parent.Self().ConfiguredRemotes(ctx)
	if err != nil {
		return none, err
	}
	remote := core.SelectDefaultGitRemote(remotes, upstream)
	if remote == nil {
		return none, nil
	}
	var result dagql.ObjectResult[*core.GitRemoteHandle]
	if err := dagql.CurrentDagqlServer(ctx).Select(ctx, parent, &result, dagql.Selector{Field: "remote", Args: []dagql.NamedInput{{Name: "name", Value: dagql.String(remote.Name)}}}); err != nil {
		return none, err
	}
	return dagql.NonNull(result), nil
}

func (s *gitSchema) remoteRepository(ctx context.Context, parent dagql.ObjectResult[*core.GitRemoteHandle], _ struct{}) (dagql.ObjectResult[*core.GitRepository], error) {
	var result dagql.ObjectResult[*core.GitRepository]
	remote := parent.Self()
	if remote.URL == "" {
		return result, fmt.Errorf("remote %q has no fetch URL", remote.Name)
	}
	srv := dagql.CurrentDagqlServer(ctx)
	url := remote.URL
	parsed, err := gitutil.ParseURL(remote.URL)
	if err != nil && !errors.Is(err, gitutil.ErrUnknownProtocol) {
		return result, fmt.Errorf("remote %q: %w", remote.Name, err)
	}
	if err == nil {
		url = parsed.Remote()
	}
	if source := core.GitUpstream(remote.Source); parsed != nil && source.Self() != nil {
		backend := source.Self().Backend.(*core.RemoteGitRepository)
		if backend.URL.Remote() == parsed.Remote() {
			// NewGitRepository clears any HEAD pin while keeping exactly the
			// source's mirror, authentication and service dependencies.
			remoteBackend := *backend
			remoteBackend.URL = parsed
			remoteBackend.Services = slices.Clone(backend.Services)
			repo, err := core.NewGitRepository(ctx, &remoteBackend)
			if err != nil {
				return result, err
			}
			return dagql.NewObjectResultForCurrentCall(ctx, srv, repo)
		}
	}
	err = srv.Select(ctx, srv.Root(), &result, dagql.Selector{Field: "git", Args: []dagql.NamedInput{{Name: "url", Value: dagql.String(url)}}})
	return result, err
}

func (s *gitSchema) contains(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args mergeBaseArgs) (dagql.Boolean, error) {
	other, err := args.Other.Load(ctx, dagql.CurrentDagqlServer(ctx))
	if err != nil {
		return false, err
	}
	contains, err := parent.Self().Contains(ctx, other.Self())
	return dagql.Boolean(contains), err
}

type gitRemoteSelectionArgs struct {
	Remotes        string
	UpstreamRemote string
}

func (s *gitSchema) withRemoteSelection(ctx context.Context, parent *core.GitRepository, args gitRemoteSelectionArgs) (*core.GitRepository, error) {
	var remotes []core.GitRemote
	if err := json.Unmarshal([]byte(args.Remotes), &remotes); err != nil {
		return nil, fmt.Errorf("decode captured remotes: %w", err)
	}
	for _, remote := range remotes {
		// Captured names already come from Git. Check them using Git's ref
		// rules, which allow names such as team/trunk.
		if remote.Name == "" {
			return nil, fmt.Errorf("remote name must be nonempty")
		}
		if _, err := gitutil.NewGitCLI().Run(ctx, "check-ref-format", "refs/remotes/"+remote.Name+"/HEAD"); err != nil {
			return nil, fmt.Errorf("invalid captured remote name %q: %w", remote.Name, err)
		}
		if remote.URL != "" {
			if err := validateGitRemoteURL(remote.URL); err != nil {
				return nil, err
			}
		}
		if remote.PushURL != "" {
			if err := validateGitRemoteURL(remote.PushURL); err != nil {
				return nil, err
			}
		}
	}
	repo := parent.CloneWithBackend(parent.Backend)
	repo.Remotes = core.MergeGitRemotes(nil, remotes)
	repo.UpstreamRemote = &args.UpstreamRemote
	return repo, nil
}
