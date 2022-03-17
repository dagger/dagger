// Copyright 2019 CUE Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package regexp

import (
	"regexp"

	"cuelang.org/go/cue/errors"
)

var errNoMatch = errors.New("no match")

// Valid reports whether the given regular expression
// is valid.
func Valid(pattern string) (bool, error) {
	_, err := regexp.Compile(pattern)
	return err == nil, err
}

// Find returns a string holding the text of the leftmost match in s of
// the regular expression. It returns bottom if there was no match.
func Find(pattern, s string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", err
	}
	m := re.FindStringIndex(s)
	if m == nil {
		return "", errNoMatch
	}
	return s[m[0]:m[1]], nil
}

// FindAll returns a list of all successive matches of the expression. It
// matches successive non-overlapping matches of the entire expression. Empty
// matches abutting a preceding match are ignored. The return value is a list
// containing the successive matches. The integer argument n indicates the
// maximum number of matches to return for n >= 0, or all matches otherwise. It
// returns bottom for no match.
func FindAll(pattern, s string, n int) ([]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	m := re.FindAllString(s, n)
	if m == nil {
		return nil, errNoMatch
	}
	return m, nil
}

// FindSubmatch returns a list of strings holding the text of the leftmost match
// of the regular expression in s and the matches, if any, of its
// subexpressions. Submatches are matches of parenthesized subexpressions (also
// known as capturing groups) within the regular expression, numbered from left
// to right in order of opening parenthesis. Submatch 0 is the match of the
// entire expression, submatch 1 the match of the first parenthesized
// subexpression, and so on. It returns bottom for no match.
func FindSubmatch(pattern, s string) ([]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	m := re.FindStringSubmatch(s)
	if m == nil {
		return nil, errNoMatch
	}
	return m, nil
}

// FindAllSubmatch finds successive matches as returned by FindSubmatch,
// observing the rules of FindAll. It returns bottom for no match.
func FindAllSubmatch(pattern, s string, n int) ([][]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	m := re.FindAllStringSubmatch(s, n)
	if m == nil {
		return nil, errNoMatch
	}
	return m, nil
}

var errNoNamedGroup = errors.New("no named groups")

// FindNamedSubmatch is like FindSubmatch, but returns a map with the names used
// in capturing groups.
//
// Example:
//     regexp.MapSubmatch(#"Hello (?P<person>\w*)!"#, "Hello World!")
//  Output:
//     [{person: "World"}]
//
func FindNamedSubmatch(pattern, s string) (map[string]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	names := re.SubexpNames()
	if len(names) == 0 {
		return nil, errNoNamedGroup
	}
	m := re.FindStringSubmatch(s)
	if m == nil {
		return nil, errNoMatch
	}
	r := make(map[string]string, len(names)-1)
	for k, name := range names {
		if name != "" {
			r[name] = m[k]
		}
	}
	return r, nil
}

// FindAllNamedSubmatch is like FindAllSubmatch, but returns a map with the
// named used in capturing groups. See FindNamedSubmatch for an example on
// how to use named groups.
func FindAllNamedSubmatch(pattern, s string, n int) ([]map[string]string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	names := re.SubexpNames()
	if len(names) == 0 {
		return nil, errNoNamedGroup
	}
	m := re.FindAllStringSubmatch(s, n)
	if m == nil {
		return nil, errNoMatch
	}
	result := make([]map[string]string, len(m))
	for i, m := range m {
		r := make(map[string]string, len(names)-1)
		for k, name := range names {
			if name != "" {
				r[name] = m[k]
			}
		}
		result[i] = r
	}
	return result, nil
}
