// Copyright 2020 CUE Authors
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

package export

import (
	"strconv"
	"strings"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/token"
	"cuelang.org/go/internal/core/adt"
)

func (e *exporter) stringLabel(f adt.Feature) ast.Label {
	x := f.Index()
	switch f.Typ() {
	case adt.IntLabel:
		return ast.NewLit(token.INT, strconv.Itoa(int(x)))

	case adt.DefinitionLabel, adt.HiddenLabel, adt.HiddenDefinitionLabel:
		s := f.IdentString(e.ctx)
		return ast.NewIdent(s)

	case adt.StringLabel:
		s := e.ctx.IndexToString(int64(x))
		if f == 0 || !ast.IsValidIdent(s) ||
			strings.HasPrefix(s, "#") || strings.HasPrefix(s, "_") {
			return ast.NewLit(token.STRING, literal.Label.Quote(s))
		}
		fallthrough

	default:
		return ast.NewIdent(e.ctx.IndexToString(int64(x)))
	}
}
