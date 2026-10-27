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
