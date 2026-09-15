package core

// This file registers the module CLI suite and shared setup. Behavior coverage
// lives in the focused module CLI test files.

import (
	"testing"

	"github.com/dagger/testctx"
)

type CLISuite struct{}

func TestCLI(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CLISuite{})
}
