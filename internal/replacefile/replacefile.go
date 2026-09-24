// Package replacefile replaces a file with a temporary one written beside it,
// by renaming the temporary file over it.
package replacefile

import (
	"os"
	"runtime"
	"time"
)

const (
	renameRetries = 5
	renameBackoff = 20 * time.Millisecond
)

// Rename renames tmp over dest, which on Unix replaces dest atomically: a
// concurrent reader or writer never sees a partial or interleaved file. On
// Windows a rename over a file another process holds open without
// FILE_SHARE_DELETE fails, and Go's own os.Open does not share delete, so
// Rename retries a few times there and then returns the error, leaving dest
// untouched; rewriting it in place instead would bring back the interleaved
// writes the rename avoids. The caller removes tmp.
func Rename(tmp, dest string) error {
	return rename(tmp, dest, os.Rename, runtime.GOOS == "windows")
}

func rename(tmp, dest string, renameFn func(string, string) error, retry bool) error {
	err := renameFn(tmp, dest)
	if err == nil || !retry {
		return err
	}
	for range renameRetries {
		time.Sleep(renameBackoff)
		if err = renameFn(tmp, dest); err == nil {
			return nil
		}
	}
	return err
}
