package gitutil

import (
	"context"
	"errors"
	"strings"
)

var (
	ErrGitAuthFailed       = errors.New("git authentication failed")
	ErrGitNoRepo           = errors.New("not a git repository")
	ErrShallowNotSupported = errors.New("shallow clone not supported")
	// ErrSHAFetchUnsupported is a normalized signal that retry-by-named-ref may succeed.
	ErrSHAFetchUnsupported = errors.New("sha fetch unsupported by remote")
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
		strings.Contains(stderr, "fatal: could not read username") ||
		strings.Contains(stderr, "fatal: could not read password") {
		return ErrGitAuthFailed
	}
	if strings.Contains(stderr, "not a git repository") {
		return ErrGitNoRepo
	}
	if strings.Contains(stderr, "does not support shallow") {
		return ErrShallowNotSupported
	}
	// Canonical SHA-fetch rejection strings from Git/go-git; map them to one retry signal.
	// Carries forward legacy gitdns matches ("not our ref", "unadvertised object") in a single classifier.
	// refs:
	// - git upload-pack: https://github.com/git/git/blob/34b6ce9b30747131b6e781ff718a45328aa887d0/upload-pack.c#L811-L812
	// - git fetch-pack: https://github.com/git/git/blob/34b6ce9b30747131b6e781ff718a45328aa887d0/fetch-pack.c#L2250-L2253
	if strings.Contains(stderr, "unadvertised object") ||
		strings.Contains(stderr, "not our ref") ||
		strings.Contains(stderr, "couldn't find remote ref") {
		return ErrSHAFetchUnsupported
	}

	return err
}
