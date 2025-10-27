package core

import (
	"testing"

	"github.com/dagger/testctx"
)

type ChecksSuite struct{}

func (s *ChecksSuite) TestChecks(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ChecksSuite{})
}
