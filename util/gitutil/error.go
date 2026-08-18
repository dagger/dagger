package gitutil

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrGitAuthFailed       = errors.New("git authentication failed")
	ErrGitNoRepo           = errors.New("not a git repository")
	ErrShallowNotSupported = errors.New("shallow clone not supported")
	// ErrSHAFetchUnsupported is a normalized signal that retry-by-named-ref may succeed.
	ErrSHAFetchUnsupported = errors.New("sha fetch unsupported by remote")
	// ErrTransient marks a failure that is worth retrying as-is: a network
	// hiccup talking to the remote, or a local repository that another process
	// mutated underneath the command.
	ErrTransient = errors.New("transient git failure")
)

// transientStderr are stderr fragments that mean "this git command may well
// succeed if it is simply run again". They cover two families:
//
//   - the remote end dropping or refusing the connection mid-transfer
//   - a concurrent writer changing the repository underneath us, which git
//     detects via the stat data it recorded when it read the file
var transientStderr = []string{
	// concurrent local writer (e.g. detached `gc --auto` from an earlier fetch)
	"shallow file has changed since we read it",
	"shallow file was changed during fetch",
	// another process holds the ref lock; the deterministic failures that share
	// the "cannot lock ref" prefix (ref/directory conflicts) are not matched
	".lock': file exists",
	// remote/network
	"the remote end hung up unexpectedly",
	"early eof",
	"rpc failed",
	"connection reset by peer",
	"connection closed by",
	"connection timed out",
	"operation timed out",
	"could not resolve host",
	"failed to connect to",
	"unexpected disconnect",
	"remote error: internal server error",
	// git reports HTTP status as "The requested URL returned error: 502"
	"returned error: 5",
	"returned error: 429",
}

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

	for _, fragment := range transientStderr {
		if strings.Contains(stderr, fragment) {
			return fmt.Errorf("%w: %w", ErrTransient, err)
		}
	}

	return err
}

// credentialedURL matches the userinfo of a URL appearing anywhere in a line,
// e.g. "fatal: Authentication failed for 'https://user:token@host/repo'".
var credentialedURL = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://)[^/@\s]*@`)

// redactCredentials masks credentials embedded in URLs. Anything git prints on
// stderr is remote-controlled and routinely echoes back the URL it was given,
// so it must never be attached to an error verbatim.
func redactCredentials(s string) string {
	return credentialedURL.ReplaceAllString(s, "${1}xxxxx@")
}

// annotateWithStderr keeps git's own explanation attached to the error. Without
// it an unclassified failure surfaces only as "exit status 128", which is what
// made recurring CI failures unattributable.
func annotateWithStderr(err error, stderr string) error {
	if err == nil {
		return nil
	}
	// `git fetch --progress` writes sideband progress to stderr too, so the very
	// last line is often a progress update rather than the reason: prefer git's
	// own diagnostic prefixes when they are there.
	var lastLine, lastDiagnostic string
	for line := range strings.SplitSeq(strings.TrimSpace(stderr), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lastLine = line
		if strings.HasPrefix(line, "fatal:") || strings.HasPrefix(line, "error:") {
			lastDiagnostic = line
		}
	}
	if lastDiagnostic != "" {
		lastLine = lastDiagnostic
	}
	lastLine = redactCredentials(lastLine)
	if lastLine == "" || strings.Contains(err.Error(), lastLine) {
		return err
	}
	const maxLen = 300
	if len(lastLine) > maxLen {
		lastLine = lastLine[:maxLen] + "…"
	}
	return fmt.Errorf("%w: %s", err, lastLine)
}
