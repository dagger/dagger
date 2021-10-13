package mod

import (
	"fmt"
	"os"
	"path"

	"golang.org/x/mod/sumdb/dirhash"
)

func dirChecksum(dirPath string) (string, error) {
	err := cleanDirForChecksum(dirPath)
	if err != nil {
		return "", err
	}

	checksum, err := dirhash.HashDir(dirPath, "", dirhash.DefaultHash)
	if err != nil {
		return "", err
	}

	return checksum, nil
}

func cleanDirForChecksum(dirPath string) error {
	if err := os.RemoveAll(path.Join(dirPath, ".git")); err != nil {
		return fmt.Errorf("error cleaning up .git directory")
	}

	return nil
}
