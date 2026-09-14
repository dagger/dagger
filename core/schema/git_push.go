package schema

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
)

type gitPushArgs struct {
	To                dagql.Optional[dagql.ID[*core.GitRepository]]
	Remote            string `default:""`
	Branch            string `default:""`
	ExpectedRemoteSHA string `name:"expectedRemoteSHA" default:""`
}

func (s *gitSchema) push(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args gitPushArgs) (dagql.ObjectResult[*core.GitPushResult], error) {
	var inst dagql.ObjectResult[*core.GitPushResult]
	opts := core.GitPushOpts{Branch: args.Branch, ExpectedRemoteSHA: args.ExpectedRemoteSHA}
	if _, err := opts.Ref(parent.Self().Ref); err != nil {
		return inst, err
	}
	if args.To.Valid && args.Remote != "" {
		return inst, fmt.Errorf("pass either to or remote, not both")
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}
	repo := parent.Self().Repo
	var destinationURL string
	if args.To.Valid {
		repo, err = args.To.Value.Load(ctx, srv)
		if err != nil {
			return inst, err
		}
	} else if args.Remote != "" && args.Remote != "origin" {
		// A named remote must be registered; origin is the implicit default
		// below, resolvable even when it was never registered explicitly.
		named := repo.Self().RemoteConfig(args.Remote)
		switch {
		case named == nil:
			return inst, fmt.Errorf("no remote named %q is registered on the source; register it with withRemote or pass an explicit destination with to", args.Remote)
		case len(named.PushURLs) > 1:
			return inst, fmt.Errorf("remote %q has multiple push URLs; pass an explicit destination repository with to", args.Remote)
		case len(named.PushURLs) == 1:
			destinationURL = named.PushURLs[0]
		case named.URL != "":
			destinationURL = named.URL
		default:
			return inst, fmt.Errorf("remote %q has no URL; pass an explicit destination repository with to", args.Remote)
		}
	} else if origin := repo.Self().RemoteConfig("origin"); origin != nil && len(origin.PushURLs) > 1 {
		return inst, fmt.Errorf("origin has multiple push URLs; pass an explicit destination repository with to")
	} else if origin != nil && len(origin.PushURLs) == 1 {
		destinationURL = origin.PushURLs[0]
	} else if _, remote := repo.Self().Backend.(*core.RemoteGitRepository); !remote {
		switch {
		case origin != nil && origin.URL != "":
			destinationURL = origin.URL
		case repo.Self().URL.Valid && repo.Self().URL.Value.String() != "":
			destinationURL = repo.Self().URL.Value.String()
		default:
			return inst, fmt.Errorf("push requires an explicit destination repository: source has no remote URL")
		}
	}
	remote, ok := repo.Self().Backend.(*core.RemoteGitRepository)
	if destinationURL != "" {
		// Do not select Query.git here: it can probe the remote and acquire
		// credentials before the push approval. This ephemeral destination is
		// never returned to the module or entered into a portable recipe.
		parsed, err := gitutil.ParseURL(destinationURL)
		if err != nil {
			return inst, fmt.Errorf("invalid push destination URL")
		}
		if parsed.Scheme == gitutil.SSHProtocol && parsed.User == nil {
			parsed.User = url.User("git")
		}
		remote = &core.RemoteGitRepository{URL: parsed}
	} else if !ok {
		return inst, fmt.Errorf("push destination must be a remote Git repository, not a Directory; use Workspace.export for a local checkout")
	}
	result, err := parent.Self().Push(ctx, remote, opts)
	if err != nil {
		return inst, err
	}
	// Return a pure receipt composition, never the effectful push call. A
	// receipt ID can be loaded in another session without touching the remote.
	err = srv.Select(ctx, srv.Root(), &inst, dagql.Selector{Field: "__gitPushResult", Args: []dagql.NamedInput{
		{Name: "ref", Value: dagql.NewString(result.Ref)}, {Name: "previousSHA", Value: dagql.NewString(result.PreviousSHA)},
		{Name: "sha", Value: dagql.NewString(result.SHA)}, {Name: "disposition", Value: result.Disposition},
	}})
	return inst, err
}

type withRemoteArgs struct {
	Name     string
	URL      string   `name:"url"`
	PushURLs []string `name:"pushUrls" default:"[]"`
}

func (s *gitSchema) withRemote(_ context.Context, parent *core.GitRepository, args withRemoteArgs) (*core.GitRepository, error) {
	if err := validateGitRemoteName(args.Name); err != nil {
		return nil, err
	}
	if err := validateGitRemoteURL(args.URL); err != nil {
		return nil, err
	}
	for _, pushURL := range args.PushURLs {
		if err := validateGitRemoteURL(pushURL); err != nil {
			return nil, err
		}
	}
	repo := parent.CloneWithBackend(parent.Backend)
	repo.Remotes = core.WithGitRemote(repo.Remotes, core.GitRemote{
		Name:     args.Name,
		URL:      args.URL,
		PushURLs: args.PushURLs,
	})
	return repo, nil
}

func validateGitRemoteName(name string) error {
	if name == "" {
		return fmt.Errorf("remote name must be nonempty")
	}
	if strings.HasPrefix(name, "-") || strings.Contains(name, "..") ||
		strings.ContainsAny(name, "/\\ \t\r\n\x00") {
		return fmt.Errorf("invalid remote name %q", name)
	}
	return nil
}

// validateGitRemoteURL accepts the spellings git itself does — scheme URLs,
// SCP-style remotes, and local paths — but never a URL carrying a password:
// remotes are routing metadata, and a recorded secret would outlive the
// recipe that embedded it.
func validateGitRemoteURL(remoteURL string) error {
	if remoteURL == "" {
		return fmt.Errorf("remote URL must be nonempty")
	}
	if strings.HasPrefix(remoteURL, "-") || strings.ContainsAny(remoteURL, "\r\n\x00") {
		return fmt.Errorf("invalid remote URL")
	}
	if strings.Contains(remoteURL, "://") {
		parsed, err := url.Parse(remoteURL)
		if err != nil {
			return fmt.Errorf("invalid remote URL")
		}
		if parsed.User != nil {
			if _, hasPassword := parsed.User.Password(); hasPassword {
				return fmt.Errorf("remote URL must not embed a password; configure credentials on the caller instead")
			}
		}
	}
	return nil
}

// gitRemoteDigestInputs flattens registered remotes for content digesting,
// with explicit counts so entry boundaries never collide.
func gitRemoteDigestInputs(remotes []core.GitRemote) []string {
	inputs := make([]string, 0, len(remotes)*4)
	for _, remote := range remotes {
		inputs = append(inputs, remote.Name, remote.URL, strconv.Itoa(len(remote.PushURLs)))
		inputs = append(inputs, remote.PushURLs...)
	}
	return inputs
}

func (s *gitSchema) pushResult(_ context.Context, _ *core.Query, args struct {
	Ref         string
	PreviousSHA string `name:"previousSHA"`
	SHA         string
	Disposition core.GitPushDisposition
}) (*core.GitPushResult, error) {
	return &core.GitPushResult{Ref: args.Ref, PreviousSHA: args.PreviousSHA, SHA: args.SHA, Disposition: args.Disposition}, nil
}
