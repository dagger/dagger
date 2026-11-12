package layercopy

import (
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

	// includeHeads holds the literal directories each include pattern starts
	// with, up to its first component with a pattern character, or is nil when
	// a pattern can match anywhere. Only PathTree walks by them; see
	// mayHoldIncluded.
	includeHeads []string
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
		m.includeHeads = includeHeads(pm.Patterns(), patternChars)
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

// includeHeads returns the literal leading directories of the include
// patterns: the components of each before the first one with a pattern
// character. It's nil when a pattern has no such directory, so it can match
// anywhere.
func includeHeads(patterns []*patternmatcher.Pattern, patternChars string) []string {
	var heads []string
	for _, p := range patterns {
		if p.Exclusion() {
			continue
		}
		var literal []string
		for _, part := range strings.Split(filepath.ToSlash(p.String()), "/") {
			if part == "" || part == "." || part == ".." || strings.ContainsAny(part, patternChars) {
				break
			}
			literal = append(literal, part)
		}
		if len(literal) == 0 {
			return nil
		}
		heads = append(heads, strings.Join(literal, "/"))
	}
	return heads
}

// mayHoldIncluded reports whether the directory rel can hold a path the
// include patterns select: it lies on the way to one of their literal leading
// directories or under one, or a pattern already matched it. A pattern can
// only match below its literal leading directory, so no other directory can.
// The copy itself walks by shouldDescend alone; PathTree adds this, so a
// selection over a large tree visits only the directories a glob pattern can
// reach, and still selects what the copy does.
func (m *matcher) mayHoldIncluded(rel string, state matchState) bool {
	if m.includeHeads == nil || state.includeMatched {
		return true
	}
	rel = filepath.ToSlash(cleanRel(rel))
	for _, head := range m.includeHeads {
		if rel == head || strings.HasPrefix(head, rel+"/") || strings.HasPrefix(rel, head+"/") {
			return true
		}
	}
	return false
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

// patternWithoutTrailingGlob is from internal/fsutil, except that it strips
// only one trailing glob. Stripping both would turn "a/*/**" into "a", a
// literal prefix that does not cover a/b.
func patternWithoutTrailingGlob(p *patternmatcher.Pattern) string {
	patStr := p.String()
	// We use filepath.Separator here because patternmatcher.Pattern patterns
	// get transformed to use the native path separator:
	// https://github.com/moby/patternmatcher/blob/130b41bafc16209dc1b52a103fdac1decad04f1a/patternmatcher.go#L52
	if trimmed, ok := strings.CutSuffix(patStr, string(filepath.Separator)+"**"); ok {
		return trimmed
	}
	return strings.TrimSuffix(patStr, string(filepath.Separator)+"*")
}

func (m *matcher) includePath(rel string, abs string, isDir bool, parent matchState) (bool, matchState, error) {
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
