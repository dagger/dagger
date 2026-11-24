//go:build !linux
// +build !linux

package snapshots

import (
	"context"
	"runtime"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/pkg/errors"
)

func (sr *immutableRef) ExportFileChain(_ context.Context, _ config.RefConfig, _ string) (*ExportChain, error) {
	return nil, errors.New("exporting a file's own content is not supported on " + runtime.GOOS)
}

// OwnFileSize reports no file as traveling as its own content: no export
// sends one on this platform.
func (cm *snapshotManager) OwnFileSize(_ context.Context, _, _ string) (int64, bool, error) {
	return 0, false, nil
}
