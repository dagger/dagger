package layercopy

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/internal/fsutil"
	"github.com/dagger/dagger/util/fsxutil"
	"github.com/moby/patternmatcher"
)

type matcher struct {
	only        map[string]struct{}
	onlyParents map[string]struct{}
	include     *patternmatcher.PatternMatcher
	exclude     *patternmatcher.PatternMatcher
	gitignore   *fsxutil.GitignoreMatcher

	// onlyPrefixIncludes is true when every include pattern is a literal path,
	// optionally followed by a trailing glob. Then a directory that matches no
	// include pattern and is not a prefix of one cannot contain a match, so it
	// does not need to be walked. This mirrors internal/fsutil's filterFS.
	onlyPrefixIncludes bool
}

type matchState struct {
	include patternmatcher.MatchInfo
	exclude patternmatcher.MatchInfo

	// includeMatched is whether the include patterns alone matched the path.
	includeMatched bool
}

func newMatcher(_ string, filter Filter) (*matcher, error) {
	m := &matcher{}
	if filter.Only != nil {
		m.only = map[string]struct{}{}
		m.onlyParents = map[string]struct{}{
			"": {},
		}
		for p := range filter.Only {
			p = filepath.ToSlash(cleanRel(p))
			m.only[p] = struct{}{}
			for parent := filepath.Dir(p); parent != "." && parent != string(filepath.Separator); parent = filepath.Dir(parent) {
				parent = filepath.ToSlash(cleanRel(parent))
				m.onlyParents[parent] = struct{}{}
			}
		}
	}
	if len(filter.Include) > 0 {
		pm, err := patternmatcher.New(filter.Include)
		if err != nil {
			return nil, err
		}
		m.include = pm

		patternChars := "*[]?^"
		if filepath.Separator != '\\' {
			patternChars += `\`
		}
		m.onlyPrefixIncludes = true
		for _, p := range pm.Patterns() {
			if !p.Exclusion() && strings.ContainsAny(patternWithoutTrailingGlob(p), patternChars) {
				m.onlyPrefixIncludes = false
				break
			}
		}
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

func (m *matcher) shouldDescend(rel string, state matchState) bool {
	rel = cleanRel(rel)
	if m.only != nil {
		if _, ok := m.onlyParents[rel]; !ok {
			return false
		}
	}
	if rel == "" || m.include == nil || !m.onlyPrefixIncludes || state.includeMatched {
		return true
	}
	// Same rule as internal/fsutil's filterFS: skip the directory unless some
	// include pattern lies beneath it.
	dirSlash := filepath.ToSlash(rel) + "/"
	for _, pat := range m.include.Patterns() {
		if pat.Exclusion() {
			continue
		}
		patStr := filepath.ToSlash(patternWithoutTrailingGlob(pat)) + "/"
		if strings.HasPrefix(patStr, dirSlash) {
			return true
		}
	}
	return false
}

// patternWithoutTrailingGlob is from internal/fsutil.
func patternWithoutTrailingGlob(p *patternmatcher.Pattern) string {
	patStr := p.String()
	// We use filepath.Separator here because patternmatcher.Pattern patterns
	// get transformed to use the native path separator:
	// https://github.com/moby/patternmatcher/blob/130b41bafc16209dc1b52a103fdac1decad04f1a/patternmatcher.go#L52
	patStr = strings.TrimSuffix(patStr, string(filepath.Separator)+"**")
	patStr = strings.TrimSuffix(patStr, string(filepath.Separator)+"*")
	return patStr
}

func (m *matcher) includePath(rel string, abs string, info os.FileInfo, parent matchState) (bool, matchState, error) {
	if rel == "" {
		return true, parent, nil
	}
	rel = filepath.ToSlash(cleanRel(rel))

	include := true
	state := parent
	if m.only != nil {
		_, include = m.only[rel]
	}
	if m.include != nil {
		matched, includeInfo, err := m.include.MatchesUsingParentResults(rel, parent.include)
		if err != nil {
			return false, state, err
		}
		state.include = includeInfo
		state.includeMatched = matched
		include = include && matched
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
