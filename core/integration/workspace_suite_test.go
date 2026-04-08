package core

import (
	"testing"

	"github.com/dagger/testctx"
)

type WorkspaceSuite struct{}

func TestWorkspace(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceSuite{})
}
