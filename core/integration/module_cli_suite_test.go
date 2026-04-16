package core

import (
	"testing"

	"github.com/dagger/testctx"
)

type CLISuite struct{}

func TestCLI(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CLISuite{})
}
