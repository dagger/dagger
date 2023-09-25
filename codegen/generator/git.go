package generator

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/psanford/memfs"
)

const (
	GitAttributesFile = ".gitattributes"
	GitIgnoreFile     = ".gitignore"
)

func MarkGeneratedAttributes(mfs *memfs.FS, outDir string, fileNames ...string) error {
	content, err := os.ReadFile(filepath.Join(outDir, GitAttributesFile))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", GitAttributesFile, err)
	}

	if !bytes.HasSuffix(content, []byte("\n")) {
		content = append(content, '\n')
	}

	for _, fileName := range fileNames {
		if bytes.Contains(content, []byte(fileName)) {
			// already has some config for the file
			continue
		}

		thisFile := []byte(fmt.Sprintf("/%s linguist-generated=true\n", fileName))

		content = append(content, thisFile...)
	}

	return mfs.WriteFile(GitAttributesFile, content, 0600)
}

func GitIgnorePaths(mfs *memfs.FS, outDir string, patterns ...string) error {
	content, err := os.ReadFile(filepath.Join(outDir, GitAttributesFile))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", GitAttributesFile, err)
	}

	if !bytes.HasSuffix(content, []byte("\n")) {
		content = append(content, '\n')
	}

	for _, fileName := range patterns {
		if bytes.Contains(content, []byte(fileName)) {
			// already has some config for the file
			continue
		}

		thisFile := []byte(fmt.Sprintf("/%s\n", fileName))

		content = append(content, thisFile...)
	}

	return mfs.WriteFile(GitAttributesFile, content, 0600)
}
