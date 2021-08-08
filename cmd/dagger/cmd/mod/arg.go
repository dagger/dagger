package mod

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

func parseArgument(arg string) (*require, error) {
	if strings.HasPrefix(arg, "github.com") {
		return parseGithubRepoName(arg)
	} else if strings.HasPrefix(arg, "alpha.dagger.io") {
		return parseDaggerRepoName(arg)
	} else {
		return nil, fmt.Errorf("repo name does not match suported providers")
	}
}

var githubRepoNameRegex = regexp.MustCompile(`(github.com/[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+)([a-zA-Z0-9/_.-]*)@?([0-9a-zA-Z.-]*)`)

func parseGithubRepoName(arg string) (*require, error) {
	repoMatches := githubRepoNameRegex.FindStringSubmatch(arg)

	if len(repoMatches) < 4 {
		return nil, fmt.Errorf("issue when parsing github repo")
	}

	return &require{
		prefix:  "https://",
		repo:    repoMatches[1],
		path:    repoMatches[2],
		version: repoMatches[3],

		cloneRepo: repoMatches[1],
		clonePath: repoMatches[2],
	}, nil
}

var daggerRepoNameRegex = regexp.MustCompile(`alpha.dagger.io([a-zA-Z0-9/_.-]*)@?([0-9a-zA-Z.-]*)`)

func parseDaggerRepoName(arg string) (*require, error) {
	repoMatches := daggerRepoNameRegex.FindStringSubmatch(arg)

	if len(repoMatches) < 3 {
		return nil, fmt.Errorf("issue when parsing dagger repo")
	}

	return &require{
		prefix:  "https://",
		repo:    "alpha.dagger.io",
		path:    repoMatches[1],
		version: repoMatches[2],

		cloneRepo: "github.com/dagger/dagger",
		clonePath: path.Join("/stdlib", repoMatches[1]),
	}, nil
}
