package core

import (
	"testing"

	"github.com/dagger/testctx"
)

type PrivateDepsSuite struct{}

func TestPrivateDeps(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(PrivateDepsSuite{})
}
