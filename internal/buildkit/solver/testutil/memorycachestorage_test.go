package testutil

import (
	"testing"

	"github.com/dagger/dagger/buildkit/solver"
)

func TestMemoryCacheStorage(t *testing.T) {
	RunCacheStorageTests(t, solver.NewInMemoryCacheStorage)
}
