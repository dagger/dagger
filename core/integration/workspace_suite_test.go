package core

// Workspace alignment: aligned; this is a suite-registration file for a multi-file workspace-era suite.
// Scope: Registers the workspace suite and keeps shared testctx setup in one place.
// Intent: Make the workspace suite boundary explicit while behavior lives in the focused workspace files.

import (
	"testing"

	"github.com/dagger/testctx"
)

type WorkspaceSuite struct{}

func TestWorkspace(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceSuite{})
}
