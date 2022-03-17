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

package cli

//go:generate go run gen.go
//go:generate gofmt -s -w .

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/internal/task"
)

func init() {
	task.Register("tool/cli.Print", newPrintCmd)
	task.Register("tool/cli.Ask", newAskCmd)

	// For backwards compatibility.
	task.Register("print", newPrintCmd)
}

type printCmd struct{}

func newPrintCmd(v cue.Value) (task.Runner, error) {
	return &printCmd{}, nil
}

func (c *printCmd) Run(ctx *task.Context) (res interface{}, err error) {
	str := ctx.String("text")
	if ctx.Err != nil {
		return nil, ctx.Err
	}
	fmt.Fprintln(ctx.Stdout, str)
	return nil, nil
}

type askCmd struct{}

func newAskCmd(v cue.Value) (task.Runner, error) {
	return &askCmd{}, nil
}

func (c *askCmd) Run(ctx *task.Context) (res interface{}, err error) {
	str := ctx.String("prompt")
	if ctx.Err != nil {
		return nil, ctx.Err
	}
	if str != "" {
		fmt.Fprint(ctx.Stdout, str+" ")
	}

	var response string
	if _, err := fmt.Scan(&response); err != nil {
		return nil, err
	}

	update := map[string]interface{}{"response": response}

	switch v := ctx.Lookup("response"); v.IncompleteKind() {
	case cue.BoolKind:
		switch strings.ToLower(response) {
		case "yes":
			update["response"] = true
		default:
			update["response"] = false
		}
	case cue.StringKind:
		// already set above
	}
	return update, nil
}
