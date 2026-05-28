package layercopy

import (
	"os"
	"path/filepath"

	"github.com/dagger/dagger/internal/fsutil"
	"github.com/dagger/dagger/util/fsxutil"
	"github.com/moby/patternmatcher"
)

type matcher struct {
	include   *patternmatcher.PatternMatcher
	exclude   *patternmatcher.PatternMatcher
	gitignore *fsxutil.GitignoreMatcher
}

type matchState struct {
	include patternmatcher.MatchInfo
	exclude patternmatcher.MatchInfo
}

func newMatcher(_ string, filter Filter) (*matcher, error) {
	m := &matcher{}
	if len(filter.Include) > 0 {
		pm, err := patternmatcher.New(filter.Include)
		if err != nil {
			return nil, err
		}
		m.include = pm
	}
	if len(filter.Exclude) > 0 {
		pm, err := patternmatcher.New(filter.Exclude)
		if err != nil {
			return nil, err
		}
		m.exclude = pm
	}
	if filter.Gitignore {
		fs, err := fsutil.NewFS("/")
		if err != nil {
			return nil, err
		}
		m.gitignore = fsxutil.NewGitIgnoreMatcher(fs)
	}
	return m, nil
}

func (m *matcher) includePath(rel string, abs string, info os.FileInfo, parent matchState) (bool, matchState, error) {
	if rel == "" {
		return true, parent, nil
	}
	rel = filepath.ToSlash(rel)

	include := true
	state := parent
	if m.include != nil {
		var err error
		include, state.include, err = m.include.MatchesUsingParentResults(rel, parent.include)
		if err != nil {
			return false, state, err
		}
	}
	if m.exclude != nil {
		excluded, excludeInfo, err := m.exclude.MatchesUsingParentResults(rel, parent.exclude)
		state.exclude = excludeInfo
		if err != nil {
			return false, state, err
		}
		if excluded {
			include = false
		}
	}
	if m.gitignore != nil {
		isDir := info == nil || info.IsDir()
		ignored, err := m.gitignore.Matches(abs, isDir)
		if err != nil {
			return false, state, err
		}
		if ignored {
			include = false
		}
	}
	return include, state, nil
}
