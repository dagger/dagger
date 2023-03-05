package network

import (
	"os"

	"github.com/adrg/xdg"
)

func touchXDGFile(path string) (string, error) {
	xdgPath, err := xdg.RuntimeFile(path)
	if err != nil {
		return "", err
	}

	if err := createIfNeeded(xdgPath); err != nil {
		return "", err
	}

	return xdgPath, nil
}

func createIfNeeded(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}

	if f, err := os.Create(path); err == nil {
		return f.Close()
	} else if os.IsExist(err) {
		return nil
	} else {
		return err
	}
}
