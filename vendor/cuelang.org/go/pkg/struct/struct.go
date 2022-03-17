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

// Package struct defines utilities for struct types.
package structs

import (
	"cuelang.org/go/cue"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
	"cuelang.org/go/internal/core/adt"
)

// MinFields validates the minimum number of fields that are part of a struct.
// It can only be used as a validator, for instance `MinFields(3)`.
//
// Only fields that are part of the data model count. This excludes hidden
// fields, optional fields, and definitions.
func MinFields(object *cue.Struct, n int) *adt.Bottom {
	iter := object.Fields(cue.Hidden(false), cue.Optional(false))
	count := 0
	for iter.Next() {
		count++
	}
	if count < n {
		return &adt.Bottom{
			Code: adt.IncompleteError, // could still be resolved
			Err:  errors.Newf(token.NoPos, "len(fields) < MinFields(%[2]d) (%[1]d < %[2]d)", count, n),
		}
	}
	return nil
}

// MaxFields validates the maximum number of fields that are part of a struct.
// It can only be used as a validator, for instance `MaxFields(3)`.
//
// Only fields that are part of the data model count. This excludes hidden
// fields, optional fields, and definitions.
func MaxFields(object *cue.Struct, n int) (bool, error) {
	iter := object.Fields(cue.Hidden(false), cue.Optional(false))
	count := 0
	for iter.Next() {
		count++
	}
	// permanent error is okay here.
	return count <= n, nil
}
