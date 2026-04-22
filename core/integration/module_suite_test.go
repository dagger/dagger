package core

// Workspace alignment: mostly aligned; this file now exists only to register the multi-file module suite.
// Scope: ModuleSuite registration for focused module-owned test files.
// Intent: Keep one stable suite entrypoint while behavior coverage lives in narrower files with explicit ownership.

import (
	"testing"

	"github.com/dagger/testctx"
)

type ModuleSuite struct{}

func TestModule(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ModuleSuite{})
}
