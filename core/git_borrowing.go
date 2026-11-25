package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// GitBorrowing is whose credentials a Git read may borrow beyond those its
// arguments give it: which clients Query.git may ask for a credential
// helper's token or an SSH agent. The context a read is made in decides it,
// not the credentials themselves. A read that may borrow a private
// repository's credentials succeeds where one that may not fails, so a key
// that leaves out the client names it wherever a result follows from such a
// read. Query.git and those keys ask GitBorrowingOf, so the two can't drift.
type GitBorrowing string

const (
	// GitBorrowsNothing is module runtime code's: it must not implicitly use
	// the credentials of its session's clients.
	GitBorrowsNothing GitBorrowing = "nothing"
	// GitBorrowsOwn is a client's that is its own non-module parent, such as
	// the CLI: its own credential helper.
	GitBorrowsOwn GitBorrowing = "own"
	// GitBorrowsForDependencies is trusted resolution of a module's declared
	// dependencies and SDK (WithModuleDependencyResolution): the non-module
	// parent's credential helper, then the session's main client's, and the
	// main client's SSH agent when the caller has none.
	GitBorrowsForDependencies GitBorrowing = "dependencies"
	// GitBorrowsForAgent is the lookup of an address an agent's model
	// supplied (WithAgentAddressResolution): its owner's credentials, with
	// the owner's approval, for a URL without userinfo of its own.
	GitBorrowsForAgent GitBorrowing = "agent"
)

// GitBorrowingOf returns whose credentials a Git read made under ctx may
// borrow.
func GitBorrowingOf(ctx context.Context, query *Query) (GitBorrowing, error) {
	if IsModuleDependencyResolution(ctx) {
		return GitBorrowsForDependencies, nil
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return "", fmt.Errorf("git borrowing: client metadata: %w", err)
	}
	parentClientMetadata, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return "", fmt.Errorf("git borrowing: non-module parent client: %w", err)
	}
	if clientMetadata.ClientID == parentClientMetadata.ClientID {
		return GitBorrowsOwn, nil
	}
	if IsAgentAddressResolution(ctx) {
		return GitBorrowsForAgent, nil
	}
	return GitBorrowsNothing, nil
}

// GitBorrowingInput keys a call by whose credentials the Git reads it makes
// with its caller's permission may borrow (GitBorrowingOf). A call whose
// result stands for the calls it makes (dagql.CallScopes) takes it when one
// of those can be a Git read under its caller's permission, which doesn't
// keep it to its session (dagql.FieldSpec.PerClientOnlyToBorrow).
var GitBorrowingInput = dagql.ImplicitInput{
	Name: "gitBorrowing",
	Resolver: func(ctx context.Context, _ map[string]dagql.Input) (dagql.Input, error) {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return nil, err
		}
		borrowing, err := GitBorrowingOf(ctx, query)
		if err != nil {
			return nil, err
		}
		return dagql.NewString(string(borrowing)), nil
	},
}
