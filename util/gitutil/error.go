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
		strings.Contains(stderr, "fatal: could not read password") ||
		strings.Contains(stderr, "permission denied (publickey)") ||
		strings.Contains(stderr, "could not read from remote repository") {
		return ErrGitAuthFailed
	}
	if strings.Contains(stderr, "not a git repository") {
		return ErrGitNoRepo
	}
	if strings.Contains(stderr, "does not support shallow") {
		return ErrShallowNotSupported
	}

	return err
}
