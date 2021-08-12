package mod

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const filePath = "./cue.mod/dagger.mod.cue"
const destBasePath = "./cue.mod/pkg"
const tmpBasePath = "./cue.mod/tmp"

// A file is the parsed, interpreted form of a cue.mod file.
type file struct {
	module  string
	require []*require
}

func readPath(workspacePath string) (*file, error) {
	f, err := os.Open(path.Join(workspacePath, filePath))
	if err != nil {
		return nil, err
	}

	modFile, err := read(f)
	if err != nil {
		return nil, err
	}

	return modFile, nil
}

func read(f io.Reader) (*file, error) {
	b, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}

	lines := nonEmptyLines(b)

	if len(lines) == 0 {
		return nil, fmt.Errorf("mod file is empty, missing module name")
	}

	var module string
	if split := strings.Split(lines[0], " "); len(split) > 1 {
		module = strings.Trim(split[1], "\"")
	}

	var requires []*require
	for i := 1; i < len(lines); i++ {
		split := strings.Split(lines[i], " ")
		r, err := parseArgument(split[0])
		if err != nil {
			return nil, err
		}

		r.version = split[1]

		requires = append(requires, r)
	}

	return &file{
		module:  module,
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

func (f *file) write(workspacePath string) error {
	return ioutil.WriteFile(path.Join(workspacePath, filePath), f.contents().Bytes(), 0600)
}

func (f *file) contents() *bytes.Buffer {
	var b bytes.Buffer

	b.WriteString(fmt.Sprintf("module: %s\n\n", f.module))
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
