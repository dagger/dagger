//go:build !windows
// +build !windows

package filesync

import (
	"github.com/dagger/dagger/internal/fsutil"
	"github.com/pkg/errors"
)

func sendDiffCopy(stream Stream, fs fsutil.FS, progress progressCb) error {
	return errors.WithStack(fsutil.Send(stream.Context(), stream, fs, progress))
}
