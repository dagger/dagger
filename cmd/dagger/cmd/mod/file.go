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

const FilePath = "./cue.mod/dagger.mod.cue"

// A file is the parsed, interpreted form of a cue.mod file.
type file struct {
	module  string
	require []*require
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
	prefix  string
	repo    string
	path    string
	version string
}

func (r *require) cloneURL() string {
	return fmt.Sprintf("%s%s", r.prefix, r.repo)
}

func (r *require) fullPath() string {
	return path.Join(r.repo, r.path)
}

func read(f io.Reader) (*file, error) {
	b, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}

	lines, err := nonEmptyLines(b)
	if err != nil {
		return nil, err
	}

	if len(lines) == 0 {
		return nil, fmt.Errorf("mod file is empty")
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

func nonEmptyLines(b []byte) ([]string, error) {
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	split := strings.Split(s, "\n")

	spaceRgx, err := regexp.Compile(`\s+`)
	if err != nil {
		return nil, err
	}

	var lines []string
	for _, l := range split {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}

		trimmed = spaceRgx.ReplaceAllString(trimmed, " ")

		lines = append(lines, trimmed)
	}

	return lines, nil
}

func parseArgument(arg string) (*require, error) {
	if strings.HasPrefix(arg, "alpha.dagger.io") {
		arg = strings.Replace(arg, "alpha.dagger.io", "github.com/dagger/dagger/stdlib", 1)
	}

	name, suffix, err := parseGithubRepoName(arg)
	if err != nil {
		return nil, err
	}

	repoPath, version := parseGithubRepoVersion(suffix)

	return &require{
		prefix:  "https://",
		repo:    name,
		path:    repoPath,
		version: version,
	}, nil
}

func parseGithubRepoName(arg string) (string, string, error) {
	repoRegex, err := regexp.Compile("(github.com/[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+)(.*)")
	if err != nil {
		return "", "", err
	}

	repoMatches := repoRegex.FindStringSubmatch(arg)

	if len(repoMatches) == 0 {
		return "", "", fmt.Errorf("repo name does not match suported providers")
	}

	// returns 2 elements: repo name and path+version
	return repoMatches[1], repoMatches[2], nil
}

func parseGithubRepoVersion(repoSuffix string) (string, string) {
	if repoSuffix == "" {
		return "", ""
	}

	i := strings.LastIndexAny(repoSuffix, "@:")
	if i == -1 {
		return repoSuffix, ""
	}

	return repoSuffix[:i], repoSuffix[i+1:]
}

func readModFile() (*file, error) {
	f, err := os.Open(FilePath)
	if err != nil {
		return nil, err
	}

	modFile, err := read(f)
	if err != nil {
		return nil, err
	}

	return modFile, nil
}

func writeModFile(f *file) error {
	return ioutil.WriteFile(FilePath, f.contents().Bytes(), 0600)
}

func move(r *require, sourceRepoPath, destBasePath string) error {
	fmt.Println("move")
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
	fmt.Println("replace")
	if err := os.RemoveAll(path.Join(destBasePath, r.fullPath())); err != nil {
		return err
	}

	return move(r, sourceRepoPath, destBasePath)
}
