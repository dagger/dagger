package core

// This file registers `WorkspaceSuite`, the shared suite used by focused
// workspace test files.

import (
	"testing"

	"github.com/dagger/testctx"
)

type WorkspaceSuite struct{}

func TestWorkspace(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceSuite{})
}
