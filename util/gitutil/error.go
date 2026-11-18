package gitutil

import (
	"context"
	"errors"
	"strings"
)

var (
	ErrGitAuthFailed       = errors.New("git authentication failed")
	ErrGitConnectionFailed = errors.New("cannot connect to git repository")
	ErrGitHostNotFound     = errors.New("cannot resolve git hostname")
	ErrGitNoRepo           = errors.New("not a git repository")
	ErrShallowNotSupported = errors.New("shallow clone not supported")
	// ErrSHAFetchUnsupported reports that the remote serves no object by SHA
	// alone, so only a fetch by named ref can succeed.
	ErrSHAFetchUnsupported = errors.New("sha fetch unsupported by remote")
	// ErrCommitNotFound reports that the remote refused a commit asked for by
	// SHA: it doesn't have it, or, on a protocol v0 server that serves only
	// advertised tips, the commit is not one of them.
	ErrCommitNotFound = errors.New("commit not found on the remote")
)

func translateError(err error, stderr string) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}

	stderr = strings.ToLower(stderr)

	if strings.Contains(stderr, "authentication failed") ||
		strings.Contains(stderr, "authentication required") ||
		strings.Contains(stderr, "permission denied (publickey") ||
		strings.Contains(stderr, "fatal: could not read username") ||
		strings.Contains(stderr, "fatal: could not read password") ||
		isEntireAuthFailure(stderr) {
		return ErrGitAuthFailed
	}
	if strings.Contains(stderr, "could not resolve host") || strings.Contains(stderr, "could not resolve hostname") {
		return ErrGitHostNotFound
	}
	if strings.Contains(stderr, "failed to connect to") ||
		strings.Contains(stderr, "connection refused") ||
		strings.Contains(stderr, "connection timed out") ||
		strings.Contains(stderr, "network is unreachable") {
		return ErrGitConnectionFailed
	}
	if strings.Contains(stderr, "not a git repository") {
		return ErrGitNoRepo
	}
	if strings.Contains(stderr, "does not support shallow") {
		return ErrShallowNotSupported
	}
	// The git client refuses up front when a protocol v0 server advertises no
	// capability to serve unadvertised objects, or a name matches no remote ref.
	// refs:
	// - git fetch-pack: https://github.com/git/git/blob/34b6ce9b30747131b6e781ff718a45328aa887d0/fetch-pack.c#L2250-L2253
	if strings.Contains(stderr, "unadvertised object") ||
		strings.Contains(stderr, "couldn't find remote ref") {
		return ErrSHAFetchUnsupported
	}
	// The server's reply to a wanted object it won't send. Git servers send it
	// for a missing commit whatever their protocol or settings, and a protocol
	// v0 server that serves only advertised tips sends it for any other commit.
	// refs:
	// - git upload-pack: https://github.com/git/git/blob/34b6ce9b30747131b6e781ff718a45328aa887d0/upload-pack.c#L811-L812
	if strings.Contains(stderr, "not our ref") {
		return ErrCommitNotFound
	}

	return err
}

// isEntireAuthFailure matches git-remote-entire's messages for a missing,
// rejected or foreign credential, or a repository the credential cannot read.
// stderr is lowercased.
func isEntireAuthFailure(stderr string) bool {
	return strings.Contains(stderr, "rejected entire_token") ||
		strings.Contains(stderr, "rejected your login") ||
		strings.Contains(stderr, "no auth context for") ||
		strings.Contains(stderr, "is not a trusted login server for cluster") ||
		strings.Contains(stderr, "you do not have access to")
}
