// Copyright 2021 CUE Authors
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

package cuecontext

import (
	"cuelang.org/go/cue"
	"cuelang.org/go/internal/core/runtime"

	_ "cuelang.org/go/pkg"
)

// Option controls a build context.
type Option interface{ buildOption() }

// New creates a new Context.
func New(options ...Option) *cue.Context {
	r := runtime.New()
	return (*cue.Context)(r)
}
