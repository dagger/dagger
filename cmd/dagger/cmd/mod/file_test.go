package mod

import (
	"strings"
	"testing"
)

func TestReadFile(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  *file
	}{
		{
			name: "module file without dependencies",
			input: `
				module: "alpha.dagger.io"
			`,
			want: &file{
				module: "alpha.dagger.io",
			},
		},
		{
			name: "module file with valid dependencies",
			input: `
				module: "alpha.dagger.io"
	
				github.com/tjovicic/test xyz
				github.com/bla/bla abc
			`,
			want: &file{
				module: "alpha.dagger.io",
				require: []*require{
					{
						prefix:  "https://",
						repo:    "github.com/tjovicic/test",
						path:    "",
						version: "xyz",
					},
					{
						prefix:  "https://",
						repo:    "github.com/bla/bla",
						path:    "",
						version: "abc",
					},
				},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := read(strings.NewReader(c.input))
			if err != nil {
				t.Error(err)
			}

			if got.module != c.want.module {
				t.Errorf("module names differ: want %s, got %s", c.want.module, got.module)
			}

			if len(got.require) != len(c.want.require) {
				t.Errorf("requires length differs: want %d, got %d", len(c.want.require), len(got.require))
			}
		})
	}
}
