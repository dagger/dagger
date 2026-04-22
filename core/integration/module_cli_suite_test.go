package core

// Workspace alignment: aligned; this is a suite-registration file for a multi-file workspace-era suite.
// Scope: Registers the module CLI suite and keeps shared testctx setup in one place.
// Intent: Make the suite boundary explicit without hiding behavioral ownership in the registration file.

import (
	"testing"

	"github.com/dagger/testctx"
)

type CLISuite struct{}

func TestCLI(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CLISuite{})
}
