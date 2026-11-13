package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dagger/dagger/util/gitutil"
	telemetry "github.com/dagger/otel-go"
)

// GitRemoteHeadTimeout bounds GitRemoteHead, so a remote that is slow or
// silent never holds up a push for long.
const GitRemoteHeadTimeout = 5 * time.Second

// ErrGitRemoteUnreachable reports that GitRemoteHead got no answer from the
// remote, or one it could not make sense of.
var ErrGitRemoteUnreachable = errors.New("git remote unreachable")

// GitRemoteHead returns the branch remote's HEAD points to, e.g.
// "refs/heads/main", as an anonymous `git ls-remote --symref` reports it, or ""
// when the remote answered without naming one: it requires authentication, is
// empty, or does not advertise symbolic refs. Any other failure (no such host,
// no connection, a timeout, an error it cannot classify) wraps
// ErrGitRemoteUnreachable.
//
// It never authenticates: no credential helper, SSH agent, token or service
// binding is involved, so it is safe to run on a URL nobody approved yet. Its
// answer is only as trustworthy as the remote: whoever serves the URL decides
// what HEAD is.
//
// Anonymous SSH never authenticates, so an SSH remote is asked over HTTPS at
// the same host and path (GitURL.HTMLURL), as forges serve both. Entire
// remotes cannot be read anonymously and are never asked.
func GitRemoteHead(ctx context.Context, remote string) (_ string, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "git remote default branch", telemetry.Internal())
	defer telemetry.EndWithCause(span, &rerr)
	url, err := gitRemoteHeadURL(remote)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, GitRemoteHeadTimeout)
	defer cancel()
	git, cleanup, err := (&RemoteGitRepository{URL: url}).setup(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrGitRemoteUnreachable, err)
	}
	defer cleanup()
	out, err := git.Run(ctx, "ls-remote", "--symref", url.Remote(), "HEAD")
	if errors.Is(err, gitutil.ErrGitAuthFailed) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrGitRemoteUnreachable, err)
	}
	return parseGitRemoteHead(string(out)), nil
}

// gitRemoteHeadURL is the URL GitRemoteHead asks for remote's HEAD.
func gitRemoteHeadURL(remote string) (*gitutil.GitURL, error) {
	url, err := gitutil.ParseURL(remote)
	if err != nil || url.Host == "" {
		return nil, fmt.Errorf("%w: invalid URL", ErrGitRemoteUnreachable)
	}
	switch url.Scheme {
	case gitutil.HTTPProtocol, gitutil.HTTPSProtocol, gitutil.GitProtocol:
		if url.User != nil {
			// Never send userinfo, even a bare username.
			copy := *url
			copy.User = nil
			url = &copy
		}
		return url, nil
	case gitutil.SSHProtocol:
		return gitutil.ParseURL(url.HTMLURL())
	default:
		return nil, fmt.Errorf("%w: %s remotes cannot be read anonymously", ErrGitRemoteUnreachable, url.Scheme)
	}
}

// parseGitRemoteHead returns the target of HEAD in `git ls-remote --symref`
// output, if it is a branch.
func parseGitRemoteHead(out string) string {
	for line := range strings.SplitSeq(out, "\n") {
		target, name, ok := strings.Cut(line, "\t")
		if !ok || name != "HEAD" {
			continue
		}
		if branch, ok := strings.CutPrefix(target, "ref: "); ok && strings.HasPrefix(branch, "refs/heads/") {
			return branch
		}
	}
	return ""
}
