package daggercmd

import (
	"testing"

	"dagger.io/dagger/core"

	"github.com/dagger/dagger/core/artifact"
	"github.com/dagger/dagger/core/dagaddress"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestCallUnknownRootFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "no args"},
		{name: "function only", args: []string{"projects", "keys"}},
		{name: "own flag", args: []string{"--json", "projects"}},
		{name: "own flag with value", args: []string{"--output=/tmp/x", "projects"}},
		{name: "shorthand with value", args: []string{"-m", "./mod", "items", "keys"}},
		{name: "shorthand run", args: []string{"-vv", "projects"}},
		{name: "function flag is the function's own", args: []string{"build", "--platform=linux/amd64"}},
		{name: "value that looks like a function", args: []string{"-m", "projects", "keys"}},
		{name: "selector", args: []string{"--runner-suite=unit.test.ts", "file"}, want: true},
		{name: "selector with separate value", args: []string{"--runner-suite", "unit.test.ts", "file"}, want: true},
		{name: "selector after own flag", args: []string{"-m", "./mod", "--runner-suite=x", "file"}, want: true},
		{name: "terminator stops the scan", args: []string{"--", "--runner-suite=x"}},
		// cobraBuilder registers these while building the tree, so they are
		// not on the command yet when the scan runs.
		{name: "constructor arg", args: []string{"--source=.", "build"}},
		{name: "constructor arg with separate value", args: []string{"--source", ".", "build"}},
		{name: "constructor arg in camel case", args: []string{"--dockerConfig=/tmp/cfg", "build"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "call"}
			cmd.Flags().BoolP("json", "j", false, "")
			cmd.Flags().StringP("output", "o", "", "")
			cmd.Flags().StringP("load-module", "m", "", "")
			cmd.Flags().CountP("verbose", "v", "")
			fc := &FuncCommand{mod: modWithConstructorArgs("source", "dockerConfig")}
			require.Equal(t, tc.want, fc.hasUnknownRootFlag(cmd, tc.args))
		})
	}
}

// modWithConstructorArgs is a Query root whose `with` field takes the given
// arguments, which is how a module's constructor flags reach the root command.
func modWithConstructorArgs(args ...string) *moduleDef {
	withFn := &modFunction{Name: "with"}
	for _, name := range args {
		withFn.Args = append(withFn.Args, &modFunctionArg{
			Name:    name,
			TypeDef: &modTypeDef{Kind: core.TypeDefKindStringKind},
		})
	}
	query := &modObject{Name: "Query", Functions: []*modFunction{withFn}}
	rootType := &modTypeDef{Kind: core.TypeDefKindObjectKind, AsObject: query}
	// The root command selects through the no-op identity constructor, the
	// shape a workspace entrypoint has.
	query.Constructor = &modFunction{ReturnType: rootType}
	return &moduleDef{
		MainObject: rootType,
		Objects:    []*modTypeDef{rootType},
	}
}

func TestCallSelectionType(t *testing.T) {
	dimensions := artifact.Dimensions{
		{Identifier: artifact.ModuleDimension, Kind: "MODULE", Name: "module", KeyName: "name"},
		{Identifier: "/runner/projects", Name: "runner-project", ItemType: "RunnerProject", CollectionType: "RunnerProjects", KeyName: "path"},
		{Identifier: "/runner/projects/suites", Name: "runner-suite", ItemType: "RunnerSuite", CollectionType: "RunnerSuites", KeyName: "file"},
		{Identifier: "type:Check", Kind: "TYPE", Name: "check", ItemType: "Check", KeyName: "name"},
	}
	for _, tc := range []struct {
		name string
		keys []dagaddress.Pair
		want string
	}{
		{name: "none"},
		{name: "outer", keys: []dagaddress.Pair{{Dimension: "/runner/projects", Key: "./api", HasKey: true}}, want: "RunnerProject"},
		{
			name: "nested wins over outer",
			keys: []dagaddress.Pair{
				{Dimension: "/runner/projects", Key: "./api", HasKey: true},
				{Dimension: "/runner/projects/suites", Key: "unit.test.ts", HasKey: true},
			},
			want: "RunnerSuite",
		},
		{
			name: "order does not matter",
			keys: []dagaddress.Pair{
				{Dimension: "/runner/projects/suites", Key: "unit.test.ts", HasKey: true},
				{Dimension: "/runner/projects", Key: "./api", HasKey: true},
			},
			want: "RunnerSuite",
		},
		{name: "module alone names no item", keys: []dagaddress.Pair{{Dimension: artifact.ModuleDimension, Key: "runner", HasKey: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, artifactSelectionType(tc.keys, dimensions))
		})
	}
}
