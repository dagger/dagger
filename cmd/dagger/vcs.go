package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/vito/progrock"
)

const (
	gitAttributesFile = ".gitattributes"
	gitIgnoreFile     = ".gitignore"
)

func automateVCS(ctx context.Context, moduleDir string, codegen *dagger.GeneratedCode) error {
	rec := progrock.FromContext(ctx)

	repo, err := git.PlainOpenWithOptions(moduleDir, &git.PlainOpenOptions{
		DetectDotGit: true,
	})
	if err != nil {
		if errors.Is(err, git.ErrRepositoryNotExists) {
			rec.Debug("skipping VCS automation (not in a git repo)")
		} else {
			rec.Warn("skipping VCS automation (failed to open git repo)", progrock.ErrorLabel(err))
		}
		return nil
	}

	ignorePaths, err := codegen.VcsIgnoredPaths(ctx)
	if err != nil {
		return fmt.Errorf("failed to get vcs ignored paths: %w", err)
	}
	if err := gitIgnorePaths(ctx, repo, moduleDir, ignorePaths...); err != nil {
		return fmt.Errorf("failed to update .gitignore: %w", err)
	}

	generatedPaths, err := codegen.VcsGeneratedPaths(ctx)
	if err != nil {
		return fmt.Errorf("failed to get vcs ignored paths: %w", err)
	}
	if err := gitMarkGeneratedAttributes(ctx, moduleDir, generatedPaths...); err != nil {
		return fmt.Errorf("failed to update .gitignore: %w", err)
	}

	return nil
}

func gitMarkGeneratedAttributes(ctx context.Context, outDir string, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}

	rec := progrock.FromContext(ctx)

	rec.Debug("marking generated", progrock.Labelf("paths", "%+v", paths))

	attrFilePath := filepath.Join(outDir, gitAttributesFile)

	content, err := os.ReadFile(attrFilePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", gitAttributesFile, err)
	}

	if !bytes.HasSuffix(content, []byte("\n")) {
		content = append(content, '\n')
	}

	for _, fileName := range paths {
		if bytes.Contains(content, []byte(fileName)) {
			// already has some config for the file
			continue
		}

		thisFile := []byte(fmt.Sprintf("/%s linguist-generated=true\n", fileName))

		content = append(content, thisFile...)
	}

	return os.WriteFile(attrFilePath, content, 0600)
}

func gitIgnorePaths(ctx context.Context, repo *git.Repository, outDir string, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}

	rec := progrock.FromContext(ctx)

	rec.Debug("ignoring", progrock.Labelf("patterns", "%+v", paths))

	ignoreFilePath := filepath.Join(outDir, gitIgnoreFile)

	content, err := os.ReadFile(ignoreFilePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if len(content) > 0 && !bytes.HasSuffix(content, []byte("\n")) {
		content = append(content, '\n')
	}

	workTree, err := repo.Worktree()
	if err != nil {
		return err
	}

	for _, filePath := range paths {
		thisFile := []byte(fmt.Sprintf("/%s\n", filePath))

		if bytes.HasPrefix(content, thisFile) || bytes.Contains(content, append([]byte("\n"), thisFile...)) {
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
			if !errors.Is(err, index.ErrEntryNotFound) {
				rec.Warn("failed to remove .gitignored path",
					progrock.Labelf("gitPath", relPath),
					progrock.ErrorLabel(err))
			}
		} else {
			rec.Warn("removed .gitignored path from index",
				progrock.Labelf("gitPath", relPath))
		}
	}

	return os.WriteFile(ignoreFilePath, content, 0o600)
}
