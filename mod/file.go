package mod

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/viper"
)

const (
	modFilePath  = "./cue.mod/dagger.mod"
	sumFilePath  = "./cue.mod/dagger.sum"
	lockFilePath = "./cue.mod/dagger.lock"
	destBasePath = "./cue.mod/pkg"
	tmpBasePath  = "./cue.mod/tmp"
)

// file is the parsed, interpreted form of dagger.mod file.
type file struct {
	requires      []*Require
	workspacePath string
}

func readPath(workspacePath string) (*file, error) {
	pMod := path.Join(workspacePath, modFilePath)
	fMod, err := os.Open(filepath.Clean(pMod))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}

		// dagger.mod doesn't exist, let's create an empty file
		if fMod, err = os.Create(filepath.Clean(pMod)); err != nil {
			return nil, err
		}
	}

	pSum := path.Join(workspacePath, sumFilePath)
	fSum, err := os.Open(filepath.Clean(pSum))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}

		// dagger.sum doesn't exist, let's create an empty file
		if fSum, err = os.Create(filepath.Clean(pSum)); err != nil {
			return nil, err
		}
	}

	modFile, err := read(fMod, fSum)
	if err != nil {
		return nil, err
	}

	modFile.workspacePath = workspacePath

	return modFile, nil
}

func read(fMod, fSum io.Reader) (*file, error) {
	bMod, err := ioutil.ReadAll(fMod)
	if err != nil {
		return nil, err
	}

	bSum, err := ioutil.ReadAll(fSum)
	if err != nil {
		return nil, err
	}

	modLines := nonEmptyLines(bMod)
	sumLines := nonEmptyLines(bSum)

	if len(modLines) != len(sumLines) {
		return nil, fmt.Errorf("length of dagger.mod and dagger.sum files differ")
	}

	requires := make([]*Require, 0, len(modLines))
	for i := 0; i < len(modLines); i++ {
		modSplit := strings.Split(modLines[i], " ")
		if len(modSplit) != 2 {
			return nil, fmt.Errorf("line in the mod file doesn't contain 2 elements")
		}

		sumSplit := strings.Split(sumLines[i], " ")
		if len(sumSplit) != 2 {
			return nil, fmt.Errorf("line in the sum file doesn't contain 2 elements")
		}

		if modSplit[0] != sumSplit[0] {
			return nil, fmt.Errorf("repos in mod and sum line don't match: %s and %s", modSplit[0], sumSplit[0])
		}

		// FIXME: if we want to add support for version constraints in the mod file, it would be here
		require, err := newRequire(modSplit[0], "")
		if err != nil {
			return nil, err
		}

		require.version = modSplit[1]
		require.checksum = sumSplit[1]

		requires = append(requires, require)
	}

	return &file{requires: requires}, nil
}

var spaceRgx = regexp.MustCompile(`\s+`)

func nonEmptyLines(b []byte) []string {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	split := strings.Split(s, "\n")

	lines := make([]string, 0, len(split))
	for _, l := range split {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}

		trimmed = spaceRgx.ReplaceAllString(trimmed, " ")
		lines = append(lines, trimmed)
	}

	return lines
}

func (f *file) install(ctx context.Context, req *Require) error {
	// cleaning up possible leftovers
	tmpPath := path.Join(f.workspacePath, tmpBasePath, req.fullPath())
	defer os.RemoveAll(tmpPath)

	// clone to a tmp directory
	r, err := clone(ctx, req, tmpPath, viper.GetString("private-key-file"), viper.GetString("private-key-password"))
	if err != nil {
		return fmt.Errorf("error downloading package %s: %w", req, err)
	}

	destPath := path.Join(f.workspacePath, destBasePath, req.fullPath())

	// requirement is new, so we should move the cloned files from tmp to pkg and add it to the mod file
	existing := f.searchInstalledRequire(req)
	if existing == nil {
		if err = replace(req, tmpPath, destPath); err != nil {
			return err
		}

		checksum, err := dirChecksum(destPath)
		if err != nil {
			return err
		}

		req.checksum = checksum

		f.requires = append(f.requires, req)

		return nil
	}

	// checkout the cloned repo to that tag, change the version in the existing requirement and
	// replace the code in the /pkg folder
	existing.version = req.version
	if err = r.checkout(ctx, req.version); err != nil {
		return err
	}

	if err = replace(req, tmpPath, destPath); err != nil {
		return err
	}

	checksum, err := dirChecksum(destPath)
	if err != nil {
		return err
	}

	existing.checksum = checksum

	return nil
}

func (f *file) updateToLatest(ctx context.Context, req *Require) (*Require, error) {
	// check if it doesn't exist
	existing := f.searchInstalledRequire(req)
	if existing == nil {
		return nil, fmt.Errorf("package %s isn't already installed", req.fullPath())
	}

	// cleaning up possible leftovers
	tmpPath := path.Join(f.workspacePath, tmpBasePath, existing.fullPath())
	defer os.RemoveAll(tmpPath)

	// clone to a tmp directory
	gitRepo, err := clone(ctx, existing, tmpPath, viper.GetString("private-key-file"), viper.GetString("private-key-password"))
	if err != nil {
		return nil, fmt.Errorf("error downloading package %s: %w", existing, err)
	}

	// checkout the latest tag
	latestTag, err := gitRepo.latestTag(ctx, req.versionConstraint)
	if err != nil {
		return nil, err
	}

	c, err := compareVersions(latestTag, existing.version)
	if err != nil {
		return nil, err
	}

	if c < 0 {
		return nil, fmt.Errorf("latest git tag is less than the current version")
	}

	existing.version = latestTag
	if err = gitRepo.checkout(ctx, existing.version); err != nil {
		return nil, err
	}

	// move the package from tmp to pkg directory
	destPath := path.Join(f.workspacePath, destBasePath, existing.fullPath())
	if err = replace(existing, tmpPath, destPath); err != nil {
		return nil, err
	}

	checksum, err := dirChecksum(destPath)
	if err != nil {
		return nil, err
	}

	req.checksum = checksum

	return existing, nil
}

func (f *file) searchInstalledRequire(r *Require) *Require {
	for _, existing := range f.requires {
		if existing.fullPath() == r.fullPath() {
			return existing
		}
	}

	return nil
}

func (f *file) ensure() error {
	for _, require := range f.requires {
		requirePath := path.Join(f.workspacePath, destBasePath, require.fullPath())

		checksum, err := dirChecksum(requirePath)
		if err != nil {
			return nil
		}

		if require.checksum != checksum {
			return fmt.Errorf("wrong checksum for %s", require.fullPath())
		}
	}

	return nil
}

func (f *file) write() error {
	// write dagger.mod file
	var bMod bytes.Buffer
	for _, r := range f.requires {
		bMod.WriteString(fmt.Sprintf("%s %s\n", r.fullPath(), r.version))
	}

	err := ioutil.WriteFile(path.Join(f.workspacePath, modFilePath), bMod.Bytes(), 0600)
	if err != nil {
		return err
	}

	// write dagger.sum file
	var bSum bytes.Buffer
	for _, r := range f.requires {
		bSum.WriteString(fmt.Sprintf("%s %s\n", r.fullPath(), r.checksum))
	}

	err = ioutil.WriteFile(path.Join(f.workspacePath, sumFilePath), bSum.Bytes(), 0600)
	if err != nil {
		return err
	}

	return nil
}
