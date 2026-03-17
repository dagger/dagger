package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFunctionName(t *testing.T) {
	type example struct {
		name string
		args []string
		want string
	}
	for _, test := range []example{
		{
			name: "invalid flag without equals",
			args: []string{"--cloud", "test", "specific"},
			want: "",
		},
		{
			name: "valid flag with equals before function",
			args: []string{"--cloud=true", "test", "specific"},
			want: "test",
		},
		{
			name: "no flags before function",
			args: []string{"test", "--race", "specific"},
			want: "test",
		},
		{
			name: "multiple valid flags before function",
			args: []string{"--cloud=true", "--value=test", "test", "--race=true", "specific"},
			want: "test",
		},
		{
			name: "invalid second flag without equals",
			args: []string{"--cloud=true", "--race", "test", "specific"},
			want: "",
		},
		{
			name: "empty args",
			args: []string{},
			want: "",
		},
		{
			name: "only valid flags no function",
			args: []string{"--cloud=true", "--race=true"},
			want: "",
		},
		{
			name: "single dash flags before function",
			args: []string{"-m", "dev", "test", "specific"},
			want: "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, functionName(test.args))
		})
	}
}
