package schema

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mitchellh/go-spdx"

	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/slog"
)

const defaultLicenseID = "Apache-2.0"

//go:embed licenses/Apache-2.0.txt
var defaultLicenseText string

// licenseFiles is the list of filenames recognized as license files,
// searched in order within each directory up to the git root.
var licenseFiles = []string{
	"COPYING",
	"COPYING.md",
	"COPYING.markdown",
	"COPYING.txt",
	"LICENCE",
	"LICENCE.md",
	"LICENCE.markdown",
	"LICENCE.txt",
	"LICENSE",
	"LICENSE.md",
	"LICENSE.markdown",
	"LICENSE.txt",
	"LICENSE-2.0.txt",
	"LICENCE-2.0.txt",
	"LICENSE-APACHE",
	"LICENCE-APACHE",
	"LICENSE-APACHE-2.0.txt",
	"LICENCE-APACHE-2.0.txt",
	"LICENSE-MIT",
	"LICENCE-MIT",
	"LICENSE.MIT",
	"LICENCE.MIT",
	"LICENSE.code",
	"LICENCE.code",
	"LICENSE.docs",
	"LICENCE.docs",
	"LICENSE.rst",
	"LICENCE.rst",
	"MIT-LICENSE",
	"MIT-LICENCE",
	"MIT-LICENSE.md",
	"MIT-LICENCE.md",
	"MIT-LICENSE.markdown",
	"MIT-LICENCE.markdown",
	"MIT-LICENSE.txt",
	"MIT-LICENCE.txt",
	"MIT_LICENSE",
	"MIT_LICENCE",
	"UNLICENSE",
	"UNLICENCE",
}

// findOrCreateLicense searches for an existing license file in the module's
// source root (and parent directories up to the git root). If none is found,
// it generates one at the source root using the given SPDX license ID.
//
// If licenseID is empty, no license is created. If searchExisting is true,
// existing license files are searched before creating a new one.
func findOrCreateLicense(ctx context.Context, bk *buildkit.Client, srcRootAbsPath string, licenseID string, searchExisting bool) error {
	if licenseID == "" {
		return nil
	}

	lg := slog.SpanLogger(ctx, InstrumentationLibrary)

	if searchExisting {
		found, err := searchForLicense(ctx, bk, srcRootAbsPath)
		if err == nil {
			lg.Debug("found existing LICENSE file", "path", found)
			return nil
		}
	}

	lg.Warn("no LICENSE file found; generating one for you, feel free to change or remove",
		"license", licenseID)

	var licenseText string
	if licenseID == defaultLicenseID && defaultLicenseText != "" {
		licenseText = defaultLicenseText
	} else {
		license, err := spdx.License(licenseID)
		if err != nil {
			return fmt.Errorf("failed to get license %q: %w", licenseID, err)
		}
		licenseText = license.Text
	}

	// Write the license to a temp file, then export to host
	tmpFile, err := os.CreateTemp("", "dagger-license-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write([]byte(licenseText)); err != nil {
		tmpFile.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	destPath := filepath.Join(srcRootAbsPath, "LICENSE")
	if err := bk.LocalFileExport(ctx, tmpFile.Name(), "LICENSE", destPath, true); err != nil {
		return fmt.Errorf("export license: %w", err)
	}

	return nil
}

// searchForLicense walks from srcRoot up to the git root looking for
// a recognized license file. Returns the path if found, error otherwise.
func searchForLicense(ctx context.Context, bk *buildkit.Client, srcRoot string) (string, error) {
	dirs, err := pathsToGitRoot(ctx, bk, srcRoot)
	if err != nil {
		return "", err
	}

	for _, dir := range dirs {
		for _, fileName := range licenseFiles {
			licensePath := filepath.Join(dir, fileName)
			if _, err := bk.StatCallerHostPath(ctx, licensePath, false); err == nil {
				return licensePath, nil
			}
		}
	}

	return "", errors.New("no license file found")
}

// pathsToGitRoot returns a list of directories from the given path up to
// (and including) the nearest directory containing a .git entry.
func pathsToGitRoot(ctx context.Context, bk *buildkit.Client, startPath string) ([]string, error) {
	curPath := startPath
	var paths []string
	for {
		paths = append(paths, curPath)

		gitPath := filepath.Join(curPath, ".git")
		if _, err := bk.StatCallerHostPath(ctx, gitPath, false); err == nil {
			return paths, nil
		}

		absPath, err := bk.AbsPath(ctx, curPath)
		if err != nil {
			return nil, err
		}
		if absPath == "/" || absPath[len(absPath)-1] == os.PathSeparator {
			return []string{startPath}, nil
		}

		curPath = filepath.Clean(filepath.Join(curPath, ".."))
	}
}
