package testutil

import (
	"testing"

	"github.com/moby/buildkit/solver"
)

func TestMemoryCacheStorage(t *testing.T) {
	RunCacheStorageTests(t, solver.NewInMemoryCacheStorage)
}
