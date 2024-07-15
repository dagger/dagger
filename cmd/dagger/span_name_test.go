package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpanName(t *testing.T) {
	type example struct {
		name string
		args []string
		want string
	}
	for _, test := range []example{
		{args: []string{"dagger", "version"}, want: "dagger version"},
		{args: []string{"dagger", "install", "foo"}, want: "dagger install foo"},
		{args: []string{"dagger", "call", "foo"}, want: "foo"},
		{args: []string{"dagger", "call", "echo", "--msg", ""}, want: "echo --msg "},
		{args: []string{"dagger", "-m", "dev", "call", "foo"}, want: "foo"},
		{args: []string{"dagger", "call", "-m", "dev", "foo"}, want: "foo"},
		{
			args: []string{"dagger", "call", "test", "important", "--race=true", "--parallel=16"},
			want: "test important --race=true --parallel=16",
		},
		{args: []string{"dagger", "call", "--source", ".:default", "foo"}, want: "foo"},
		{args: []string{"dagger", "call", "--source=.:default", "foo"}, want: "foo"},
		{args: []string{"dagger", "call", "--source", ".:default", "foo", "--fizz"}, want: "foo --fizz"},
		{
			name: "bool arg consumed call chain",
			args: []string{"dagger", "call", "--bool", "foo", "--fizz"},
			// in this case we would have kept 0 args since `--bool` will be parsed
			// like `--bool foo`, so we want to fall back to the full command
			want: "--bool foo --fizz",
		},
	} {
		t.Run(fmt.Sprintf("%v", test.args), func(t *testing.T) {
			require.Equal(t, test.want, spanName(test.args))
		})
	}
}
