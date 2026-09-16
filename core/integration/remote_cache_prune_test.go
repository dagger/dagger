package core

import (
	"context"

	"github.com/dagger/testctx"
)

// Reproduce the saved-report restart under default disk-derived limits.
// The diagnostic requires actual host pressure to observe removal.
func (RemoteCacheTransferSuite) TestDefaultGCPruneDiagnostic(ctx context.Context, t *testctx.T) {
	runTransferSchemaRecovery(ctx, t, false, true)
}
