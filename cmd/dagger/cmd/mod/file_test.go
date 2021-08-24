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
			name: "module file with valid dependencies",
			input: `
				github.com/tjovicic/test xyz
				github.com/bla/bla abc
			`,
			want: &file{
				require: []*require{
					{
						repo:    "github.com/tjovicic/test",
						path:    "",
						version: "xyz",
					},
					{
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

			if len(got.require) != len(c.want.require) {
				t.Errorf("requires length differs: want %d, got %d", len(c.want.require), len(got.require))
			}
		})
	}
}
