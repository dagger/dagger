package core

import "errors"

// GitIdentity is a Git author identity from a client's Git configuration: its
// user.name and user.email, either of which may be unset (empty).
type GitIdentity struct {
	Name  string
	Email string
}

// ErrGitIdentityNotShared means the owning client did not let an agent or
// module use its Git identity: the owner denied it, or could not be asked.
// Callers decide what that means for them; a commit falls back to the default
// identity rather than failing (see Server.AuthorizeGitIdentity).
var ErrGitIdentityNotShared = errors.New("git identity not shared by the owning client")
