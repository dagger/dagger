package schema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/dagger/dagger/util/hashutil"
)

// resolveRepository is the lazy -> resolved boundary for git().
//
// It performs auth/protocol resolution and snapshots remote state, then rewrites
// object identity to semantic inputs instead of per-client call envelopes.
func (s *gitSchema) resolveRepository(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRepository],
	_ struct{},
) (dagql.ObjectResult[*core.GitRepository], error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.GitRepository]{}, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	repo := parent.Self()
	remote, isRemote := repo.Backend.(*core.RemoteGitRepository)
	if !isRemote {
		return s.resolveLocalRepository(ctx, srv, parent, repo)
	}

	return s.resolveRemoteRepository(ctx, srv, parent, repo, remote)
}

func (s *gitSchema) resolveLocalRepository(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.GitRepository],
	repo *core.GitRepository,
) (dagql.ObjectResult[*core.GitRepository], error) {
	var zero dagql.ObjectResult[*core.GitRepository]

	resolved := repo.Clone()
	lsRemote, err := resolved.Backend.Remote(ctx)
	if err != nil {
		return zero, err
	}
	resolved.Remote = lsRemote
	resolved.Resolved = true

	result, err := dagql.NewObjectResultForCurrentID(ctx, srv, resolved)
	if err != nil {
		return zero, err
	}

	// Strip CachePerClient client identity for stable local-repo keys across sessions.
	if resolved.Remote != nil {
		dgstInputs := localRepoResolveDigestInputs(parent, resolved)
		result = result.WithObjectDigest(hashutil.HashStrings(dgstInputs...))
	}

	return result, nil
}

func (s *gitSchema) resolveRemoteRepository(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.GitRepository],
	repo *core.GitRepository,
	remote *core.RemoteGitRepository,
) (dagql.ObjectResult[*core.GitRepository], error) {
	var zero dagql.ObjectResult[*core.GitRepository]

	// 1) Normalize remote inputs.
	remote = remote.Clone()
	if remote.URL != nil && remote.URL.Scheme == gitutil.SSHProtocol && remote.URL.User == nil {
		remote.URL.User = url.User("git")
	}

	if err := s.scopeProvidedSSHAuthSocket(ctx, srv, remote); err != nil {
		return zero, err
	}

	// 2) URL was ambiguous at parse time; choose protocol from auth hints, then probe.
	if remote.URL == nil {
		return s.resolveAmbiguousURL(ctx, srv, parent, remote)
	}

	// 3) If auth wasn't provided explicitly, try caller-scoped auth.
	if !remote.SSHAuthSocket.Valid && !remote.AuthToken.Valid && !remote.AuthHeader.Valid {
		resolved, ok, err := s.resolveWithClientAuth(ctx, srv, parent, remote)
		if err != nil {
			return zero, err
		}
		if ok {
			return resolved, nil
		}
	}

	// 4) Resolve ls-remote and rewrite identity to semantic repo inputs.
	resolved := repo.Clone()
	resolved.Backend = remote

	lsRemote, err := resolved.Backend.Remote(ctx)
	if err != nil {
		return zero, err
	}
	resolved.Remote = lsRemote
	resolved.Resolved = true

	result, err := dagql.NewObjectResultForCurrentID(ctx, srv, resolved)
	if err != nil {
		return zero, err
	}

	// Keep auth in repo identity; keep ls-remote in identity to invalidate on pushes.
	if resolved.Remote != nil {
		dgstInputs := remoteRepoResolveDigestInputs(remote, resolved)
		result = result.WithObjectDigest(hashutil.HashStrings(dgstInputs...))
	}

	return result, nil
}

func (s *gitSchema) scopeProvidedSSHAuthSocket(
	ctx context.Context,
	srv *dagql.Server,
	remote *core.RemoteGitRepository,
) error {
	if !remote.SSHAuthSocket.Valid {
		return nil
	}

	var scopedSock dagql.ObjectResult[*core.Socket]
	if err := srv.Select(ctx, srv.Root(), &scopedSock,
		dagql.Selector{Field: "host"},
		dagql.Selector{
			Field: "_sshAuthSocket",
			Args: []dagql.NamedInput{
				{
					Name:  "source",
					Value: dagql.Opt(dagql.NewID[*core.Socket](remote.SSHAuthSocket.Value.ID())),
				},
			},
		},
	); err != nil {
		return fmt.Errorf("%w: failed to scope SSH auth socket: %w", gitutil.ErrGitAuthFailed, err)
	}

	remote.SSHAuthSocket = dagql.Opt(dagql.NewID[*core.Socket](scopedSock.ID()))
	return nil
}

func (s *gitSchema) resolveWithClientAuth(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.GitRepository],
	remote *core.RemoteGitRepository,
) (dagql.ObjectResult[*core.GitRepository], bool, error) {
	var zero dagql.ObjectResult[*core.GitRepository]

	switch remote.URL.Scheme {
	case gitutil.SSHProtocol:
		var scopedSock dagql.ObjectResult[*core.Socket]
		if err := srv.Select(ctx, srv.Root(), &scopedSock,
			dagql.Selector{Field: "host"},
			dagql.Selector{Field: "_sshAuthSocket"},
		); err != nil {
			if errors.Is(err, errSSHAuthSocketNotSet) {
				return zero, false, fmt.Errorf("%w: SSH URLs are not supported without an SSH socket", gitutil.ErrGitAuthFailed)
			}
			return zero, false, fmt.Errorf("%w: failed to get SSH socket: %w", gitutil.ErrGitAuthFailed, err)
		}

		authArgs := []dagql.NamedInput{
			{Name: "sshAuthSocket", Value: dagql.Opt(dagql.NewID[*core.Socket](scopedSock.ID()))},
		}
		resolved, err := s.resolveViaGitCall(ctx, srv, parent, remote, remote.URL.String(), authArgs)
		return resolved, true, err

	case gitutil.HTTPProtocol, gitutil.HTTPSProtocol:
		token, username, ok := s.lookupParentClientHTTPAuth(ctx, srv, remote.URL)
		if !ok {
			return zero, false, nil
		}

		authArgs := []dagql.NamedInput{
			{Name: "httpAuthToken", Value: dagql.Opt(token)},
		}
		if username != "" {
			authArgs = append(authArgs, dagql.NamedInput{
				Name: "httpAuthUsername", Value: dagql.NewString(username),
			})
		}

		resolved, err := s.resolveViaGitCall(ctx, srv, parent, remote, remote.URL.String(), authArgs)
		return resolved, true, err
	default:
		return zero, false, nil
	}
}

// Keep pinned head in digest so pinned refs don't alias moving branch snapshots.
func appendPinnedHeadDigestInputs(inputs []string, pinnedHead *gitutil.Ref) []string {
	if pinnedHead == nil {
		return inputs
	}
	if pinnedHead.Name != "" {
		inputs = append(inputs, "pinnedRef", pinnedHead.Name)
	}
	if pinnedHead.SHA != "" {
		inputs = append(inputs, "pinnedCommit", pinnedHead.SHA)
	}
	return inputs
}

func localRepoResolveDigestInputs(parent dagql.ObjectResult[*core.GitRepository], resolved *core.GitRepository) []string {
	inputs := []string{
		"local",
		parent.ID().Digest().String(),
		strconv.FormatBool(resolved.DiscardGitDir),
		"remote",
		resolved.Remote.Digest().String(),
	}
	return appendPinnedHeadDigestInputs(inputs, resolved.PinnedHead)
}

func remoteRepoResolveDigestInputs(remote *core.RemoteGitRepository, resolved *core.GitRepository) []string {
	inputs := []string{
		remote.URL.String(),
		strconv.FormatBool(resolved.DiscardGitDir),
	}
	inputs = appendRemoteAuthDigestInputs(inputs, remote)
	inputs = append(inputs, "remote", resolved.Remote.Digest().String())
	return appendPinnedHeadDigestInputs(inputs, resolved.PinnedHead)
}

// Auth material is part of repo identity so different auth contexts cannot collide.
func appendRemoteAuthDigestInputs(inputs []string, remote *core.RemoteGitRepository) []string {
	if remote.SSHAuthSocket.Valid {
		if dgst := core.SocketIDDigest(remote.SSHAuthSocket.Value.ID()); dgst != "" {
			inputs = append(inputs, "sshAuthSock", dgst.String())
		}
	}
	if remote.AuthToken.Valid {
		if dgst := core.SecretIDDigest(remote.AuthToken.Value.ID()); dgst != "" {
			inputs = append(inputs, "authToken", dgst.String())
		}
	}
	if remote.AuthHeader.Valid {
		if dgst := core.SecretIDDigest(remote.AuthHeader.Value.ID()); dgst != "" {
			inputs = append(inputs, "authHeader", dgst.String())
		}
	}
	return inputs
}

// resolveAmbiguousURL chooses protocol from auth hints, then probes https/ssh.
func (s *gitSchema) resolveAmbiguousURL(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.GitRepository],
	remote *core.RemoteGitRepository,
) (dagql.ObjectResult[*core.GitRepository], error) {
	var zero dagql.ObjectResult[*core.GitRepository]
	rawURL := parent.ID().Arg("url").Value().ToInput().(string)

	if remote.SSHAuthSocket.Valid {
		return s.resolveViaGitCall(ctx, srv, parent, remote, "ssh://git@"+rawURL, nil)
	}
	if remote.AuthToken.Valid || remote.AuthHeader.Valid {
		return s.resolveViaGitCall(ctx, srv, parent, remote, "https://"+rawURL, nil)
	}

	candidates := []string{"https://" + rawURL, "ssh://git@" + rawURL}

	for _, candidateURL := range candidates {
		result, err := s.resolveViaGitCall(ctx, srv, parent, remote, candidateURL, nil)
		if err == nil {
			return result, nil
		}
		if errors.Is(err, gitutil.ErrGitAuthFailed) {
			continue
		}
		return zero, err
	}

	return zero, fmt.Errorf("failed to resolve git URL: tried https and ssh")
}

// resolveViaGitCall re-enters git(...).__resolve with canonicalized args.
func (s *gitSchema) resolveViaGitCall(
	ctx context.Context,
	srv *dagql.Server,
	parent dagql.ObjectResult[*core.GitRepository],
	remote *core.RemoteGitRepository,
	url string,
	authArgs []dagql.NamedInput,
) (dagql.ObjectResult[*core.GitRepository], error) {
	args := []dagql.NamedInput{
		{Name: "url", Value: dagql.NewString(url)},
	}
	args = append(args, authArgs...)

	// If resolve injected auth, don't also append original auth fields.
	if len(authArgs) == 0 {
		args = appendInheritedGitAuthArgs(args, remote)
	}

	// Preserve non-auth git() args when replaying through a canonical URL/auth form.
	args = appendInheritedGitCallArgs(args, parent, remote)

	var result dagql.ObjectResult[*core.GitRepository]
	err := srv.Select(ctx, srv.Root(), &result,
		dagql.Selector{
			Field: "git",
			Args:  args,
		},
		dagql.Selector{Field: "__resolve"},
	)
	return result, err
}

func appendInheritedGitAuthArgs(args []dagql.NamedInput, remote *core.RemoteGitRepository) []dagql.NamedInput {
	if token := remote.AuthToken; token.Valid {
		args = append(args, dagql.NamedInput{Name: "httpAuthToken", Value: dagql.Opt(dagql.NewID[*core.Secret](token.Value.ID()))})
	}
	if header := remote.AuthHeader; header.Valid {
		args = append(args, dagql.NamedInput{Name: "httpAuthHeader", Value: dagql.Opt(dagql.NewID[*core.Secret](header.Value.ID()))})
	}
	if remote.AuthUsername != "" {
		args = append(args, dagql.NamedInput{Name: "httpAuthUsername", Value: dagql.NewString(remote.AuthUsername)})
	}
	if sock := remote.SSHAuthSocket; sock.Valid {
		args = append(args, dagql.NamedInput{Name: "sshAuthSocket", Value: dagql.Opt(dagql.NewID[*core.Socket](sock.Value.ID()))})
	}
	return args
}

func appendInheritedGitCallArgs(
	args []dagql.NamedInput,
	parent dagql.ObjectResult[*core.GitRepository],
	remote *core.RemoteGitRepository,
) []dagql.NamedInput {
	appendIfTrue := func(name string) {
		arg := parent.ID().Arg(name)
		if arg == nil {
			return
		}
		v, ok := arg.Value().ToInput().(bool)
		if ok && v {
			args = append(args, dagql.NamedInput{Name: name, Value: dagql.Opt(dagql.Boolean(true))})
		}
	}
	appendIfNonEmpty := func(name string) {
		arg := parent.ID().Arg(name)
		if arg == nil {
			return
		}
		v, ok := arg.Value().ToInput().(string)
		if ok && v != "" {
			args = append(args, dagql.NamedInput{Name: name, Value: dagql.NewString(v)})
		}
	}

	appendIfTrue("keepGitDir")
	appendIfNonEmpty("sshKnownHosts")
	appendIfNonEmpty("commit")
	appendIfNonEmpty("ref")

	if remote.Services != nil {
		if svcArg := parent.ID().Arg("experimentalServiceHost"); svcArg != nil {
			if litID, ok := svcArg.Value().(*call.LiteralID); ok {
				args = append(args, dagql.NamedInput{Name: "experimentalServiceHost", Value: dagql.Opt(dagql.NewID[*core.Service](litID.Value()))})
			}
		}
	}
	return args
}

// lookupParentClientHTTPAuth returns HTTP credentials from the parent client context.
func (s *gitSchema) lookupParentClientHTTPAuth(
	ctx context.Context,
	srv *dagql.Server,
	parsedURL *gitutil.GitURL,
) (core.SecretID, string, bool) {
	var zero core.SecretID

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return zero, "", false
	}

	parentMD, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return zero, "", false
	}

	clientMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return zero, "", false
	}

	if clientMD.ClientID != parentMD.ClientID {
		return zero, "", false
	}

	authCtx := engine.ContextWithClientMetadata(ctx, parentMD)
	bk, err := query.Buildkit(authCtx)
	if err != nil {
		return zero, "", false
	}

	creds, err := bk.GetCredential(authCtx, parsedURL.Scheme, parsedURL.Host, parsedURL.Path)
	if err != nil {
		return zero, "", false
	}
	if creds.Password == "" {
		return zero, "", false
	}

	sum := sha256.Sum256([]byte(creds.Password))
	secretName := hex.EncodeToString(sum[:])

	var token dagql.ObjectResult[*core.Secret]
	if err := srv.Select(authCtx, srv.Root(), &token,
		dagql.Selector{
			Field: "setSecret",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.NewString(secretName)},
				{Name: "plaintext", Value: dagql.NewString(creds.Password)},
			},
		},
	); err != nil {
		return zero, "", false
	}

	return dagql.NewID[*core.Secret](token.ID()), creds.Username, true
}

// resolveRef canonicalizes a ref against resolved repo state, then keys identity by
// canonical target (name + commit) plus repo transport semantics.
func (s *gitSchema) resolveRef(
	ctx context.Context,
	parent dagql.ObjectResult[*core.GitRef],
	_ struct{},
) (dagql.ObjectResult[*core.GitRef], error) {
	var zero dagql.ObjectResult[*core.GitRef]

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return zero, fmt.Errorf("failed to get current dagql server: %w", err)
	}

	// Always resolve refs against the canonical repository identity first.
	var resolvedRepo dagql.ObjectResult[*core.GitRepository]
	if err := srv.Select(ctx, parent.Self().Repo, &resolvedRepo,
		dagql.Selector{Field: "__resolve"},
	); err != nil {
		return zero, fmt.Errorf("failed to resolve repo: %w", err)
	}

	ref := parent.Self().Clone()
	ref.Repo = resolvedRepo

	repo := resolvedRepo.Self()
	// Normalize symbolic refs to the concrete remote ref/SHA when available.
	if ref.Ref.Name != "" && repo.Remote != nil {
		resolvedRefInfo, err := repo.Remote.Lookup(ref.Ref.Name)
		if err != nil {
			return zero, err
		}
		if ref.Ref.SHA == "" {
			ref.Ref.SHA = resolvedRefInfo.SHA
		}
		if resolvedRefInfo.Name != "" {
			ref.Ref.Name = resolvedRefInfo.Name
		}
	}

	if ref.Backend == nil {
		refBackend, err := repo.Backend.Get(ctx, ref.Ref)
		if err != nil {
			return zero, err
		}
		ref.Backend = refBackend
	}
	ref.Resolved = true

	// Keep caller intent (requested name) while pinning to the resolved commit.
	refArgs := []*call.Argument{
		call.NewArgument("name", call.NewLiteralString(parent.Self().Ref.Name), false),
	}
	if ref.Ref != nil && ref.Ref.SHA != "" {
		refArgs = append(refArgs, call.NewArgument("commit", call.NewLiteralString(ref.Ref.SHA), false))
	}

	resolvedRefID := resolvedRepo.ID().Receiver().
		Append(ref.Type(), "ref", call.WithArgs(refArgs...)).
		Append(ref.Type(), "__resolve")

	result, err := dagql.NewObjectResultForID(ref, srv, resolvedRefID)
	if err != nil {
		return zero, err
	}

	// Ref identity ignores auth but keeps protocol and commit.
	if ref.Ref != nil && ref.Ref.SHA != "" {
		dgstInputs := refResolveDigestInputs(resolvedRepo.Self(), ref.Ref)
		result = result.WithObjectDigest(hashutil.HashStrings(dgstInputs...))
	}

	return result, nil
}

func refResolveDigestInputs(resolvedRepo *core.GitRepository, ref *gitutil.Ref) []string {
	inputs := []string{
		ref.Name, // canonical: refs/heads/main, refs/tags/v1.0
		ref.SHA,
		strconv.FormatBool(resolvedRepo.DiscardGitDir),
	}
	if remote, isRemote := resolvedRepo.Backend.(*core.RemoteGitRepository); isRemote && remote.URL != nil {
		inputs = append([]string{remote.URL.String()}, inputs...)
	}
	return inputs
}
