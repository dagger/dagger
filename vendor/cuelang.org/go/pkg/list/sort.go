// Copyright 2018 The CUE Authors
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

// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package list

import (
	"sort"

	"cuelang.org/go/cue"
)

// valueSorter defines a sort.Interface; implemented in cue/builtinutil.go.
type valueSorter struct {
	a   []cue.Value
	cmp cue.Value
	err error
}

func (s *valueSorter) ret() ([]cue.Value, error) {
	if s.err != nil {
		return nil, s.err
	}
	// The input slice is already a copy and that we can modify it safely.
	return s.a, nil
}

func (s *valueSorter) Len() int      { return len(s.a) }
func (s *valueSorter) Swap(i, j int) { s.a[i], s.a[j] = s.a[j], s.a[i] }
func (s *valueSorter) Less(i, j int) bool {
	v := s.cmp.Fill(s.a[i], "x")
	v = v.Fill(s.a[j], "y")
	isLess, err := v.Lookup("less").Bool()
	if err != nil && s.err == nil {
		s.err = err
		return true
	}
	return isLess
}

// Sort sorts data while keeping the original order of equal elements.
// It does O(n*log(n)) comparisons.
//
// cmp is a struct of the form {T: _, x: T, y: T, less: bool}, where
// less should reflect x < y.
//
// Example:
//
//    Sort([2, 3, 1], list.Ascending)
//
//    Sort{{a: 2}, {a: 3}, {a: 1}, {x: {}, y: {}, less: x.a < y.a}}
//
func Sort(list []cue.Value, cmp cue.Value) (sorted []cue.Value, err error) {
	s := valueSorter{list, cmp, nil}
	// The input slice is already a copy and that we can modify it safely.
	sort.Stable(&s)
	return s.ret()
}

// Deprecated: use Sort, which is always stable
func SortStable(list []cue.Value, cmp cue.Value) (sorted []cue.Value, err error) {
	s := valueSorter{list, cmp, nil}
	sort.Stable(&s)
	return s.ret()
}

// Strings sorts a list of strings in increasing order.
func SortStrings(a []string) []string {
	sort.Strings(a)
	return a
}

// IsSorted tests whether a list is sorted.
//
// See Sort for an example comparator.
func IsSorted(list []cue.Value, cmp cue.Value) bool {
	s := valueSorter{list, cmp, nil}
	return sort.IsSorted(&s)
}

// IsSortedStrings tests whether a list is a sorted lists of strings.
func IsSortedStrings(a []string) bool {
	return sort.StringsAreSorted(a)
}
