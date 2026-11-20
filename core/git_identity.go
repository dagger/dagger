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
// Callers decide what that means for them; a commit is attributed to whoever
// asked instead of failing (see Server.AuthorizeGitIdentity). The error is a
// *GitIdentityNotSharedError, naming them.
var ErrGitIdentityNotShared = errors.New("git identity not shared by the owning client")

// GitIdentityNotSharedError is why an owner's Git identity was not shared
// (see ErrGitIdentityNotShared), with who asked, as the engine resolved them
// (never as the caller claims), so a commit can be attributed to them instead.
type GitIdentityNotSharedError struct {
	// Agent is the name of the agent that asked, if any.
	Agent string
	// Module is the name of the module nearest the caller, if any.
	Module string
	// Reason is why it was not shared: a denial, or why nobody could answer.
	Reason error
}

func (err *GitIdentityNotSharedError) Error() string {
	if err.Reason == nil {
		return ErrGitIdentityNotShared.Error()
	}
	return ErrGitIdentityNotShared.Error() + ": " + err.Reason.Error()
}

func (err *GitIdentityNotSharedError) Unwrap() []error {
	if err.Reason == nil {
		return []error{ErrGitIdentityNotShared}
	}
	return []error{ErrGitIdentityNotShared, err.Reason}
}
