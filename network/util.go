package network

import (
	"os"
	"path/filepath"
)

func createIfNeeded(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	if f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600); err == nil {
		return f.Close()
	} else if os.IsExist(err) {
		return nil
	} else {
		return err
	}
}
