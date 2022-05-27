package engine

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/mitchellh/go-homedir"
)

const (
	IDFilePath = "~/.config/dagger/cli_id"
)

func ID() (string, error) {
	idFile, err := homedir.Expand(IDFilePath)
	if err != nil {
		return "", err
	}
	id, err := os.ReadFile(idFile)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}

		if err := os.MkdirAll(filepath.Dir(idFile), 0755); err != nil {
			return "", err
		}

		id = []byte(uuid.NewString())
		if err := os.WriteFile(idFile, id, 0600); err != nil {
			return "", err
		}
	}
	return string(id), nil
}
