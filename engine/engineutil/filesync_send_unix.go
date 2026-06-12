//go:build !windows

package engineutil

import (
	"github.com/dagger/dagger/internal/buildkit/session/filesync"
	"github.com/dagger/dagger/internal/fsutil"
)

func sendDiffCopyToCaller(stream filesync.FileSend_DiffCopyClient, fs fsutil.FS, progress func(int, bool)) error {
	return fsutil.Send(stream.Context(), stream, fs, progress)
}
