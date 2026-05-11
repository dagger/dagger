package core

// This file registers `ModuleSuite`, the shared suite used by focused module
// test files.

import (
	"testing"

	"github.com/dagger/testctx"
)

type ModuleSuite struct{}

func TestModule(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ModuleSuite{})
}
