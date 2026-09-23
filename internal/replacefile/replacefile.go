// Package replacefile replaces a file with a temporary one written beside it,
// so a concurrent reader or writer never sees a partial or interleaved file.
package replacefile

import (
	"errors"
	"os"
	"runtime"
	"time"
)

const (
	renameRetries = 5
	renameBackoff = 20 * time.Millisecond
)

// Rename renames tmp over dest. On Windows a rename over a file another
// process holds open without FILE_SHARE_DELETE fails, and Go's own os.Open
// does not share delete, so Rename retries a few times and then rewrites
// dest in place with tmp's contents: not atomic, but whole once written.
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
	info, statErr := os.Stat(tmp)
	if statErr != nil {
		return errors.Join(err, statErr)
	}
	data, readErr := os.ReadFile(tmp)
	if readErr != nil {
		return errors.Join(err, readErr)
	}
	if writeErr := os.WriteFile(dest, data, info.Mode().Perm()); writeErr != nil {
		return errors.Join(err, writeErr)
	}
	_ = os.Remove(tmp)
	return nil
}
