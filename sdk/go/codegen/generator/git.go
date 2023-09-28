package generator

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/psanford/memfs"
	"github.com/vito/progrock"
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

func GitIgnorePaths(ctx context.Context, mfs *memfs.FS, outDir string, paths ...string) error {
	rec := progrock.FromContext(ctx)

	rec.Debug("ignoring", progrock.Labelf("patterns", "%+v", paths))

	content, err := os.ReadFile(filepath.Join(outDir, GitIgnoreFile))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if len(content) > 0 && !bytes.HasSuffix(content, []byte("\n")) {
		content = append(content, '\n')
	}

	for _, filePath := range paths {
		thisFile := []byte(fmt.Sprintf("/%s\n", filePath))

		if bytes.Contains(content, thisFile) {
			rec.Debug("path already in .gitignore", progrock.Labelf("path", filePath))
			// already has some config for the file
			continue
		}

		content = append(content, thisFile...)
	}

	return mfs.WriteFile(GitIgnoreFile, content, 0o600)
}
