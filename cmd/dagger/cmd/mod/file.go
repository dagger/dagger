package mod

import (
	"bytes"
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

const filePath = "./cue.mod/dagger.mod"
const destBasePath = "./cue.mod/pkg"
const tmpBasePath = "./cue.mod/tmp"

// A file is the parsed, interpreted form of a cue.mod file.
type file struct {
	require []*require

	workspacePath string
}

func readPath(workspacePath string) (*file, error) {
	p := path.Join(workspacePath, filePath)

	f, err := os.Open(p)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}

		// dagger.mod.cue doesn't exist, let's create an empty file
		if f, err = os.Create(p); err != nil {
			return nil, err
		}
	}

	modFile, err := read(f)
	if err != nil {
		return nil, err
	}

	modFile.workspacePath = workspacePath

	return modFile, nil
}

func read(f io.Reader) (*file, error) {
	b, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}

	lines := nonEmptyLines(b)

	var requires []*require
	for _, line := range lines {
		split := strings.Split(line, " ")
		r, err := parseArgument(split[0])
		if err != nil {
			return nil, err
		}

		r.version = split[1]

		requires = append(requires, r)
	}

	return &file{
		require: requires,
	}, nil
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

func (f *file) processRequire(req *require, upgrade bool) (bool, error) {
	var isNew bool

	tmpPath := path.Join(f.workspacePath, tmpBasePath, req.repo)
	if err := os.MkdirAll(tmpPath, 0755); err != nil {
		return false, fmt.Errorf("error creating tmp dir for cloning package")
	}
	defer os.RemoveAll(tmpPath)

	// clone the repo
	privateKeyFile := viper.GetString("private-key-file")
	privateKeyPassword := viper.GetString("private-key-password")
	r, err := clone(req, tmpPath, privateKeyFile, privateKeyPassword)
	if err != nil {
		return isNew, fmt.Errorf("error downloading package %s: %w", req, err)
	}

	existing := f.search(req)
	destPath := path.Join(f.workspacePath, destBasePath)

	// requirement is new, so we should move the files and add it to the mod file
	if existing == nil {
		if err := move(req, tmpPath, destPath); err != nil {
			return isNew, err
		}
		f.require = append(f.require, req)
		isNew = true
		return isNew, nil
	}

	if upgrade {
		latestTag, err := r.latestTag()
		if err != nil {
			return isNew, err
		}

		if latestTag == "" {
			return isNew, fmt.Errorf("repo does not have a tag")
		}

		req.version = latestTag
	}

	c, err := compareVersions(existing.version, req.version)
	if err != nil {
		return isNew, err
	}

	// the existing requirement is newer or equal so we skip installation
	if c >= 0 {
		return isNew, nil
	}

	// the new requirement is newer so we checkout the cloned repo to that tag, change the version in the existing
	// requirement and replace the code in the /pkg folder
	existing.version = req.version
	if err = r.checkout(req.version); err != nil {
		return isNew, err
	}
	if err = replace(req, tmpPath, destPath); err != nil {
		return isNew, err
	}
	isNew = true

	return isNew, nil
}

func (f *file) write() error {
	return ioutil.WriteFile(path.Join(f.workspacePath, filePath), f.contents().Bytes(), 0600)
}

func (f *file) contents() *bytes.Buffer {
	var b bytes.Buffer

	for _, r := range f.require {
		b.WriteString(fmt.Sprintf("%s %s\n", r.fullPath(), r.version))
	}

	return &b
}

func (f *file) search(r *require) *require {
	for _, existing := range f.require {
		if existing.fullPath() == r.fullPath() {
			return existing
		}
	}
	return nil
}

type require struct {
	repo    string
	path    string
	version string

	cloneRepo string
	clonePath string
}

func (r *require) fullPath() string {
	return path.Join(r.repo, r.path)
}

func move(r *require, sourceRepoPath, destBasePath string) error {
	destPath := path.Join(destBasePath, r.fullPath())
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	if err := os.Rename(path.Join(sourceRepoPath, r.path), destPath); err != nil {
		return err
	}

	return nil
}

func replace(r *require, sourceRepoPath, destBasePath string) error {
	if err := os.RemoveAll(path.Join(destBasePath, r.fullPath())); err != nil {
		return err
	}

	return move(r, sourceRepoPath, destBasePath)
}
