package mod

import (
	"strings"
	"testing"
)

func TestReadFile(t *testing.T) {
	cases := []struct {
		name    string
		modFile string
		sumFile string
		want    *file
	}{
		{
			name: "module file with valid dependencies",
			modFile: `
				github.com/tjovicic/test xyz
				github.com/bla/bla abc
			`,
			sumFile: `
				github.com/tjovicic/test h1:hash
				github.com/bla/bla h1:hash
			`,
			want: &file{
				requires: []*Require{
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
			got, err := read(strings.NewReader(c.modFile), strings.NewReader(c.sumFile))
			if err != nil {
				t.Error(err)
			}

			if len(got.requires) != len(c.want.requires) {
				t.Errorf("requires length differs: want %d, got %d", len(c.want.requires), len(got.requires))
			}
		})
	}
}
