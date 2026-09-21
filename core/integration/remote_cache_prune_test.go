package core

import (
	"context"
	"os"

	"github.com/dagger/testctx"
)

// Reproduce the saved-report restart under default disk-derived limits.
// The diagnostic requires actual host pressure to observe removal.
func (RemoteCacheTransferSuite) TestDefaultGCPruneDiagnostic(ctx context.Context, t *testctx.T) {
	if os.Getenv("_DAGGER_TEST_REMOTE_CACHE_PRUNE_DIAGNOSTIC") != "1" {
		t.Skip("set _DAGGER_TEST_REMOTE_CACHE_PRUNE_DIAGNOSTIC=1 to opt in to the disk-pressure diagnostic (up to 8 GiB temporary allocation)")
	}
	runTransferSchemaRecovery(ctx, t, false, true)
}
