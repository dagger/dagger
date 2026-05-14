//go:build windows

package engineutil

import (
	"github.com/Microsoft/go-winio"
	"github.com/dagger/dagger/internal/buildkit/session/filesync"
	"github.com/dagger/dagger/internal/fsutil"
)

func sendDiffCopyToCaller(stream filesync.FileSend_DiffCopyClient, fs fsutil.FS, progress func(int, bool)) error {
	winio.EnableProcessPrivileges([]string{winio.SeBackupPrivilege})
	defer winio.DisableProcessPrivileges([]string{winio.SeBackupPrivilege})
	return fsutil.Send(stream.Context(), stream, fs, progress)
}
