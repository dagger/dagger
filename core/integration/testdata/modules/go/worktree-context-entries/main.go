package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

// Entries returns the names at the root of the module's context directory.
//
// The +defaultPath="/" argument resolves to the module context root. When the
// module is loaded from a git worktree checkout — whose .git is a dangling
// POINTER FILE rather than a directory — the engine drops that root .git file
// from the synced context snapshot, so it must not appear among these entries.
func (*Test) Entries(
	ctx context.Context,
	// +defaultPath="/"
	dir *dagger.Directory,
) ([]string, error) {
	return dir.Entries(ctx)
}
