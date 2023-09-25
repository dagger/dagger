package generator

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
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

func GitIgnorePaths(ctx context.Context, repo *git.Repository, mfs *memfs.FS, outDir string, paths ...string) error {
	rec := progrock.FromContext(ctx)

	rec.Debug("ignoring", progrock.Labelf("patterns", "%+v", paths))

	content, err := os.ReadFile(filepath.Join(outDir, GitIgnoreFile))
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if !bytes.HasSuffix(content, []byte("\n")) {
		content = append(content, '\n')
	}

	workTree, err := repo.Worktree()
	if err != nil {
		return err
	}

	for _, filePath := range paths {
		thisFile := []byte(fmt.Sprintf("/%s\n", filePath))

		if bytes.Contains(content, thisFile) {
			rec.Debug("path already in .gitignore", progrock.Labelf("path", filePath))
			// already has some config for the file
			continue
		}

		content = append(content, thisFile...)

		abs, err := filepath.Abs(filepath.Join(outDir, filePath))
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(workTree.Filesystem.Root(), abs)
		if err != nil {
			return err
		}

		// ignore failure
		if _, err := workTree.Remove(relPath); err != nil {
			rec.Warn("failed to remove .gitignored path",
				progrock.Labelf("gitPath", relPath),
				progrock.ErrorLabel(err))
		} else {
			rec.Warn("removed .gitignored path from index",
				progrock.Labelf("gitPath", relPath))
		}
	}

	return mfs.WriteFile(GitIgnoreFile, content, 0o600)
}
